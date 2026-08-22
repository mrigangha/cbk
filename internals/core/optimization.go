package core

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
)

// StoredAction is a persisted recommendation awaiting human approval.
type StoredAction struct {
	ID          int64           `json:"id"`
	ObjectType  string          `json:"object_type"`
	ObjectID    string          `json:"object_id"`
	ObjectName  string          `json:"object_name,omitempty"`
	Action      string          `json:"action"`
	Reason      string          `json:"reason"`
	RuleID      string          `json:"rule_id,omitempty"`
	Confidence  *float64        `json:"confidence,omitempty"`
	SuggestedChange json.RawMessage `json:"suggested_change,omitempty"`
	Status      string          `json:"status"`
	DatePreset  string          `json:"date_preset,omitempty"`

	CreatedAt string `json:"created_at"`
	DecidedAt string `json:"decided_at,omitempty"`
	ExecutedAt string `json:"executed_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

const storedActionColumns = `
	id, object_type, object_id, COALESCE(object_name, ''),
	action, reason, COALESCE(rule_id, ''), confidence,
	COALESCE(suggested_change, ''), status, COALESCE(date_preset, ''),
	created_at, COALESCE(decided_at, ''), COALESCE(executed_at, ''),
	COALESCE(error, '')
`

func scanStoredAction(row interface{ Scan(...any) error }) (*StoredAction, error) {

	var a StoredAction

	// suggested_change is TEXT in SQLite; scan into bytes then cast.
	var suggested []byte

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

	return &a, nil
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

	stored := 0
	skipped := 0

	for _, d := range plan.Recommendations {

		if d.RecommendedAction == optimization.ActionNoAction {
			continue // don't persist non-actions
		}

		changeJSON, _ := json.Marshal(d.SuggestedChange)

		// Skip when an identical PENDING proposal already exists.
		var existing int64
		err := a.db.QueryRow(`
			SELECT id FROM optimization_actions
			WHERE user_id = ? AND object_id = ? AND action = ? AND status = 'PENDING'
		`, user.ID, d.CampaignID, d.RecommendedAction).Scan(&existing)

		if err == nil {
			skipped++
			continue
		}
		if err != sql.ErrNoRows {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var confidence any
		if d.Confidence > 0 {
			confidence = d.Confidence
		}

		res, err := a.db.Exec(`
			INSERT INTO optimization_actions (
				user_id, object_type, object_id, object_name,
				action, reason, rule_id, confidence, suggested_change,
				date_preset
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			user.ID,
			string(d.ObjectType),
			d.CampaignID,
			d.CampaignName,
			d.RecommendedAction,
			d.Reason,
			d.RuleID,
			confidence,
			string(changeJSON),
			actx.datePreset,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = res

		stored++
	}

	a.writeJSONValue(w, map[string]any{
		"plan":              plan,
		"stored_proposals":  stored,
		"duplicate_skipped": skipped,
		"note":              "Proposals are PENDING. Nothing executes until approved.",
	})
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
