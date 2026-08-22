package core

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mrigangha/cbk/internals/analytics"
)

// goalSnapshot maps a stored marketing goal into the neutral shape the
// analytics engine understands.
func goalSnapshot(g *MarketingGoal) *analytics.GoalSnapshot {
	if g == nil {
		return nil
	}
	return &analytics.GoalSnapshot{
		Name:              g.Name,
		Objective:         g.Objective,
		TargetCPL:         g.TargetCPL,
		TargetCPA:         g.TargetCPA,
		TargetROAS:        g.TargetROAS,
		TargetConversions: floatPtr(g.TargetConversions),
		Budget:            g.Budget,
		DailyBudget:       g.DailyBudget,
	}
}

func floatPtr(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

const defaultDatePreset = "last_30d"

// analyticsCtx resolves the authenticated user's Meta credentials and
// the optional query parameters shared by every analytics endpoint.
type analyticsCtx struct {
	token      string
	accountID  string
	datePreset string
	goal       *analytics.GoalSnapshot
}

func (a *Api) resolveAnalyticsCtx(
	w http.ResponseWriter,
	r *http.Request,
) (*analyticsCtx, bool) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}

	var token, accountID string

	requestedAccount := r.URL.Query().Get("ad_account_id")

	query := `SELECT access_token, ad_account_id FROM meta_ads_accounts WHERE user_id = ?`
	args := []any{user.ID}

	if requestedAccount != "" {
		query += ` AND ad_account_id = ?`
		args = append(args, requestedAccount)
	}
	query += ` LIMIT 1`

	err := a.db.QueryRow(query, args...).Scan(&token, &accountID)
	if err != nil {
		http.Error(w, "no connected Meta Ads account", http.StatusBadRequest)
		return nil, false
	}

	preset := r.URL.Query().Get("date_preset")
	if preset == "" {
		preset = defaultDatePreset
	}

	ctx := &analyticsCtx{
		token:      token,
		accountID:  accountID,
		datePreset: preset,
	}

	// Optional goal for progress calculations.
	if rawGoalID := r.URL.Query().Get("goal_id"); rawGoalID != "" {
		goalID, err := strconv.ParseInt(rawGoalID, 10, 64)
		if err == nil && goalID > 0 {
			goal, err := a.getOwnedGoal(r, goalID)
			if err == nil && goal != nil {
				ctx.goal = goalSnapshot(goal)
			}
		}
	} else if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
		// no-op placeholder; goals come explicitly or via agent runtime
		_ = sessionID
	}

	return ctx, true
}

func (a *Api) writeJSONValue(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (a *Api) AnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	report, err := analytics.BuildReport(
		actx.token, actx.accountID, "", analytics.ScopeAccount,
		actx.datePreset, actx.goal, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.writeJSONValue(w, report)
}

func (a *Api) AnalyticsCampaigns(w http.ResponseWriter, r *http.Request) {
	a.analyticsList(w, r, analytics.ScopeCampaign)
}

func (a *Api) AnalyticsAdSets(w http.ResponseWriter, r *http.Request) {
	a.analyticsList(w, r, analytics.ScopeAdSet)
}

func (a *Api) AnalyticsAds(w http.ResponseWriter, r *http.Request) {
	a.analyticsList(w, r, analytics.ScopeAd)
}

func (a *Api) analyticsList(
	w http.ResponseWriter,
	r *http.Request,
	scope analytics.Scope,
) {
	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	kind := "campaigns"
	switch scope {
	case analytics.ScopeAdSet:
		kind = "adsets"
	case analytics.ScopeAd:
		kind = "ads"
	}

	objects, err := analytics.ListObjects(actx.token, actx.accountID, kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Optional parent filter.
	parent := ""
	switch scope {
	case analytics.ScopeAdSet:
		parent = r.URL.Query().Get("campaign_id")
	case analytics.ScopeAd:
		parent = r.URL.Query().Get("ad_set_id")
	}

	filtered := make([]map[string]any, 0, len(objects))
	for _, obj := range objects {
		switch scope {
		case analytics.ScopeAdSet:
			if cid, _ := obj["campaign_id"].(string); parent == "" || cid == parent {
				filtered = append(filtered, obj)
			}
		case analytics.ScopeAd:
			if aid, _ := obj["adset_id"].(string); parent == "" || aid == parent {
				filtered = append(filtered, obj)
			}
		default:
			filtered = append(filtered, obj)
		}
	}

	analyses, _, err := analytics.AnalyzeSet(
		actx.token, scope, filtered, actx.datePreset, actx.goal, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	report := map[string]any{
		"scope":       string(scope),
		"date_preset": actx.datePreset,
		"count":       len(analyses),
		"objects":     analyses,
	}

	if actx.goal != nil {
		totals := analytics.Metrics{}
		rows := make([]analytics.Metrics, 0, len(analyses))
		for _, item := range analyses {
			rows = append(rows, item.Metrics)
		}
		totals = analytics.Sum(rows)

		days := presetDaysInt(actx.datePreset)
		gp := analytics.AssessGoalProgress(totals, actx.goal, days)
		report["goal_progress"] = gp
	}

	a.writeJSONValue(w, report)
}

func (a *Api) AnalyticsTrends(w http.ResponseWriter, r *http.Request) {
	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	scope := analytics.ScopeAccount
	objectID := actx.accountID

	if id := r.URL.Query().Get("campaign_id"); id != "" {
		scope, objectID = analytics.ScopeCampaign, id
	} else if id := r.URL.Query().Get("adset_id"); id != "" {
		scope, objectID = analytics.ScopeAdSet, id
	} else if id := r.URL.Query().Get("ad_id"); id != "" {
		scope, objectID = analytics.ScopeAd, id
	}

	days, err := analytics.FetchDaily(actx.token, objectID, scope, actx.datePreset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.writeJSONValue(w, map[string]any{
		"object_id":        objectID,
		"scope":            string(scope),
		"date_preset":      actx.datePreset,
		"days":             days,
		"trend_directions": analytics.TrendDirections(days),
	})
}

func (a *Api) AnalyticsAnomalies(w http.ResponseWriter, r *http.Request) {
	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	days, err := analytics.FetchDaily(
		actx.token, actx.accountID, analytics.ScopeAccount, actx.datePreset,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.writeJSONValue(w, map[string]any{
		"date_preset": actx.datePreset,
		"days_in_win": len(days),
		"anomalies":   analytics.DetectAnomalies(days),
	})
}

func (a *Api) AnalyticsCompare(w http.ResponseWriter, r *http.Request) {
	actx, ok := a.resolveAnalyticsCtx(w, r)
	if !ok {
		return
	}

	rawIDs := strings.Split(r.URL.Query().Get("ids"), ",")
	ids := make([]string, 0, len(rawIDs))

	for _, id := range rawIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}

	if len(ids) < 2 || len(ids) > 10 {
		http.Error(w, "provide between 2 and 10 ids to compare", http.StatusBadRequest)
		return
	}

	scope := analytics.ScopeCampaign
	switch strings.ToLower(r.URL.Query().Get("scope")) {
	case "adset", "adsets":
		scope = analytics.ScopeAdSet
	case "ad", "ads":
		scope = analytics.ScopeAd
	}

	comparison, err := analytics.CompareObjects(
		actx.token, actx.accountID, ids, scope, actx.datePreset, actx.goal,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.writeJSONValue(w, comparison)
}

func presetDaysInt(preset string) int {
	switch preset {
	case "today", "yesterday":
		return 1
	case "last_7d":
		return 7
	case "last_14d":
		return 14
	default:
		return 30
	}
}
