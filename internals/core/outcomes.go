package core

import (
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
)

// Outcome windows measured from the day AFTER execution.
var outcomeWindows = []int{1, 3, 7}

// StoredOutcome is one captured post-execution snapshot plus verdict.
type StoredOutcome struct {
	ID         int64                    `json:"id"`
	ActionID   int64                    `json:"action_id"`
	WindowDays int                      `json:"window_days"`
	Metrics    json.RawMessage          `json:"metrics,omitempty"`
	Evaluation *optimization.Evaluation `json:"evaluation,omitempty"`
	CapturedAt string                   `json:"captured_at"`
}

const outcomeColumns = `
	id, action_id, window_days,
	COALESCE(metrics, ''), COALESCE(evaluation, ''),
	captured_at
`

func scanOutcome(row interface{ Scan(...any) error }) (*StoredOutcome, error) {

	var (
		o          StoredOutcome
		metrics    []byte
		evaluation []byte
	)

	if err := row.Scan(
		&o.ID, &o.ActionID, &o.WindowDays,
		&metrics, &evaluation, &o.CapturedAt,
	); err != nil {
		return nil, err
	}

	if len(metrics) > 0 {
		o.Metrics = json.RawMessage(metrics)
	}
	if len(evaluation) > 0 {
		ev := &optimization.Evaluation{}
		if json.Unmarshal(evaluation, ev) == nil {
			o.Evaluation = ev
		}
	}

	return &o, nil
}

// StartOutcomeCollector runs CaptureDueOutcomes periodically so 1d/3d/7d
// windows fill as they mature. Call once at boot.
func (a *Api) StartOutcomeCollector(interval time.Duration) {

	go func() {
		time.Sleep(10 * time.Second)

		log.Println("[outcomes] collector pass starting")

		n := a.CaptureDueOutcomes()

		log.Printf("[outcomes] collector pass captured %d snapshots", n)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			a.CaptureDueOutcomes()
		}
	}()
}

type dueAction struct {
	id            int64
	userID        int64
	objectType    string
	objectID      string
	action        string
	executedAt    time.Time
	goalID        sql.NullInt64
	beforeMetrics []byte
}

// CaptureDueOutcomes snapshots metrics for every matured post-execution
// window and re-evaluates the owning proposal. Returns snapshot count.
func (a *Api) CaptureDueOutcomes() int {

	now := time.Now().UTC()
	total := 0

	for _, window := range outcomeWindows {

		rows, err := a.db.Query(`
			SELECT oa.id, oa.user_id, oa.object_type, oa.object_id,
			       oa.action,
			       CAST(oa.executed_at AS TEXT) AS executed_at,
			       oa.goal_id, oa.before_metrics
			FROM optimization_actions oa
			WHERE oa.status = 'EXECUTED'
			  AND oa.before_metrics IS NOT NULL
			  AND oa.executed_at IS NOT NULL
			  AND julianday(?) - julianday(oa.executed_at) >= ?
			  AND NOT EXISTS (
			      SELECT 1 FROM optimization_outcomes o
			      WHERE o.action_id = oa.id AND o.window_days = ?
			  )
		`, now.Format("2006-01-02"), window+1, window)
		if err != nil {
			log.Printf("[outcomes] due-query failed (window %d): %v", window, err)
			continue
		}

		due := []dueAction{}

		for rows.Next() {

			var d dueAction
			var rawExecuted string

			if err := rows.Scan(
				&d.id, &d.userID, &d.objectType, &d.objectID,
				&d.action, &rawExecuted, &d.goalID,
				&d.beforeMetrics,
			); err != nil {
				log.Printf("[outcomes] scan failed (window %d): %v", window, err)
				continue
			}

			executedAt, perr := time.Parse("2006-01-02 15:04:05", rawExecuted)
			if perr != nil {
				continue
			}
			d.executedAt = executedAt.UTC()

			due = append(due, d)
		}
		rows.Close()

		log.Printf("[outcomes] window %dd: %d due", window, len(due))

		for _, d := range due {
			if a.captureWindow(d, window) {
				total++
			}
		}
	}

	return total
}

// captureWindow fetches one post-action window, stores it and updates
// the proposal's running verdict.
func (a *Api) captureWindow(d dueAction, window int) bool {

	since := d.executedAt.AddDate(0, 0, 1).Format("2006-01-02")
	until := d.executedAt.AddDate(0, 0, window).Format("2006-01-02")

	metrics, err := analytics.FetchMetricsRange(
		a.metaTokenFor(d.userID),
		d.objectID,
		scopeForObjectType(d.objectType),
		since, until,
	)
	if err != nil {
		log.Printf("[outcomes] capture failed for action %d (%s %s): %v",
			d.id, d.action, d.objectID, err)
		return false
	}

	// Evaluate against the pre-action snapshot and the linked goal.
	var verdict optimization.Evaluation
	verdict.Result = optimization.VerdictInsufficientData

	if len(d.beforeMetrics) > 0 {

		var before analytics.Metrics
		if json.Unmarshal(d.beforeMetrics, &before) == nil {

			goal := a.goalSnapshotFor(d.goalID)

			verdict = optimization.EvaluateAction(
				d.action, before, metrics, goal,
			)
		}
	}

	evalRaw, _ := json.Marshal(verdict)
	metricsRaw, _ := json.Marshal(metrics)

	res, err := a.db.Exec(`
		INSERT OR IGNORE INTO optimization_outcomes
			(action_id, user_id, window_days, metrics, evaluation)
		VALUES (?, ?, ?, ?, ?)
	`, d.id, d.userID, window, string(metricsRaw), string(evalRaw))
	if err != nil {
		log.Printf("[outcomes] insert failed (action %d): %v", d.id, err)
		return false
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false // already captured by a concurrent pass
	}

	a.db.Exec(`
		UPDATE optimization_actions SET outcome_status = ? WHERE id = ?
	`, string(verdict.Result), d.id)

	return true
}

// goalSnapshotFor loads the neutral goal view for evaluation context.
func (a *Api) goalSnapshotFor(goalID sql.NullInt64) *analytics.GoalSnapshot {

	if !goalID.Valid || goalID.Int64 <= 0 {
		return nil
	}

	goal, err := scanGoal(a.db.QueryRow(`
		SELECT `+goalColumns+` FROM marketing_goals WHERE id = ?
	`, goalID.Int64))
	if err != nil {
		return nil
	}

	return goalSnapshot(goal)
}

// ===========================
// HISTORY + PERFORMANCE API
// ===========================

// actionExplanation loads a proposal with its outcomes and captured
// before-snapshot — the full audit trail behind a decision.
func (a *Api) actionExplanation(userID, actionID int64) (map[string]any, error) {

	action, err := scanStoredAction(a.db.QueryRow(`
		SELECT `+storedActionColumns+` FROM optimization_actions
		WHERE id = ? AND user_id = ?
	`, actionID, userID))

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	outcomes := []StoredOutcome{}

	rows, err := a.db.Query(`
		SELECT `+outcomeColumns+` FROM optimization_outcomes
		WHERE action_id = ? ORDER BY window_days ASC
	`, actionID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			o, oerr := scanOutcome(rows)
			if oerr == nil {
				outcomes = append(outcomes, *o)
			}
		}
	}

	return map[string]any{
		"action":         action,
		"outcomes":       outcomes,
		"before_metrics": action.BeforeMetrics,
		"explanation": map[string]any{
			"reason":     action.Reason,
			"rule":       action.RuleID,
			"confidence": action.Confidence,
			"signals":    action.Signals,
		},
	}, nil
}

// GetOptimizationAction returns one proposal with every captured outcome.
func (a *Api) GetOptimizationAction(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "actionID"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid action id", http.StatusBadRequest)
		return
	}

	explanation, err := a.actionExplanation(user.ID, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if explanation == nil {
		http.Error(w, "action not found", http.StatusNotFound)
		return
	}

	a.writeJSONValue(w, explanation)
}

// ListOptimizationOutcomes returns recent captured windows joined with
// their proposal summary — the "did it work?" history feed.
func (a *Api) ListOptimizationOutcomes(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	type outcomeRow struct {
		StoredOutcome
		ObjectName   string `json:"object_name,omitempty"`
		ObjectType   string `json:"object_type"`
		ObjectID     string `json:"object_id"`
		Action       string `json:"action"`
		ActionStatus string `json:"action_status"`
	}

	rows, err := a.db.Query(`
		SELECT o.id, o.action_id, o.window_days,
		       COALESCE(o.metrics, ''), COALESCE(o.evaluation, ''),
		       o.captured_at,
		       COALESCE(oa.object_name, ''), oa.object_type, oa.object_id,
		       oa.action, oa.status
		FROM optimization_outcomes o
		JOIN optimization_actions oa ON oa.id = o.action_id
		WHERE o.user_id = ?
		ORDER BY o.captured_at DESC, o.window_days DESC
		LIMIT ?
	`, user.ID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	outcomes := []outcomeRow{}

	for rows.Next() {

		var (
			row        outcomeRow
			metrics    []byte
			evaluation []byte
		)

		if err := rows.Scan(
			&row.ID, &row.ActionID, &row.WindowDays,
			&metrics, &evaluation, &row.CapturedAt,
			&row.ObjectName, &row.ObjectType, &row.ObjectID,
			&row.Action, &row.ActionStatus,
		); err != nil {
			continue
		}

		if len(metrics) > 0 {
			row.Metrics = json.RawMessage(metrics)
		}
		if len(evaluation) > 0 {
			ev := &optimization.Evaluation{}
			if json.Unmarshal(evaluation, ev) == nil {
				row.Evaluation = ev
			}
		}

		outcomes = append(outcomes, row)
	}

	a.writeJSONValue(w, map[string]any{"outcomes": outcomes})
}

// SpendTotals answers "did our AI actually save/make money?" with
// daily-rate-normalized spend across every evaluated proposal.
type SpendTotals struct {
	DailySpendBefore     float64 `json:"daily_spend_before"`
	DailySpendAfter      float64 `json:"daily_spend_after"`
	EstimatedDailyImpact float64 `json:"estimated_daily_impact"` // after - before (negative = cheaper)
	EstimatedDailySaving float64 `json:"estimated_daily_saving"` // before - after (positive = saved)
}

// spendTotals joins each evaluated proposal's BEFORE snapshot with its
// latest AFTER window. Raw sums would compare a ~30d pre-window against
// a ≤7d post-window, so everything is normalized to daily rates.
func (a *Api) spendTotals(userID int64) SpendTotals {

	var t SpendTotals

	rows, err := a.db.Query(`
		SELECT oa.action, COALESCE(oa.date_preset, ''), o.window_days,
		       COALESCE(oa.before_metrics, ''), COALESCE(o.metrics, '')
		FROM optimization_outcomes o
		JOIN optimization_actions oa ON oa.id = o.action_id
		WHERE o.user_id = ? AND oa.status = 'EXECUTED'
		  AND o.window_days = (
		      SELECT MAX(window_days) FROM optimization_outcomes x
		      WHERE x.action_id = o.action_id
		  )
	`, userID)
	if err != nil {
		return t
	}
	defer rows.Close()

	for rows.Next() {

		var (
			action, datePreset  string
			windowDays          int
			beforeRaw, afterRaw []byte
		)

		if err := rows.Scan(&action, &datePreset, &windowDays,
			&beforeRaw, &afterRaw); err != nil {
			continue
		}

		var before, after analytics.Metrics
		if json.Unmarshal(beforeRaw, &before) != nil ||
			json.Unmarshal(afterRaw, &after) != nil {
			continue
		}
		if before.Spend <= 0 || windowDays <= 0 {
			continue
		}

		days := presetDaysInt(datePreset)
		if days <= 0 {
			days = 30
		}

		dailyBefore := before.Spend / float64(days)
		dailyAfter := after.Spend / float64(windowDays)

		t.DailySpendBefore += dailyBefore
		t.DailySpendAfter += dailyAfter
	}

	t.EstimatedDailyImpact = roundMoney(t.DailySpendAfter - t.DailySpendBefore)
	t.EstimatedDailySaving = roundMoney(t.DailySpendBefore - t.DailySpendAfter)
	t.DailySpendBefore = roundMoney(t.DailySpendBefore)
	t.DailySpendAfter = roundMoney(t.DailySpendAfter)

	return t
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

// OptimizationPerformance aggregates success rates per action type —
// "was my optimization actually good?"
func (a *Api) OptimizationPerformance(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Latest window per executed proposal.
	rows, err := a.db.Query(`
		SELECT oa.action, o.evaluation
		FROM optimization_outcomes o
		JOIN optimization_actions oa ON oa.id = o.action_id
		WHERE o.user_id = ? AND oa.status = 'EXECUTED'
		  AND o.window_days = (
		      SELECT MAX(window_days) FROM optimization_outcomes x
		      WHERE x.action_id = o.action_id
		  )
	`, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type bucket struct {
		Total       int     `json:"total"`
		Success     int     `json:"success"`
		Failure     int     `json:"failure"`
		Neutral     int     `json:"neutral"`
		NoData      int     `json:"insufficient_data"`
		SuccessRate float64 `json:"success_rate"`
	}

	overall := bucket{}
	byAction := map[string]*bucket{}
	evaluated := 0

	for rows.Next() {

		var (
			action     string
			evaluation []byte
		)

		if err := rows.Scan(&action, &evaluation); err != nil {
			continue
		}
		if len(evaluation) == 0 {
			continue
		}

		ev := &optimization.Evaluation{}
		if json.Unmarshal(evaluation, ev) != nil {
			continue
		}

		b := byAction[action]
		if b == nil {
			b = &bucket{}
			byAction[action] = b
		}

		count := func(bb *bucket) {
			bb.Total++
			switch ev.Result {
			case optimization.VerdictSuccess:
				bb.Success++
			case optimization.VerdictFailure:
				bb.Failure++
			case optimization.VerdictNeutral:
				bb.Neutral++
			default:
				bb.NoData++
			}
		}

		count(&overall)
		count(b)
		evaluated++
	}

	rate := func(b *bucket) float64 {
		denom := b.Success + b.Failure
		if denom == 0 {
			return 0
		}
		v := float64(b.Success) / float64(denom) * 100
		return float64(int(v*10)) / 10
	}

	overall.SuccessRate = rate(&overall)
	for _, b := range byAction {
		b.SuccessRate = rate(b)
	}

	a.writeJSONValue(w, map[string]any{
		"evaluated_actions": evaluated,
		"overall":           overall,
		"by_action":         byAction,
		"spend_totals":      a.spendTotals(user.ID),
	})
}
