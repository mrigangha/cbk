// Package metaclient is the single choke point through which every
// Meta Graph API call flows. It exists so the platform can never hammer
// Meta hard enough to get throttled or blocked:
//
//   - token-bucket rate limiting (per access token)
//   - global concurrency cap on in-flight calls
//   - usage-header tracking (x-business-use-case-usage / x-app-usage)
//     with proactive slowdown and self-pause before Meta cuts us off
//   - circuit breaker per token: consecutive throttle responses open it
//     for an exponentially growing cooldown; calls fail FAST while open
//   - bounded retries with exponential backoff + jitter, honoring
//     Retry-After, method-aware (ambiguous writes are not retried)
//   - TTL cache for GETs plus singleflight deduplication of identical
//     concurrent reads
package metaclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrRateLimited is returned when a call is refused locally because the
// circuit breaker is open or usage crossed the hard block threshold.
var ErrRateLimited = errors.New("meta rate limit protection: call suppressed to protect quota")

type Config struct {
	CallsPerMinute float64       // sustained refill rate
	Burst          int           // bucket capacity
	MaxConcurrent  int           // in-flight cap across all tokens
	BackoffAtUsage float64       // % — insert pacing delay above this
	BlockAtUsage   float64       // % — refuse calls above this
	BaseCooldown   time.Duration // first breaker-open duration
	MaxCooldown    time.Duration // breaker cooldown ceiling
	MaxRetries     int           // extra attempts after the first
	CacheTTL       time.Duration // GET response lifetime
	RequestTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		CallsPerMinute: 60,
		Burst:          10,
		MaxConcurrent:  4,
		BackoffAtUsage: 70,
		BlockAtUsage:   90,
		BaseCooldown:   30 * time.Second,
		MaxCooldown:    15 * time.Minute,
		MaxRetries:     3,
		CacheTTL:       45 * time.Second,
		RequestTimeout: 15 * time.Second,
	}
}

// Throttle codes Meta returns inside error bodies.
var throttleCodes = map[float64]bool{
	4: true, 17: true, 32: true, 613: true,
}

// ===========================
// STATE
// ===========================

type keyState struct {
	tokens         float64
	lastRefill     time.Time
	usagePct       float64
	blockedUntil   time.Time
	consecThrottle int
}

type cacheEntry struct {
	data    map[string]any
	expires time.Time
}

type inflightCall struct {
	done chan struct{}
	data map[string]any
	err  error
}

type Client struct {
	cfg     Config
	http    *http.Client
	baseURL string

	mu       sync.Mutex
	keys     map[string]*keyState
	cache    map[string]cacheEntry
	inflight map[string]*inflightCall
	slots    chan struct{}

	// observability counters
	totalCalls    uint64
	throttledHits uint64
	retries       uint64
	cacheHits     uint64
	localBlocks   uint64
	breakerOpens  uint64
}

var (
	defaultMu sync.RWMutex
	def       *Client
)

// Default returns the process-wide client.
func Default() *Client {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if def == nil {
		def = New(DefaultConfig())
	}
	return def
}

// SetBaseURL overrides the Graph API base URL (used by tests).
func SetBaseURL(u string) {
	Default().mu.Lock()
	Default().baseURL = u
	Default().mu.Unlock()
}

// BaseURL reports the current base URL.
func BaseURL() string {
	c := Default()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseURL
}

// New builds a client with its own state (tests).
func New(cfg Config) *Client {
	return &Client{
		cfg:      cfg,
		http:     &http.Client{Timeout: cfg.RequestTimeout},
		baseURL:  "https://graph.facebook.com/v23.0",
		keys:     map[string]*keyState{},
		cache:    map[string]cacheEntry{},
		inflight: map[string]*inflightCall{},
		slots:    make(chan struct{}, cfg.MaxConcurrent),
	}
}

// ===========================
// PUBLIC API
// ===========================

// Request executes a Graph API call through every protection layer and
// returns the decoded JSON body plus HTTP status.
func Request(
	method, path, accessToken string,
	query map[string][]string,
	body []byte,
) (map[string]any, int, error) {
	return Default().request(method, path, accessToken, query, body)
}

func (c *Client) request(
	method, path, accessToken string,
	query map[string][]string,
	body []byte,
) (map[string]any, int, error) {

	// Callers may pass an absolute URL or a path; join relative ones so a
	// stray missing slash can't glue onto the version segment.
	full := path
	if strings.HasPrefix(path, "http") {
		// Absolute URL (OAuth endpoints, dashboard handlers): use as-is.
	} else if !strings.HasPrefix(path, "/") {
		full = c.baseURL + "/" + path
	} else {
		full = c.baseURL + path
	}

	isGET := method == http.MethodGet

	cacheKey := fmt.Sprintf("%s|%s|%s", method, full, flattenQuery(query))

	// ---- read cache + singleflight (GET only)
	if isGET {
		if data, ok := c.cacheGet(cacheKey); ok {
			atomicAdd(&c.cacheHits, 1)
			return data, http.StatusOK, nil
		}

		joined, wasInflight := c.joinInflight(cacheKey)
		if wasInflight {
			<-joined.done
			atomicAdd(&c.cacheHits, 1)
			return joined.data, statusFromErr(joined.err), joined.err
		}

		// Leader executes, publishes, then releases waiters.
		data, status, err := c.withRetries(method, full, accessToken, query, body, cacheKey)

		c.completeInflight(cacheKey, data, err)
		c.finishInflight(cacheKey)

		return data, status, err
	}

	return c.withRetries(method, full, accessToken, query, body, "")
}

// flushCacheOnWrite drops cached GETs so reads never see stale data.
func (c *Client) withRetries(
	method, full, accessToken string,
	query map[string][]string,
	body []byte,
	cacheKey string,
) (map[string]any, int, error) {

	isGET := method == http.MethodGet

	var lastData map[string]any
	var lastStatus int
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {

		data, status, err := c.attempt(method, full, accessToken, query, body)

		lastData, lastStatus, lastErr = data, status, err

		if err == nil && status >= 200 && status < 300 {
			if isGET && cacheKey != "" {
				c.cachePut(cacheKey, data)
			}
			// Any successful write invalidates cached reads.
			if method != http.MethodGet {
				c.flushCache()
			}
			return data, status, nil
		}

		retryable := false
		switch {
		case errors.Is(err, context.Canceled):
			return data, status, err
		case status == http.StatusTooManyRequests:
			retryable = true
		case status == 400 || status == 401 || status == 403:
			retryable = isThrottleBody(data) // codes 4/17/32/613 arrive as 400s
		case status >= 500:
			retryable = isGET || method == http.MethodDelete
		case err != nil:
			retryable = isGET || method == http.MethodDelete
		}

		if !retryable || attempt == c.cfg.MaxRetries {
			break
		}

		atomicAdd(&c.retries, 1)
		sleepBackoff(attempt + 1)
	}

	if lastStatus == 0 && lastErr == nil {
		lastErr = ErrRateLimited
	} else if isThrottleStatus(lastStatus) || isThrottleBody(lastData) {
		lastErr = fmt.Errorf("%w: %s", ErrRateLimited, errText(lastData))
	}

	return lastData, lastStatus, lastErr
}

// attempt performs one gated HTTP round-trip.
func (c *Client) attempt(
	method, full, accessToken string,
	query map[string][]string,
	body []byte,
) (map[string]any, int, error) {

	key := tokenKey(accessToken)

	// Concurrency slot first so pauses below don't hold idle slots.
	select {
	case c.slots <- struct{}{}:
	default:
		// wait politely for a slot
		timer := time.NewTimer(10 * time.Second)
		select {
		case c.slots <- struct{}{}:
			timer.Stop()
		case <-timer.C:
			return nil, 0, fmt.Errorf("timed out waiting for a free request slot")
		}
	}
	defer func() { <-c.slots }()

	// Gate: circuit breaker + usage block + token bucket.
	if err := c.gate(key); err != nil {
		atomicAdd(&c.localBlocks, 1)
		return nil, 0, err
	}

	// `full` is already final here (absolute URL or joined base+path).
	url := full
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&" // absolute URLs often carry their own query string
		}
		url += sep + encodeQuery(query)
	}

	req, err := http.NewRequestWithContext(
		context.Background(), method, url, readerFromBody(body),
	)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accessToken == "" {
		req.Header.Del("Authorization") // e.g. OAuth token-exchange endpoints
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	atomicAdd(&c.totalCalls, 1)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	// Learn from rate-limit headers on EVERY response.
	c.trackUsage(key, resp.Header)

	var data map[string]any
	if len(raw) > 0 {
		json.Unmarshal(raw, &data) // best-effort; error bodies still parsed
	}

	if isThrottleStatus(resp.StatusCode) || isThrottleBody(data) {
		atomicAdd(&c.throttledHits, 1)
		c.registerThrottle(key, resp.Header, data)
	} else if resp.StatusCode == http.StatusOK {
		c.registerSuccess(key)
	}

	return data, resp.StatusCode, nil
}

// ===========================
// GATE (breaker + usage + limiter)
// ===========================

func (c *Client) gate(key string) error {

	c.mu.Lock()

	st := c.stateLocked(key)
	now := time.Now()

	// Circuit open → fail fast. Aggressive protection means refusing
	// locally instead of burning quota we may not have.
	if now.Before(st.blockedUntil) {
		wait := st.blockedUntil.Sub(now)
		usage := st.usagePct
		c.mu.Unlock()
		return fmt.Errorf("%w: circuit open for %s (usage %.0f%%)",
			ErrRateLimited, wait.Round(time.Second), usage)
	}

	// Usage crossed the hard block line → self-impose a cooldown now
	// instead of waiting for Meta to punish us.
	if st.usagePct >= c.cfg.BlockAtUsage {
		st.blockedUntil = now.Add(c.cfg.BaseCooldown)
		st.consecThrottle++
		usage := st.usagePct
		c.mu.Unlock()
		atomicAdd(&c.breakerOpens, 1)
		return fmt.Errorf("%w: usage %.0f%% ≥ %.0f%% — pausing %s",
			ErrRateLimited, usage, c.cfg.BlockAtUsage, c.cfg.BaseCooldown)
	}

	pacingDelay := time.Duration(0)
	if st.usagePct >= c.cfg.BackoffAtUsage {
		// Slow lane: proportional spacing above the soft threshold.
		over := (st.usagePct - c.cfg.BackoffAtUsage) / (100 - c.cfg.BackoffAtUsage)
		pacingDelay = time.Duration(over*float64(time.Second)) + 200*time.Millisecond
	}

	// Token bucket refill.
	elapsed := now.Sub(st.lastRefill).Seconds()
	st.tokens += elapsed * (c.cfg.CallsPerMinute / 60.0)
	if st.tokens > float64(c.cfg.Burst) {
		st.tokens = float64(c.cfg.Burst)
	}
	st.lastRefill = now

	if pacingDelay == 0 && st.tokens < 1 {
		pacingDelay = time.Duration((1 - st.tokens) / (c.cfg.CallsPerMinute / 60.0) * float64(time.Second))
	}
	c.mu.Unlock()

	time.Sleep(pacingDelay)
	return nil
}

func (c *Client) stateLocked(key string) *keyState {

	st, ok := c.keys[key]
	if !ok {
		st = &keyState{tokens: float64(c.cfg.Burst), lastRefill: time.Now()}
		c.keys[key] = st
	}

	return st
}

// registerThrottle trips the breaker with exponential cooldown.
func (c *Client) registerThrottle(
	key string,
	header http.Header,
	data map[string]any,
) {

	c.mu.Lock()
	defer c.mu.Unlock()

	st := c.stateLocked(key)
	st.consecThrottle++

	cooldown := c.cfg.BaseCooldown << uint(minInt(st.consecThrottle-1, 5))
	if cooldown > c.cfg.MaxCooldown {
		cooldown = c.cfg.MaxCooldown
	}

	// Honor Retry-After when Meta provides one.
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			honored := time.Duration(secs) * time.Second
			if honored < cooldown {
				cooldown = honored
			}
		}
	}

	st.blockedUntil = time.Now().Add(cooldown)
	atomicAdd(&c.breakerOpens, 1)

	if data != nil {
		_ = data // message already surfaced by caller via body
	}
}

func (c *Client) registerSuccess(key string) {

	c.mu.Lock()
	defer c.mu.Unlock()

	st := c.stateLocked(key)
	st.consecThrottle = 0
}

// trackUsage parses Meta's usage headers and records the worst number.
func (c *Client) trackUsage(key string, header http.Header) {

	worst := 0.0

	if raw := header.Get("x-app-usage"); raw != "" {
		worst = maxFloat(worst, parseAppUsage(raw))
	}

	if raw := header.Get("x-business-use-case-usage"); raw != "" {
		worst = maxFloat(worst, parseBUCUsage(raw))
	}

	if worst <= 0 {
		return
	}

	c.mu.Lock()
	st := c.stateLocked(key)
	if worst > st.usagePct {
		st.usagePct = worst
	}
	// Usage decays slowly over time in reality; nudge stored value down
	// on good responses so stale highs don't linger forever.
	st.usagePct = math.Max(st.usagePct-2, 0)
	c.mu.Unlock()
}

func parseAppUsage(raw string) float64 {

	var parsed struct {
		CallCount    float64 `json:"call_count"`
		TotalTime    float64 `json:"total_time"`
		TotalCPUTime float64 `json:"total_cputime"`
	}

	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return 0
	}

	return maxFloat(parsed.CallCount,
		maxFloat(parsed.TotalTime, parsed.TotalCPUTime))
}

func parseBUCUsage(raw string) float64 {

	// Shape: {"act_123":[{"call_count":20,"total_time":30,...}], ...}
	var parsed map[string][]struct {
		CallCount    float64 `json:"call_count"`
		TotalTime    float64 `json:"total_time"`
		TotalCPUTime float64 `json:"total_cputime"`
	}

	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return 0
	}

	worst := 0.0
	for _, entries := range parsed {
		for _, e := range entries {
			worst = maxFloat(worst,
				maxFloat(e.CallCount, maxFloat(e.TotalTime, e.TotalCPUTime)))
		}
	}

	return worst
}

// ===========================
// CACHE + SINGLEFLIGHT
// ===========================

func (c *Client) cacheGet(key string) (map[string]any, bool) {

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expires) {
		delete(c.cache, key)
		return nil, false
	}

	return entry.data, true
}

func (c *Client) cachePut(key string, data map[string]any) {

	c.mu.Lock()
	defer c.mu.Unlock()

	// Hard cap so long agent runs can't balloon memory.
	if len(c.cache) >= 500 {
		for k := range c.cache { // drop arbitrary entry
			delete(c.cache, k)
			break
		}
	}

	c.cache[key] = cacheEntry{data: data, expires: time.Now().Add(c.cfg.CacheTTL)}
}

// flushCache drops all cached GETs after any successful write.
func (c *Client) flushCache() {
	c.mu.Lock()
	c.cache = map[string]cacheEntry{}
	c.mu.Unlock()
}

func (c *Client) joinInflight(key string) (*inflightCall, bool) {

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.inflight[key]; ok {
		return existing, true
	}

	call := &inflightCall{done: make(chan struct{})}
	c.inflight[key] = call

	return call, false
}

func (c *Client) finishInflight(key string) {

	c.mu.Lock()
	call, ok := c.inflight[key]
	delete(c.inflight, key)
	c.mu.Unlock()

	if !ok {
		return
	}
	close(call.done)
}

// completeInflight stores the result for anyone who joined this call.
func (c *Client) completeInflight(key string, data map[string]any, err error) {

	c.mu.Lock()
	call, ok := c.inflight[key]
	c.mu.Unlock()

	if !ok {
		return
	}

	call.data = data
	call.err = err
}

// ===========================
// STATUS SNAPSHOT
// ===========================

type Status struct {
	TotalCalls    uint64        `json:"total_calls"`
	ThrottledHits uint64        `json:"throttled_hits"`
	Retries       uint64        `json:"retries"`
	CacheHits     uint64        `json:"cache_hits"`
	LocalBlocks   uint64        `json:"local_blocks"`
	BreakerOpens  uint64        `json:"breaker_opens"`
	OpenCircuits  []OpenCircuit `json:"open_circuits,omitempty"`
}

type OpenCircuit struct {
	Key          string  `json:"key"`
	UsagePct     float64 `json:"usage_pct"`
	BlockedForMs int64   `json:"blocked_for_ms"`
}

// StatusSnapshot summarizes protection state for observability.
func StatusSnapshot() Status {
	c := Default()
	return c.statusSnapshot()
}

func (c *Client) statusSnapshot() Status {

	c.mu.Lock()
	defer c.mu.Unlock()

	out := Status{
		TotalCalls:    atomicLoad(&c.totalCalls),
		ThrottledHits: atomicLoad(&c.throttledHits),
		Retries:       atomicLoad(&c.retries),
		CacheHits:     atomicLoad(&c.cacheHits),
		LocalBlocks:   atomicLoad(&c.localBlocks),
		BreakerOpens:  atomicLoad(&c.breakerOpens),
	}

	now := time.Now()

	for key, st := range c.keys {
		if now.Before(st.blockedUntil) {
			out.OpenCircuits = append(out.OpenCircuits, OpenCircuit{
				Key:          shortKey(key),
				UsagePct:     st.usagePct,
				BlockedForMs: st.blockedUntil.Sub(now).Milliseconds(),
			})
		}
	}

	return out
}

func shortKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}

// ===========================
// HELPERS
// ===========================

func tokenKey(accessToken string) string {
	if accessToken == "" {
		return "anonymous"
	}
	if len(accessToken) > 12 {
		return accessToken[:12]
	}
	return accessToken
}

func flattenQuery(query map[string][]string) string {

	if len(query) == 0 {
		return ""
	}

	keys := make([]string, 0, len(query))

	parts := []string{}
	for k, vals := range query {
		keys = append(keys, k)
		_ = keys
		for _, v := range vals {
			parts = append(parts, k+"="+v)
		}
	}

	sortStrings(parts)
	return strings.Join(parts, "&")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func encodeQuery(query map[string][]string) string {

	parts := []string{}
	for k, vals := range query {
		for _, v := range vals {
			parts = append(parts, k+"="+v)
		}
	}

	sortStrings(parts)
	return strings.Join(parts, "&")
}

func readerFromBody(body []byte) io.Reader {
	if body == nil {
		return nil
	}
	return strings.NewReader(string(body))
}

func isThrottleStatus(status int) bool {
	return status == http.StatusTooManyRequests
}

func isThrottleBody(data map[string]any) bool {

	errObj, ok := data["error"].(map[string]any)
	if !ok {
		return false
	}

	code, ok := errObj["code"].(float64)
	if !ok {
		return false
	}

	return throttleCodes[code]
}

func errText(data map[string]any) string {

	errObj, ok := data["error"].(map[string]any)
	if !ok {
		return "throttled"
	}

	if m, ok := errObj["message"].(string); ok {
		return m
	}

	return "throttled"
}

func sleepBackoff(attempt int) {

	base := time.Duration(250*(1<<uint(attempt-1))) * time.Millisecond
	jitter := time.Duration(rand.Int63n(int64(base / 4)))

	time.Sleep(base + jitter)
}

func statusFromErr(err error) int {
	return 0
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func atomicAdd(v *uint64, delta uint64) { atomic.AddUint64(v, delta) }
func atomicLoad(v *uint64) uint64       { return atomic.LoadUint64(v) }
