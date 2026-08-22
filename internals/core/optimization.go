package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/metaclient"
	"github.com/mrigangha/cbk/internals/optimization"
)

// StoredAction is a persisted recommendation awaiting human approval.
type StoredAction struct {
	ID              int64           `json:"id"`
	ObjectType      string          `json:"object_type"`
	ObjectID        string          `json:"object_id"`
	ObjectName      string          `json:"object_name,omitempty"`
	Action          string          `json:"action"`
	Reason          string          `json:"reason"`
	RuleID          string          `json:"rule_id,omitempty"`
	Confidence      *float64        `json:"confidence,omitempty"`
	SuggestedChange json.RawMessage `json:"suggested_change,omitempty"`
	Status          string          `json:"status"`
	DatePreset      string          `json:"date_preset,omitempty"`

	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	GoalID         *int64          `json:"goal_id,omitempty"`
	BeforeMetrics  json.RawMessage `json:"before_metrics,omitempty"`
	OutcomeStatus  string          `json:"outcome_status,omitempty"`
	Signals        []string        `json:"signals,omitempty"`

	CreatedAt  string `json:"created_at"`
	DecidedAt  string `json:"decided_at,omitempty"`
	ExecutedAt string `json:"executed_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

const storedActionColumns = `
	id, object_type, object_id, COALESCE(object_name, ''),
	action, reason, COALESCE(rule_id, ''), confidence,
	COALESCE(suggested_change, ''), status, COALESCE(date_preset, ''),
	COALESCE(idempotency_key, ''), goal_id,
	COALESCE(before_metrics, ''), COALESCE(outcome_status, ''),
	COALESCE(signals, ''),
	created_at, COALESCE(decided_at, ''), COALESCE(executed_at, ''),
	COALESCE(error, '')
`

func scanStoredAction(row interface{ Scan(...any) error }) (*StoredAction, error) {

	var a StoredAction

	// suggested_change/before_metrics are TEXT in SQLite; scan into
	// bytes then cast.
	var suggested, before, signals []byte
	var goalID sql.NullInt64

	err := row.Scan(
		&a.ID,
		&a.ObjectType,
		&a.ObjectID,
		&a.ObjectName,
		&a.Action,
		&a.Reason,
		&a.RuleID,
		&a.Confidence,
		&suggested,
		&a.Status,
		&a.DatePreset,
		&a.IdempotencyKey,
		&goalID,
		&before,
		&a.OutcomeStatus,
		&signals,
		&a.CreatedAt,
		&a.DecidedAt,
		&a.ExecutedAt,
		&a.Error,
	)
	if err != nil {
		return nil, err
	}

	if len(suggested) > 0 {
		a.SuggestedChange = json.RawMessage(suggested)
	}
	if len(before) > 0 {
		a.BeforeMetrics = json.RawMessage(before)
	}
	if len(signals) > 0 {
		json.Unmarshal(signals, &a.Signals)
	}
	if goalID.Valid {
		v := goalID.Int64
		a.GoalID = &v
	}

	return &a, nil
}

// IdempotencyKeyFor builds the replay-protection key for a proposal:
// object + action + concrete target + decision window.
func IdempotencyKeyFor(
	objectID, action, datePreset string,
	suggestedChange json.RawMessage,
) string {

	var s struct {
		DailyBudgetPct *float64 `json:"daily_budget_pct"`
		Status         string   `json:"status"`
	}
	if len(suggestedChange) > 0 {
		json.Unmarshal(suggestedChange, &s)
	}

	target := "none"
	switch {
	case s.DailyBudgetPct != nil:
		target = fmt.Sprintf("pct%+.0f", *s.DailyBudgetPct)
	case s.Status != "":
		target = strings.ToLower(s.Status)
	}

	return fmt.Sprintf("%s|%s|%s|%s", objectID, action, target, datePreset)
}

// GenerateOptimizationActions runs the decision engine over the account
// and stores every recommendation as a PENDING proposal.
// Nothing is executed — proposals await explicit human approval.
func (a *Api) GenerateOptimizationActions(w http.ResponseWriter, r *http.Request) {

	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	user := a.GetUser(r)

	level := "campaigns"
	if l := r.URL.Query().Get("level"); l == "adsets" {
		level = "adsets"
	}

	scope := analytics.ScopeCampaign
	if level == "adsets" {
		scope = analytics.ScopeAdSet
	}

	objects, err := analytics.ListObjects(actx.token, actx.accountID, level)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	analyses, totals, err := analytics.AnalyzeSet(
		actx.token, scope, objects, actx.datePreset, actx.goal, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	report := &analytics.Report{
		Scope:      scope,
		DatePreset: actx.datePreset,
		DaysInWin:  presetDaysInt(actx.datePreset),
		Totals:     totals,
		Objects:    analyses,
	}

	plan := optimization.OptimizeReport(report, actx.goal)

	stored, skipped, err := a.persistProposals(
		user.ID, actx.token, actx.accountID,
		actx.datePreset, level, report, actx.goal, actx.goalID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.writeJSONValue(w, map[string]any{
		"plan":              plan,
		"stored_proposals":  stored,
		"duplicate_skipped": skipped,
		"note":              "Proposals are PENDING. Nothing executes until approved.",
	})
}

// persistProposals stores every non-NO_ACTION decision as a PENDING
// proposal, skipping duplicates. Shared by the REST endpoint and the
// optimize_campaign agent tool.
func (a *Api) persistProposals(
	userID int64,
	token, accountID, datePreset, level string,
	report *analytics.Report,
	goal *analytics.GoalSnapshot,
	goalID *int64,
) (int, int, error) {

	plan := optimization.OptimizeReport(report, goal)

	stored := 0
	skipped := 0

	for _, d := range plan.Recommendations {

		if d.RecommendedAction == optimization.ActionNoAction {
			continue
		}

		changeJSON, _ := json.Marshal(d.SuggestedChange)

		idemKey := IdempotencyKeyFor(
			d.CampaignID,
			d.RecommendedAction,
			datePreset,
			changeJSON,
		)

		var existing int64
		err := a.db.QueryRow(`
			SELECT id FROM optimization_actions
			WHERE user_id = ? AND object_id = ? AND action = ? AND status = 'PENDING'
		`, userID, d.CampaignID, d.RecommendedAction).Scan(&existing)

		if err == nil {
			skipped++
			continue
		}
		if err != sql.ErrNoRows {
			return 0, 0, err
		}

		var confidence any
		if d.Confidence > 0 {
			confidence = d.Confidence
		}

		signalsJSON, _ := json.Marshal(d.Signals)

		if _, err := a.db.Exec(`
			INSERT INTO optimization_actions (
				user_id, object_type, object_id, object_name,
				action, reason, rule_id, confidence, suggested_change,
				date_preset, idempotency_key, goal_id, signals
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			userID,
			string(d.ObjectType),
			d.CampaignID,
			d.CampaignName,
			d.RecommendedAction,
			d.Reason,
			d.RuleID,
			confidence,
			string(changeJSON),
			datePreset,
			idemKey,
			goalID,
			string(signalsJSON),
		); err != nil {
			return 0, 0, err
		}

		stored++
	}

	return stored, skipped, nil
}

func (a *Api) ListOptimizationActions(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	status := r.URL.Query().Get("status")

	query := `SELECT ` + storedActionColumns + `
		FROM optimization_actions WHERE user_id = ?`
	args := []any{user.ID}

	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY created_at DESC, id DESC LIMIT 200`

	rows, err := a.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	actions := []StoredAction{}

	for rows.Next() {
		action, err := scanStoredAction(rows)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		actions = append(actions, *action)
	}

	a.writeJSONValue(w, map[string]any{"actions": actions})
}

func (a *Api) decideOptimizationAction(
	w http.ResponseWriter,
	r *http.Request,
	newStatus string,
) {

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

	res, err := a.db.Exec(`
		UPDATE optimization_actions
		SET status = ?, decided_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ? AND status = 'PENDING'
	`, newStatus, id, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "pending action not found", http.StatusNotFound)
		return
	}

	action, err := scanStoredAction(a.db.QueryRow(
		`SELECT `+storedActionColumns+` FROM optimization_actions WHERE id = ?`,
		id,
	))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.writeJSONValue(w, action)
}

func (a *Api) ApproveOptimizationAction(w http.ResponseWriter, r *http.Request) {
	a.decideOptimizationAction(w, r, optimization.StatusApproved)
}

func (a *Api) RejectOptimizationAction(w http.ResponseWriter, r *http.Request) {
	a.decideOptimizationAction(w, r, optimization.StatusRejected)
}

// MetaRateLimitStatus exposes the shared client's protection state so
// the dashboard/agent can see when calls are being suppressed.
func (a *Api) MetaRateLimitStatus(w http.ResponseWriter, r *http.Request) {
	a.writeJSONValue(w, metaclient.StatusSnapshot())
}

// SimulateOptimizations runs the decision engine in pure dry-run mode:
// nothing is stored, nothing executes. The response carries estimated
// daily budget impact per recommendation so users can judge a batch
// before sending anything to approval.
func (a *Api) SimulateOptimizations(w http.ResponseWriter, r *http.Request) {

	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	level := "campaigns"
	if l := r.URL.Query().Get("level"); l == "adsets" {
		level = "adsets"
	}

	scope := analytics.ScopeCampaign
	if level == "adsets" {
		scope = analytics.ScopeAdSet
	}

	// Account-wide dry run: list every child object, analyze each,
	// then let the decision engine score the set. Never fetch insights
	// for an empty object id — Meta reads that as node "insights".
	objects, err := analytics.ListObjects(actx.token, actx.accountID, level)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	analyses, totals, err := analytics.AnalyzeSet(
		actx.token, scope, objects, actx.datePreset, actx.goal, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	report := &analytics.Report{
		Scope:      scope,
		DatePreset: actx.datePreset,
		DaysInWin:  presetDaysInt(actx.datePreset),
		Totals:     totals,
		Objects:    analyses,
	}

	plan := optimization.OptimizeReport(report, actx.goal)

	days := presetDaysInt(actx.datePreset)
	if days <= 0 {
		days = 30
	}

	spendByID := map[string]float64{}
	for _, obj := range report.Objects {
		spendByID[obj.ID] = obj.Metrics.Spend
	}

	type simulated struct {
		optimization.Decision
		EstimatedDailyImpact float64 `json:"estimated_daily_impact,omitempty"`
		Risk                 string  `json:"risk"`
	}

	out := make([]simulated, 0, len(plan.Recommendations))

	for _, d := range plan.Recommendations {

		item := simulated{Decision: d}

		switch {
		case d.RequiresConfirmation && d.Confidence >= 0.85:
			item.Risk = "HIGH"
		case d.RequiresConfirmation:
			item.Risk = "MEDIUM"
		default:
			item.Risk = "LOW"
		}

		if d.SuggestedChange != nil && d.SuggestedChange.DailyBudgetPct != nil {

			dailySpend := spendByID[d.CampaignID] / float64(days)
			item.EstimatedDailyImpact =
				math.Round(dailySpend**d.SuggestedChange.DailyBudgetPct/100*100) / 100
		}

		out = append(out, item)
	}

	totalImpact := 0.0
	for _, item := range out {
		totalImpact += item.EstimatedDailyImpact
	}
	totalImpact = math.Round(totalImpact*100) / 100

	a.writeJSONValue(w, map[string]any{
		"dry_run":                      true,
		"goal":                         plan.GoalName,
		"date_preset":                  actx.datePreset,
		"recommendations":              out,
		"summary":                      plan.Summary,
		"estimated_daily_impact_total": totalImpact,
	})
}
