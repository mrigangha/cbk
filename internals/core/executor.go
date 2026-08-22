package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
	"github.com/mrigangha/cbk/internals/tools"
)

// ExecuteOptimizationAction runs an APPROVED proposal against the Meta
// API, behind the guardrails. This is the only path from recommendation
// to real change, and it is always human-initiated.
func (a *Api) ExecuteOptimizationAction(w http.ResponseWriter, r *http.Request) {

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

	action, err := scanStoredAction(a.db.QueryRow(`
		SELECT `+storedActionColumns+` FROM optimization_actions
		WHERE id = ? AND user_id = ?
	`, id, user.ID))

	if err == sql.ErrNoRows {
		http.Error(w, "action not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if action.Status != optimization.StatusApproved {
		http.Error(w,
			"only APPROVED actions can be executed (current: "+action.Status+")",
			http.StatusConflict)
		return
	}

	result := a.runOptimizationAction(user.ID, a.metaToolContext(r), action)

	if result.Success {
		a.db.Exec(`
			UPDATE optimization_actions
			SET status = 'EXECUTED', executed_at = CURRENT_TIMESTAMP,
			    result = ?, error = ''
			WHERE id = ?
		`, mustJSON(result), id)
	} else {
		a.db.Exec(`
			UPDATE optimization_actions
			SET status = 'FAILED', executed_at = CURRENT_TIMESTAMP,
			    error = ?, result = ?
			WHERE id = ?
		`, result.Message, mustJSON(result), id)
	}

	a.writeJSONValue(w, map[string]any{
		"id":       id,
		"executed": result.Success,
		"result":   result,
	})
}

// ExecutionResult summarizes what happened (or why it didn't).
type ExecutionResult struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Message string `json:"message"`

	Before *float64       `json:"before,omitempty"`
	After  *float64       `json:"after,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// OptimizationOutcome is what gets persisted alongside the action.
type OptimizationOutcome struct {
	ExecutionResult
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// runOptimizationAction validates against the guardrails and dispatches
// to the existing Meta CRUD tools.
func (a *Api) runOptimizationAction(
	userID int64,
	toolCtx tools.ToolContext,
	action *StoredAction,
) ExecutionResult {

	res := ExecutionResult{Action: action.Action}

	g := DefaultGuardrails()
	now := time.Now().UTC()

	var suggested struct {
		DailyBudgetPct *float64 `json:"daily_budget_pct,omitempty"`
		Status         string   `json:"status,omitempty"`
	}
	if len(action.SuggestedChange) > 0 {
		json.Unmarshal(action.SuggestedChange, &suggested)
	}

	daysInWindow := presetDaysInt(action.DatePreset)

	// Conversions in window come from the analytics engine.
	conversions := 0.0
	if metrics, err := analytics.FetchMetrics(
		toolCtx.AccessToken, action.ObjectID,
		scopeForObjectType(action.ObjectType), action.DatePreset,
	); err == nil {
		conversions = metrics.Conversions
	}

	// Cooldown from the last EXECUTED action on this object.
	var lastExecuted sql.NullString
	a.db.QueryRow(`
		SELECT MAX(executed_at) FROM optimization_actions
		WHERE user_id = ? AND object_id = ? AND status = 'EXECUTED'
	`, userID, action.ObjectID).Scan(&lastExecuted)

	vc := ValidationContext{
		ObjectType:    action.ObjectType,
		Action:        action.Action,
		Conversions:   conversions,
		DaysInWindow:  daysInWindow,
		RequestedPct:  suggested.DailyBudgetPct,
		Now:           now,
	}

	if lastExecuted.Valid && lastExecuted.String != "" {
		if t, perr := time.Parse("2006-01-02 15:04:05", lastExecuted.String); perr == nil {
			vc.LastExecutedAt = &t
		}
	}

	// Live state from Meta — decide on reality, not cache.
	currentStatus, currentBudget, metaErr := fetchLiveState(
		toolCtx, action.ObjectType, action.ObjectID,
	)
	if metaErr != nil {
		res.Message = "failed to read current state from Meta: " + metaErr.Error()
		return res
	}
	vc.CurrentStatus = currentStatus

	pct, ok, reason := g.Validate(vc)
	if !ok {
		res.Message = reason
		return res
	}

	switch action.Action {

	case optimization.ActionPauseCampaign:
		out, err := tools.PauseCampaign(toolCtx, map[string]any{
			"campaign_id": action.ObjectID,
		})
		return finishExecution(out, err, "campaign paused")

	case optimization.ActionResumeCampaign:
		out, err := tools.ActivateCampaign(toolCtx, map[string]any{
			"campaign_id": action.ObjectID,
		})
		return finishExecution(out, err, "campaign resumed")

	case optimization.ActionDecreaseBudget, optimization.ActionIncreaseBudget:

		if currentBudget <= 0 {
			res.Message = "cannot compute budget change: no daily budget set on campaign"
			return res
		}

		before := currentBudget
		after := ApplyBudgetPct(float64(currentBudget), *pct)

		if after <= 0 {
			res.Message = "computed budget rounds to zero; refusing"
			return res
		}

		out, err := tools.UpdateCampaign(toolCtx, map[string]any{
			"campaign_id":  action.ObjectID,
			"daily_budget": strconv.FormatInt(after, 10),
		})

		r := finishExecution(out, err,
			fmt.Sprintf("daily budget %d → %d (%+.0f%%)",
				before, after, *pct))

		bf, af := float64(before), float64(after)
		r.Before, r.After = &bf, &af

		return r

	case optimization.ActionRefreshCreative:
		res.Message = "manual action required: create new creatives in Ads Manager"
		return res
	}

	res.Message = fmt.Sprintf("action %q is not executable", action.Action)
	return res
}

func finishExecution(out any, err error, message string) ExecutionResult {

	res := ExecutionResult{}

	if err != nil {
		res.Message = "Meta API rejected the change: " + err.Error()
		return res
	}

	res.Success = true
	res.Message = message

	if m, ok := out.(map[string]any); ok {
		res.Meta = m
	}

	return res
}

func scopeForObjectType(t string) analytics.Scope {
	switch t {
	case "ADSET":
		return analytics.ScopeAdSet
	case "AD":
		return analytics.ScopeAd
	default:
		return analytics.ScopeCampaign
	}
}

// metaToolContext builds the Meta tool context for the authenticated user.
func (a *Api) metaToolContext(r *http.Request) tools.ToolContext {

	ctx := tools.ToolContext{}

	user := a.GetUser(r)
	if user == nil {
		return ctx
	}

	a.db.QueryRow(`
		SELECT access_token, ad_account_id
		FROM meta_ads_accounts
		WHERE user_id = ?
		LIMIT 1
	`, user.ID).Scan(&ctx.AccessToken, &ctx.AdAccountID)

	return ctx
}

// fetchLiveState reads the object's current status and daily budget
// straight from Meta so decisions are made on reality, not cache.
func fetchLiveState(
	ctx tools.ToolContext,
	objectType, objectID string,
) (string, int64, error) {

	raw, err := tools.GetCampaign(ctx, map[string]any{"campaign_id": objectID})
	if err != nil {
		return "", 0, err
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return "", 0, fmt.Errorf("unexpected response shape from Meta")
	}

	status, _ := m["status"].(string)
	if status == "" {
		status, _ = m["effective_status"].(string)
	}

	var dailyBudget int64
	if b, ok := m["daily_budget"].(string); ok {
		dailyBudget, _ = strconv.ParseInt(b, 10, 64)
	}

	return status, dailyBudget, nil
}
