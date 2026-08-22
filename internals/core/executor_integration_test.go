package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/metaclient"
	"github.com/mrigangha/cbk/internals/tools"

	_ "modernc.org/sqlite"
)

// TestMain builds a throwaway database with the REAL migrations once,
// so executor tests exercise production schema.
func TestMain(m *testing.M) {

	os.Setenv("JWT_SECRET", "exec-test-secret")

	dir, err := os.MkdirTemp("", "cbk-exec-tests")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(dir, "exec.db")

	cmd := exec.Command("go", "run", "../../migrations", dbPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrations failed:\n%s\n", out)
		os.Exit(1)
	}

	execTestDBPath = dbPath

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

var execTestDBPath string

// newExecTestApi opens the migrated test DB and seeds one user with a
// connected Meta account. Returns the Api and that user's ID.
func newExecTestApi(t *testing.T) (*Api, int64) {

	t.Helper()

	db, err := sql.Open("sqlite", sqliteDSN(execTestDBPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	a := &Api{db: db}

	res, err := db.Exec(`
		INSERT INTO users (username, email, hashed_password)
		VALUES (?, ?, 'x')
	`, fmt.Sprintf("exec-%d", time.Now().UnixNano()),
		fmt.Sprintf("exec-%d@test.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()

	db.Exec(`
		INSERT INTO meta_ads_accounts
			(user_id, ad_account_id, account_name, access_token)
		VALUES (?, '777', 'Test Account', 'mock-token')
	`, uid)

	return a, uid
}

// metaMock is a configurable fake Graph API for executor flows.
type metaMock struct {
	mu             sync.Mutex
	campaignStatus string // returned by GET node
	dailyBudget    string
	improvements   bool  // when true insights report better CPA/ROAS
	failMutations  bool  // when true POSTs fail
	mutations      int32 // successful POST count
}

func (mm *metaMock) server(t *testing.T) *httptest.Server {

	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {

			w.Header().Set("Content-Type", "application/json")

			switch {

			case r.Method == http.MethodPost:
				atomic.AddInt32(&mm.mutations, 1)

				mm.mu.Lock()
				fail := mm.failMutations
				mm.mu.Unlock()

				if fail {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]any{
						"error": map[string]any{
							"code":    500,
							"message": "internal mock failure",
						},
					})
					return
				}

				json.NewEncoder(w).Encode(map[string]any{"success": true})

			case strings.HasSuffix(r.URL.Path, "/insights"):
				metrics := mm.currentInsights()
				json.NewEncoder(w).Encode(map[string]any{"data": []any{metrics}})

			default: // GET single node
				mm.mu.Lock()
				status := mm.campaignStatus
				budget := mm.dailyBudget
				mm.mu.Unlock()

				json.NewEncoder(w).Encode(map[string]any{
					"id":               strings.TrimPrefix(r.URL.Path, "/"),
					"status":           status,
					"effective_status": status,
					"daily_budget":     budget,
				})
			}
		},
	))

	t.Cleanup(ts.Close)
	return ts
}

func (mm *metaMock) currentInsights() map[string]any {

	mm.mu.Lock()
	defer mm.mu.Unlock()

	if mm.improvements {
		// Lower spend, same-ish conversions → CPA improved.
		return map[string]any{
			"spend": "700", "impressions": "30000", "reach": "12000",
			"clicks": "260",
			"actions": []any{
				map[string]any{"action_type": "offsite_conversion.fb_pixel_purchase", "value": "18"},
			},
			"purchase_roas": []any{
				map[string]any{"action_type": "purchase", "roas": "3.1"},
			},
		}
	}

	return map[string]any{
		"spend": "2000", "impressions": "40000", "reach": "15000",
		"clicks": "300",
		"actions": []any{
			map[string]any{"action_type": "offsite_conversion.fb_pixel_purchase", "value": "16"},
		},
		"purchase_roas": []any{
			map[string]any{"action_type": "purchase", "roas": "2.2"},
		},
	}
}

func wireMock(t *testing.T, mm *metaMock) {
	old := metaclient.BaseURL()
	metaclient.SetBaseURL(mm.server(t).URL)
	analytics.SetGraphBaseURL(metaclient.BaseURL())
	tools.SetGraphBaseURL(metaclient.BaseURL())
	t.Cleanup(func() {
		metaclient.SetBaseURL(old)
		analytics.SetGraphBaseURL(old)
		tools.SetGraphBaseURL(old)
	})
}

func seedApprovedAction(
	t *testing.T,
	a *Api,
	userID int64,
	objectID, action string,
	suggested string,
	idemKey string,
) int64 {

	t.Helper()

	res, err := a.db.Exec(`
		INSERT INTO optimization_actions (
			user_id, object_type, object_id, object_name,
			action, reason, rule_id, confidence, suggested_change,
			status, date_preset, idempotency_key, goal_id
		) VALUES (?, 'CAMPAIGN', ?, 'Mock Campaign', ?,
			'test reason', 'unit', 0.9, ?, 'APPROVED', 'last_30d', ?, NULL)
	`, userID, objectID, action, suggested, idemKey)
	if err != nil {
		t.Fatal(err)
	}

	id, _ := res.LastInsertId()
	return id
}

func execRouter(a *Api) http.Handler {

	r := chi.NewRouter()
	r.With(AuthMiddleware).Post("/optimization/actions/{actionID}/execute", a.ExecuteOptimizationAction)
	r.Get("/optimization/performance", a.OptimizationPerformance)
	return r
}

// authenticated request builder against the router.
func authedReq(
	t *testing.T,
	a *Api,
	method, path string,
) *http.Request {

	t.Helper()

	req := httptest.NewRequest(method, path, nil)

	// Real JWT so AuthMiddleware passes; identity resolves via context.
	var email string
	a.db.QueryRow(`SELECT email FROM users ORDER BY id DESC LIMIT 1`).Scan(&email)

	req.Header.Set("Authorization", "Bearer "+NewJWT(email))

	ctx := context.WithValue(req.Context(), UserContextKey,
		map[string]any{"email": email})

	return req.WithContext(ctx)
}

// ===========================
// TESTS
// ===========================

const testObjectID = "111"

func decreaseSuggested() string {
	return `{"daily_budget_pct":-20}`
}

// 1. Idempotent replay: executing two proposals with the same key must
// mutate Meta exactly once.
func TestExecute_IdempotentReplay(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	key := testObjectID + "|DECREASE_BUDGET|pct-20|last_30d"

	id1 := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(), key)
	id2 := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(), key)

	router := execRouter(a)

	for _, id := range []int64{id1, id2} {
		req := authedReq(t, a, http.MethodPost,
			fmt.Sprintf("/optimization/actions/%d/execute", id))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Fatalf("execute %d returned %d: %s", id, rec.Code, rec.Body.String())
		}
	}

	if got := atomic.LoadInt32(&mm.mutations); got != 1 {
		t.Fatalf("Meta mutations = %d, want exactly 1 (replay suppressed)", got)
	}

	var status1, status2 string
	a.db.QueryRow(`SELECT status FROM optimization_actions WHERE id=?`, id1).Scan(&status1)
	a.db.QueryRow(`SELECT status FROM optimization_actions WHERE id=?`, id2).Scan(&status2)

	if status1 != "EXECUTED" {
		t.Errorf("first action status = %s", status1)
	}
	// Second proposal stays APPROVED: its effect already happened via
	// the first one, so replay returns without re-running it.
	if status2 != "APPROVED" {
		t.Errorf("second action status = %s, want APPROVED (replayed)", status2)
	}
}

// 2. before_metrics snapshot is captured at execution time.
func TestExecute_CapturesBeforeMetrics(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	id := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(),
		testObjectID+"|DECREASE_BUDGET|pct-20|last_30d")

	router := execRouter(a)
	req := authedReq(t, a, http.MethodPost,
		fmt.Sprintf("/optimization/actions/%d/execute", id))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("execute failed: %d %s", rec.Code, rec.Body.String())
	}

	var raw []byte
	err := a.db.QueryRow(
		`SELECT before_metrics FROM optimization_actions WHERE id=?`, id,
	).Scan(&raw)
	if err != nil || len(raw) == 0 {
		t.Fatalf("before_metrics missing: %v %q", err, string(raw))
	}

	var metrics map[string]any
	if json.Unmarshal(raw, &metrics) != nil || metrics["spend"] == nil {
		t.Fatalf("before_metrics not valid metrics JSON: %s", string(raw))
	}
}

// 3. Full loop: executor → outcome windows → performance aggregation.
func TestExecute_OutcomesAndPerformance(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	id := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(),
		testObjectID+"|DECREASE_BUDGET|pct-20|last_30d")

	router := execRouter(a)
	req := authedReq(t, a, http.MethodPost,
		fmt.Sprintf("/optimization/actions/%d/execute", id))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("execute failed: %d %s", rec.Code, rec.Body.String())
	}

	// Backdate so all outcome windows are mature.
	a.db.Exec(`
		UPDATE optimization_actions
		SET executed_at = datetime('now', '-10 days')
		WHERE id = ?
	`, id)

	// The post-action world improved.
	mm.mu.Lock()
	mm.improvements = true
	mm.mu.Unlock()

	captured := a.CaptureDueOutcomes()
	if captured < 3 {
		t.Fatalf("captured %d outcome windows, want >= 3", captured)
	}

	// Performance endpoint aggregates the verdicts.
	preq := authedReq(t, a, http.MethodGet, "/optimization/performance")
	pres := httptest.NewRecorder()
	router.ServeHTTP(pres, preq)

	var payload struct {
		Evaluated int `json:"evaluated_actions"`
		Overall   struct {
			Success     int     `json:"success"`
			SuccessRate float64 `json:"success_rate"`
		} `json:"overall"`
		SpendTotals struct {
			DailySpendBefore     float64 `json:"daily_spend_before"`
			DailySpendAfter      float64 `json:"daily_spend_after"`
			EstimatedDailyImpact float64 `json:"estimated_daily_impact"`
		} `json:"spend_totals"`
	}
	if err := json.Unmarshal(pres.Body.Bytes(), &payload); err != nil {
		t.Fatalf("performance body: %s", pres.Body.String())
	}

	if payload.Evaluated < 1 || payload.Overall.Success < 1 ||
		payload.Overall.SuccessRate < 100 {
		t.Errorf("unexpected performance: %+v", payload)
	}

	// Money view: daily-rate totals must be present for evaluated actions.
	if payload.SpendTotals.DailySpendBefore <= 0 ||
		payload.SpendTotals.DailySpendAfter <= 0 ||
		payload.SpendTotals.EstimatedDailyImpact == 0 {
		t.Errorf("spend_totals not computed: %+v", payload.SpendTotals)
	}
}

// 4. A failing Meta mutation records FAILED and never fabricates a
// SUCCESS outcome.
func TestExecute_FailedMutation_NoFalseOutcome(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	id := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(),
		testObjectID+"|DECREASE_BUDGET|pct-20|last_30d")

	mm.mu.Lock()
	mm.failMutations = true
	mm.mu.Unlock()

	router := execRouter(a)
	req := authedReq(t, a, http.MethodPost,
		fmt.Sprintf("/optimization/actions/%d/execute", id))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected graceful failure response, got %d", rec.Code)
	}

	var resp struct {
		Executed bool `json:"executed"`
		Result   struct {
			Success bool `json:"success"`
		} `json:"result"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.Executed || resp.Result.Success {
		t.Fatal("failed mutation must not report success")
	}

	var status, outcomeStatus sql.NullString
	a.db.QueryRow(`
		SELECT status, COALESCE(outcome_status,'')
		FROM optimization_actions WHERE id=?
	`, id).Scan(&status, &outcomeStatus)

	if status.String != "FAILED" {
		t.Errorf("status = %s, want FAILED", status.String)
	}
	if outcomeStatus.String == "SUCCESS" {
		t.Error("outcome_status must not be SUCCESS after failed mutation")
	}

	// Outcomes only evaluate EXECUTED rows — none may exist here.
	a.db.Exec(`UPDATE optimization_actions SET executed_at =
		datetime('now','-10 days') WHERE id=?`, id)

	if n := a.CaptureDueOutcomes(); n != 0 {
		t.Errorf("captured %d outcomes for FAILED action, want 0", n)
	}
}

// 7. Decision → Outcome attribution: after a successful execution and
// matured outcome windows, /optimization/effectiveness must produce
// real money-weighted numbers (success rate, CPA/ROAS improvement).
func TestEffectiveness_EndToEndAttribution(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	id := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(),
		testObjectID+"|DECREASE_BUDGET|pct-20|last_30d")

	router := execRouter(a)
	req := authedReq(t, a, http.MethodPost,
		fmt.Sprintf("/optimization/actions/%d/execute", id))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("execute failed: %d %s", rec.Code, rec.Body.String())
	}

	a.db.Exec(`UPDATE optimization_actions
		SET executed_at = datetime('now', '-10 days') WHERE id = ?`, id)

	mm.mu.Lock()
	mm.improvements = true // post-action world: CPA ~39 vs ~125 before
	mm.mu.Unlock()

	if n := a.CaptureDueOutcomes(); n < 3 {
		t.Fatalf("captured %d windows, want >= 3", n)
	}

	effRouter := chi.NewRouter()
	effRouter.With(AuthMiddleware).Get("/optimization/effectiveness",
		a.GetOptimizationEffectiveness)

	ereq := authedReq(t, a, http.MethodGet,
		"/optimization/effectiveness?days=30")
	eres := httptest.NewRecorder()
	effRouter.ServeHTTP(eres, ereq)
	if eres.Code != 200 {
		t.Fatalf("effectiveness returned %d: %s",
			eres.Code, eres.Body.String())
	}

	var eff struct {
		WindowDays       int      `json:"window_days"`
		Evaluated        int      `json:"actions_evaluated"`
		Success          int      `json:"verdict_success"`
		SuccessRate      float64  `json:"success_rate"`
		CPAImprovement   *float64 `json:"cpa_improvement_pct"`
		ROASImprovement  *float64 `json:"roas_improvement_pct"`
		Score            *float64 `json:"score"`
		ConversionsDelta float64  `json:"conversions_delta"`
	}
	if err := json.Unmarshal(eres.Body.Bytes(), &eff); err != nil {
		t.Fatalf("bad body: %s", eres.Body.String())
	}

	if eff.WindowDays != 30 || eff.Evaluated != 1 ||
		eff.Success != 1 || eff.SuccessRate != 100 {
		t.Fatalf("unexpected core counts: %+v", eff)
	}

	// Before: spend 2000 / 16 conv = CPA 125. After: 700 / 18 = 38.9.
	// Improvement ≈ -68.9%.
	if eff.CPAImprovement == nil || *eff.CPAImprovement > -50 {
		t.Errorf("cpa_improvement_pct = %v, want ≈ -69", eff.CPAImprovement)
	}

	// ROAS 2.2 → 3.1 ≈ +40.9%.
	if eff.ROASImprovement == nil || *eff.ROASImprovement < 20 {
		t.Errorf("roas_improvement_pct = %v, want ≈ +41", eff.ROASImprovement)
	}

	// Conversions rose by 2 in the measured windows.
	if eff.ConversionsDelta != 2 {
		t.Errorf("conversions_delta = %v, want 2", eff.ConversionsDelta)
	}

	if eff.Score == nil || *eff.Score <= 0 || *eff.Score > 100 {
		t.Errorf("score = %v, want within (0,100]",
			eff.Score)
	} else {
		t.Logf("effectiveness: cpa=%+.1f%% roas=%+.1f%% score=%.1f",
			*eff.CPAImprovement, *eff.ROASImprovement, *eff.Score)
	}
}

// 5. Cooldown stops the second distinct proposal on the same object
// inside the 24h window.
func TestExecute_CooldownBlocksSecondProposal(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	id1 := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", decreaseSuggested(),
		testObjectID+"|DECREASE_BUDGET|pct-20|last_30d")
	id2 := seedApprovedAction(t, a, uid, testObjectID,
		"DECREASE_BUDGET", `{"daily_budget_pct":-10}`,
		testObjectID+"|DECREASE_BUDGET|pct-10|last_7d")

	router := execRouter(a)

	for _, id := range []int64{id1, id2} {
		req := authedReq(t, a, http.MethodPost,
			fmt.Sprintf("/optimization/actions/%d/execute", id))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
	}

	var mutations int64
	mm.mu.Lock()
	mutations = int64(atomic.LoadInt32(&mm.mutations))
	mm.mu.Unlock()

	if mutations > 1 {
		t.Fatalf("cooldown bypassed: %d mutations landed", mutations)
	}

	var status2 string
	a.db.QueryRow(`SELECT status FROM optimization_actions WHERE id=?`, id2).
		Scan(&status2)

	if status2 != "FAILED" {
		t.Errorf("second proposal status = %s, want FAILED (cooldown)", status2)
	}

	var errMsg string
	a.db.QueryRow(`SELECT COALESCE(error,'') FROM optimization_actions WHERE id=?`, id2).
		Scan(&errMsg)
	if !strings.Contains(errMsg, "cooldown") {
		t.Errorf("error = %q, want cooldown message", errMsg)
	}
}

// executeConcurrently fires n execute requests at once (true race) and
// returns each recorder.
func executeConcurrently(
	t *testing.T,
	a *Api,
	router http.Handler,
	ids []int64,
) []*httptest.ResponseRecorder {

	t.Helper()

	recs := make([]*httptest.ResponseRecorder, len(ids))
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			req := authedReq(t, a, http.MethodPost,
				fmt.Sprintf("/optimization/actions/%d/execute", id))
			rec := httptest.NewRecorder()
			<-start // release all goroutines together
			router.ServeHTTP(rec, req)
			recs[i] = rec
		}(i, id)
	}

	close(start)
	wg.Wait()
	return recs
}

// countStatuses returns how many proposals ended in each status.
func countStatuses(a *Api, ids []int64) map[string]int {

	counts := map[string]int{}

	for _, id := range ids {
		var status string
		a.db.QueryRow(
			`SELECT status FROM optimization_actions WHERE id=?`, id,
		).Scan(&status)
		counts[status]++
	}

	return counts
}

// 6. N concurrent proposals sharing one idempotency key must mutate
// Meta exactly once — no double-spend through the approval gate.
func TestExecute_ConcurrentDuplicateKey_SingleMutation(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	key := testObjectID + "|DECREASE_BUDGET|pct-20|last_30d"

	ids := []int64{}
	for i := 0; i < 5; i++ {
		ids = append(ids, seedApprovedAction(t, a, uid, testObjectID,
			"DECREASE_BUDGET", decreaseSuggested(), key))
	}

	recs := executeConcurrently(t, a, execRouter(a), ids)

	for i, rec := range recs {
		if rec == nil || rec.Code != 200 {
			t.Fatalf("request %d failed: %v", i, rec)
		}
	}

	if got := atomic.LoadInt32(&mm.mutations); got != 1 {
		t.Fatalf("Meta mutations = %d, want exactly 1 under concurrency", got)
	}

	counts := countStatuses(a, ids)
	if counts["EXECUTED"] != 1 {
		t.Errorf("EXECUTED count = %d, want 1", counts["EXECUTED"])
	}
	if counts["APPROVED"] != 4 {
		t.Errorf("replayed count = %d, want 4 (losers stay APPROVED)", counts["APPROVED"])
	}
}

// 7. N distinct proposals on the SAME object fired simultaneously must
// not all squeeze past the cooldown read before any of them writes.
func TestExecute_ConcurrentCooldown_SingleMutation(t *testing.T) {

	a, uid := newExecTestApi(t)
	mm := &metaMock{campaignStatus: "ACTIVE", dailyBudget: "10000"}
	wireMock(t, mm)

	pcts := []string{"-10", "-20", "-25", "-30"}
	ids := []int64{}
	for _, p := range pcts {
		ids = append(ids, seedApprovedAction(t, a, uid, testObjectID,
			"DECREASE_BUDGET",
			fmt.Sprintf(`{"daily_budget_pct":%s}`, p),
			testObjectID+"|DECREASE_BUDGET|pct"+p+"|last_7d"))
	}

	executeConcurrently(t, a, execRouter(a), ids)

	if got := atomic.LoadInt32(&mm.mutations); got != 1 {
		t.Fatalf("cooldown bypassed concurrently: %d mutations landed, want 1", got)
	}

	counts := countStatuses(a, ids)
	if counts["EXECUTED"] != 1 {
		t.Errorf("EXECUTED count = %d, want 1", counts["EXECUTED"])
	}
	if counts["FAILED"] != 3 {
		t.Errorf("FAILED count = %d, want 3 (cooldown rejections)", counts["FAILED"])
	}
}
