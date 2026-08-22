package tools

import (
	"fmt"

	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
)

// RegisterAnalyticsTool adds the marketing-intelligence tool that turns
// raw insights into scores, health, fatigue, anomalies and goal progress.
// It is kept separate from the Meta CRUD tools because it depends on the
// analytics package.
func RegisterAnalyticsTool(h *ToolHandler) {

	h.RegisterTool(Tool{
		Name:        "get_analytics",
		Description: "Marketing intelligence over Meta Ads data. Returns normalized metrics (spend, CTR, CPC, CVR, CPL, CPA, ROAS, frequency), a performance score with reasons, delivery health, creative fatigue signals and goal progress when a goal is active. Use scope=account for an overview, or scope=campaign|adset|ad with object_id for one object. Prefer this over raw insight queries when judging how campaigns are performing.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"access_token": map[string]any{
					"type":        "STRING",
					"description": "Injected automatically. Ignore.",
				},
				"scope": map[string]any{
					"type":        "STRING",
					"description": "account (default), campaign, adset or ad.",
				},
				"object_id": map[string]any{
					"type":        "STRING",
					"description": "Required unless scope is account.",
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "today, yesterday, last_7d, last_14d, last_30d (default), this_month, last_month.",
				},
			},
			"required": []string{},
		},
		Handler: GetAnalytics,
	})
}

// ToolsGoalSnapshot is set per-request by the host so tool results can
// include goal progress. The agent runtime executes synchronously on one
// goroutine per request, which makes this safe today.
var ToolsGoalSnapshot *analytics.GoalSnapshot

// GetAnalytics executes the intelligence layer for one scope.
func GetAnalytics(
	ctx ToolContext,
	args map[string]any,
) (any, error) {
	scopeName := "account"
	if s, ok := argString(args, "scope"); ok {
		scopeName = s
	}

	scope := analytics.NormalizeScopePublic(scopeName)

	objectID := ""
	if id, ok := argString(args, "object_id"); ok {
		objectID = id
	}

	if scope != analytics.ScopeAccount && objectID == "" {
		return nil, fmt.Errorf(
			"object_id is required when scope is %s", scope)
	}

	datePreset := "last_30d"
	if d, ok := argString(args, "date_preset"); ok {
		datePreset = d
	}

	var goal *analytics.GoalSnapshot
	goal = ToolsGoalSnapshot

	report, err := analytics.BuildReport(
		ctx.AccessToken,
		ctx.AdAccountID,
		objectID,
		scope,
		datePreset,
		goal,
		false,
	)

	if err != nil {
		return nil, err
	}

	return report, nil
}

// GetRecommendations returns concrete recommended actions per campaign
// from the optimization decision engine.
func GetRecommendations(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	datePreset := "last_30d"
	if d, ok := argString(args, "date_preset"); ok {
		datePreset = d
	}

	report, err := analytics.BuildReport(
		ctx.AccessToken,
		ctx.AdAccountID,
		"",
		analytics.ScopeAccount,
		datePreset,
		ToolsGoalSnapshot,
		true,
	)
	if err != nil {
		return nil, err
	}

	return optimization.OptimizeReport(report, ToolsGoalSnapshot), nil
}
