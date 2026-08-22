package optimization

import (
	"sort"
	"time"

	"github.com/mrigangha/cbk/internals/analytics"
)

// Rule is one decision heuristic. Evaluate returns nil when the rule
// does not apply to the given input.
type Rule struct {
	ID       string
	Priority int // lower = consulted first (documentation only)
	Evaluate func(Input) *Decision
}

// Rules is the full rule set, evaluated in order; the highest-confidence
// fired decision wins per object.
var Rules = []Rule{
	{ID: "not_delivering", Priority: 10, Evaluate: notDeliveringRule.Evaluate},
	{ID: "cpa_above_target", Priority: 20, Evaluate: cpaAboveTargetRule.Evaluate},
	{ID: "cpl_above_target", Priority: 21, Evaluate: cplAboveTargetRule.Evaluate},
	{ID: "roas_below_target", Priority: 22, Evaluate: roasBelowTargetRule.Evaluate},
	{ID: "zero_result_spend", Priority: 30, Evaluate: zeroResultSpendRule.Evaluate},
	{ID: "creative_fatigue", Priority: 40, Evaluate: fatigueRefreshRule.Evaluate},
	{ID: "high_performer_underpaced", Priority: 50, Evaluate: highPerformerScaleRule.Evaluate},
	{ID: "healthy_keep", Priority: 90, Evaluate: keepHealthyRule.Evaluate},
}

// Plan is the optimizer's output for a set of campaigns.
type Plan struct {
	GeneratedAt     string           `json:"generated_at"`
	Scope           string           `json:"scope"`
	DatePreset      string           `json:"date_preset"`
	GoalName        string           `json:"goal_name,omitempty"`
	Recommendations []Decision       `json:"recommendations"`
	Summary         map[string]int   `json:"summary"`          // action → count
	NeedsAttention  []string         `json:"needs_attention"`  // object names requiring human review
}

// Optimize picks the strongest applicable decision for one object.
func Optimize(in Input) Decision {

	// Rules assume consistent derived fields (CPL/CPA/ROAS/CTR...);
	// fill any gaps so hand-assembled analyses behave like fetched ones.
	in.Analysis.Metrics = analytics.DeriveRatios(in.Analysis.Metrics)

	var best *Decision

	for i := range Rules {
		if d := Rules[i].Evaluate(in); d != nil {
			if best == nil || d.Confidence > best.Confidence {
				d.RuleID = Rules[i].ID
				best = d
			}
		}
	}

	if best == nil {
		d := baseDecision(in, "monitor")
		d.Reason = "insufficient signals for a confident recommendation"
		d.RecommendedAction = ActionNoAction
		d.Confidence = 0.3
		best = &d
	}

	return *best
}

// OptimizeReport runs the rule set across every analyzed campaign in an
// analytics report and produces a sorted plan.
func OptimizeReport(
	report *analytics.Report,
	goal *analytics.GoalSnapshot,
) *Plan {

	plan := &Plan{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Scope:       string(report.Scope),
		DatePreset:  report.DatePreset,
		Summary:     map[string]int{},
		NeedsAttention: []string{},
	}

	if goal != nil {
		plan.GoalName = goal.Name
	}

	objectType := objectTypeForScope(report.Scope)

	for _, obj := range report.Objects {

		in := Input{
			Analysis:     obj,
			ObjectType:   objectType,
			Goal:         goal,
			SetTotals:    report.Totals,
			DaysInWindow: report.DaysInWin,
		}

		d := Optimize(in)

		plan.Recommendations = append(plan.Recommendations, d)

		plan.Summary[d.RecommendedAction]++

		if d.RequiresConfirmation &&
			(isPauseAction(d.RecommendedAction) || d.Confidence >= 0.8) {
			plan.NeedsAttention = append(plan.NeedsAttention, d.CampaignName)
		}
	}

	sort.SliceStable(plan.Recommendations, func(i, j int) bool {

		a, b := plan.Recommendations[i], plan.Recommendations[j]

		// Actionable decisions first (KEEP sinks), then by confidence.
		rankA := actionUrgency(a)
		rankB := actionUrgency(b)

		if rankA != rankB {
			return rankA < rankB
		}
		return a.Confidence > b.Confidence
	})

	return plan
}

func actionUrgency(d Decision) int {
	switch d.RecommendedAction {
	case ActionPauseCampaign, ActionPauseAdSet, ActionPauseAd:
		return 0
	case ActionDecreaseBudget:
		return 1
	case ActionRefreshCreative:
		return 2
	case ActionIncreaseBudget:
		return 3
	default:
		return 9
	}
}

func isPauseAction(a string) bool {
	return a == ActionPauseCampaign || a == ActionPauseAdSet || a == ActionPauseAd
}

func objectTypeForScope(s analytics.Scope) ObjectType {
	switch s {
	case analytics.ScopeAdSet:
		return ObjectAdSet
	case analytics.ScopeAd:
		return ObjectAd
	default:
		return ObjectCampaign
	}
}
