package tools

import (
	"fmt"

	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
)

// OptimizationPersister is set by the host (core) so that
// optimize_campaign with dry_run=false can store PENDING proposals for
// human approval. Tools cannot touch the database directly.
// The agent runtime executes synchronously per request, which keeps
// this global safe today.
var OptimizationPersister func(
	token, accountID, datePreset, level string,
	goal *analytics.GoalSnapshot,
) (stored int, skipped int, err error)

// OptimizeCampaign is the agent-facing entry point to the decision
// engine. With dry_run=true (default) it only reports what it WOULD do;
// with dry_run=false it stores PENDING proposals awaiting approval.
func OptimizeCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	dryRun := true // safe default
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}

	datePreset := "last_30d"
	if d, ok := argString(args, "date_preset"); ok {
		datePreset = d
	}

	level := "campaigns"
	if l, ok := argString(args, "level"); ok && l == "adsets" {
		level = "adsets"
	}

	var goal *analytics.GoalSnapshot
	goal = ToolsGoalSnapshot

	report, err := buildOptimizationReport(
		ctx.AccessToken, ctx.AdAccountID, datePreset, level, goal,
	)
	if err != nil {
		return nil, err
	}

	plan := optimization.OptimizeReport(report, goal)

	if dryRun {
		return map[string]any{
			"dry_run":         true,
			"goal":            plan.GoalName,
			"date_preset":     plan.DatePreset,
			"recommendations": plan.Recommendations,
			"summary":         plan.Summary,
			"note":            "Dry run only — no proposals stored, nothing changed.",
		}, nil
	}

	if OptimizationPersister == nil {
		return nil, fmt.Errorf(
			"persistence unavailable in this context; use dry_run=true")
	}

	stored, skipped, err := OptimizationPersister(
		ctx.AccessToken, ctx.AdAccountID, datePreset, level, goal,
	)
	if err != nil {
		return nil, err
	}

	note := "Proposals stored as PENDING. They will NOT execute until a human approves them."
	if stored == 0 && skipped > 0 {
		note = "Identical proposals are already pending approval."
	}

	return map[string]any{
		"dry_run":           false,
		"goal":              plan.GoalName,
		"recommendations":   plan.Recommendations,
		"summary":           plan.Summary,
		"stored_proposals":  stored,
		"duplicate_skipped": skipped,
		"needs_attention":   plan.NeedsAttention,
		"note":              note,
	}, nil
}

// BuildOptimizationReport is exported so the host can reuse the same
// report construction when persisting proposals.
func BuildOptimizationReport(
	token, accountID, datePreset, level string,
	goal *analytics.GoalSnapshot,
) (*analytics.Report, error) {
	return buildOptimizationReport(token, accountID, datePreset, level, goal)
}

func buildOptimizationReport(
	token, accountID, datePreset, level string,
	goal *analytics.GoalSnapshot,
) (*analytics.Report, error) {

	scope := analytics.ScopeCampaign
	if level == "adsets" {
		scope = analytics.ScopeAdSet
	}

	objects, err := analytics.ListObjects(token, accountID, level)
	if err != nil {
		return nil, err
	}

	analyses, totals, err := analytics.AnalyzeSet(
		token, scope, objects, datePreset, goal, true,
	)
	if err != nil {
		return nil, err
	}

	days := 30
	switch datePreset {
	case "today", "yesterday":
		days = 1
	case "last_7d":
		days = 7
	case "last_14d":
		days = 14
	}

	return &analytics.Report{
		Scope:      scope,
		DatePreset: datePreset,
		DaysInWin:  days,
		Totals:     totals,
		Objects:    analyses,
	}, nil
}
