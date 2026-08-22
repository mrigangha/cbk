package metaclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(cfg Config, baseURL string) *Client {
	c := New(cfg)
	c.baseURL = baseURL
	return c
}

func TestCacheHitAvoidsSecondCall(t *testing.T) {

	var hits int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	c := testClient(DefaultConfig(), ts.URL)

	for i := 0; i < 3; i++ {
		data, status, err := c.request(
			http.MethodGet, "/act_1/campaigns", "tok-cache", nil, nil,
		)
		if err != nil || status != 200 || data["ok"] != true {
			t.Fatalf("call %d failed: %v %d", i, err, status)
		}
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("server hit %d times, want 1 (cache miss)", got)
	}
}

func TestRetryOnThrottleThenSuccess(t *testing.T) {

	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": 4, "message": "throttled"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "123"})
	}))
	defer ts.Close()

	cfg := DefaultConfig()
	cfg.BaseCooldown = 10 * time.Millisecond // fast test
	cfg.MaxCooldown = 50 * time.Millisecond
	c := testClient(cfg, ts.URL)
	_, status, err := c.request(
		http.MethodGet, "/x", "tok-retry", nil, nil,
	)

	if err != nil || status != 200 {
		t.Fatalf("expected eventual success, got %d %v", status, err)
	}
	if got := atomic.LoadInt32(&attempts); got < 3 {
		t.Errorf("attempts = %d, want >= 3", got)
	}
}

func TestBreakerOpensAndFailsFast(t *testing.T) {

	var hits int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": 17, "message": "user request limit reached"},
		})
	}))
	defer ts.Close()

	cfg := DefaultConfig()
	cfg.BaseCooldown = 60 * time.Second // keep open for the test window
	cfg.MaxRetries = 1
	c := testClient(cfg, ts.URL)
	// First call exhausts retries and trips the breaker.
	c.request(http.MethodGet, "/x", "tok-breaker", nil, nil)

	hitsAfterFirst := atomic.LoadInt32(&hits)

	// Subsequent calls must be refused locally — zero new server hits.
	for i := 0; i < 3; i++ {
		_, _, err := c.request(http.MethodGet, "/x", "tok-breaker", nil, nil)
		if err == nil {
			t.Fatal("expected local rate-limit refusal")
		}
	}

	if got := atomic.LoadInt32(&hits); got != hitsAfterFirst {
		t.Errorf("breaker did not fail fast: server hit %d → %d",
			hitsAfterFirst, got)
	}
}

func TestUsageHeaderTripsHardBlock(t *testing.T) {

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-business-use-case-usage",
			`{"act_999":[{"call_count":95,"total_time":40}]}`)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	cfg := DefaultConfig()
	cfg.BlockAtUsage = 90
	c := testClient(cfg, ts.URL)
	// First call succeeds but records 95% usage.
	if _, status, err := c.request(
		http.MethodGet, "/y", "tok-usage", nil, nil,
	); err != nil || status != 200 {
		t.Fatalf("priming call failed: %v %d", err, status)
	}

	// Different path (skips cache) but SAME token — the per-token
	// usage bucket must refuse this call locally.
	_, _, err := c.request(http.MethodGet, "/other", "tok-usage", nil, nil)

	if err == nil {
		t.Fatal("expected refusal after usage crossed block threshold")
	}
}

func TestWritesFlushCache(t *testing.T) {

	var hits int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer ts.Close()

	c := testClient(DefaultConfig(), ts.URL)
	c.request(http.MethodGet, "/z", "tok-flush", nil, nil) // cached
	c.request(http.MethodPost, "/z", "tok-flush", nil, []byte("{}"))
	c.request(http.MethodGet, "/z", "tok-flush", nil, nil) // cache was flushed

	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("server hits = %d, want 3 (write must invalidate cache)", got)
	}
}

func TestStatusSnapshot_Counters(t *testing.T) {

	c := testClient(DefaultConfig(), "https://example.invalid") // network errors

	c.request(http.MethodGet, "/q", "tok-status", nil, nil)

	snap := c.statusSnapshot()

	if snap.TotalCalls == 0 || snap.Retries == 0 {
		t.Errorf("counters not tracked: %+v", snap)
	}
}

// Regression: absolute URLs passed as `path` used to be prefixed with
// baseURL anyway, producing graph.facebook.com/v23.0https:/... and Meta
// error 100 subcode 33 ("Object with ID 'v23.0https:' does not exist").
func TestAbsoluteURLUsedVerbatim(t *testing.T) {

	var gotPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer ts.Close()

	c := testClient(DefaultConfig(), "https://graph.facebook.com/v23.0")

	absolute := ts.URL + "/me/adaccounts?fields=id,name"
	if _, status, err := c.request(
		http.MethodGet, absolute, "tok-abs", nil, nil,
	); err != nil || status != 200 {
		t.Fatalf("request failed: %v %d", err, status)
	}

	if !strings.HasPrefix(gotPath, "/me/adaccounts") {
		t.Fatalf("server saw %q — absolute URL was mangled", gotPath)
	}
}

// A relative path without a leading slash must not glue onto the
// version segment (v23.0act_1 → object ID "v23.0act_1" at Meta).
func TestRelativePathGetsSeparator(t *testing.T) {

	var gotPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	c := testClient(DefaultConfig(), ts.URL)

	if _, status, err := c.request(
		http.MethodGet, "act_123/campaigns", "tok-rel", nil, nil,
	); err != nil || status != 200 {
		t.Fatalf("request failed: %v %d", err, status)
	}

	want := "/act_123/campaigns"
	if gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

// Query maps must merge with an existing query string using &, not a
// second ?.
func TestQueryMapAppendsToExistingQueryString(t *testing.T) {

	var gotRaw string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	c := testClient(DefaultConfig(), ts.URL)

	_, status, err := c.request(
		http.MethodGet,
		ts.URL+"/me/adaccounts?fields=id,name",
		"tok-query",
		map[string][]string{"limit": {"25"}},
		nil,
	)
	if err != nil || status != 200 {
		t.Fatalf("request failed: %v %d", err, status)
	}

	if gotRaw != "fields=id,name&limit=25" {
		t.Fatalf("raw query = %q, want merged params", gotRaw)
	}
}
