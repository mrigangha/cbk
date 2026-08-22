package optimization

import (
	"testing"

	"github.com/mrigangha/cbk/internals/analytics"
)

func goalWith(cpl, cpa, roas float64) *analytics.GoalSnapshot {
	g := &analytics.GoalSnapshot{Name: "Test Goal"}
	if cpl > 0 {
		g.TargetCPL = &cpl
	}
	if cpa > 0 {
		g.TargetCPA = &cpa
	}
	if roas > 0 {
		g.TargetROAS = &roas
	}
	return g
}

func TestNotDelivering_Pauses(t *testing.T) {

	in := Input{
		Analysis: analytics.ObjectAnalysis{
			ID:    "111",
			Name:  "Silent Campaign",
			Health: analytics.Health{Status: "NOT_DELIVERING"},
		},
		DaysInWindow: 10,
	}

	d := Optimize(in)

	if d.RecommendedAction != ActionPauseCampaign {
		t.Fatalf("action = %s, want PAUSE_CAMPAIGN", d.RecommendedAction)
	}
	if !d.RequiresConfirmation {
		t.Error("pause must require confirmation")
	}
	if d.SuggestedChange == nil || d.SuggestedChange.Status != "PAUSED" {
		t.Errorf("change = %+v", d.SuggestedChange)
	}
}

func TestCPLAboveTarget_DecreasesThenPauses(t *testing.T) {

	// CPL 375 vs target 300 = 25% over → decrease.
	mild := Input{
		Analysis: analytics.ObjectAnalysis{
			ID: "222",
			Metrics: analytics.Metrics{
				Spend: 3000, Clicks: 500, Leads: 8, CPL: 375,
			},
			Health: analytics.Health{Status: "WATCH"},
		},
		Goal:         goalWith(300, 0, 0),
		DaysInWindow: 30,
	}

	d := Optimize(mild)
	if d.RecommendedAction != ActionDecreaseBudget {
		t.Fatalf("mild case action = %s", d.RecommendedAction)
	}
	if *d.SuggestedChange.DailyBudgetPct >= 0 {
		t.Errorf("budget pct = %v, want negative", *d.SuggestedChange.DailyBudgetPct)
	}

	// CPL 700 vs target 300 = 133% over with spend → pause wins.
	severe := mild
	severe.Analysis.Metrics.CPL = 700
	severe.Analysis.Metrics.Spend = 2000

	d = Optimize(severe)
	if d.RecommendedAction != ActionPauseCampaign {
		t.Fatalf("severe case action = %s, want PAUSE_CAMPAIGN", d.RecommendedAction)
	}
	if d.Confidence < 0.75 {
		t.Errorf("confidence = %.2f, want >= 0.75", d.Confidence)
	}
}

func TestHighPerformerUnderpaced_Scales(t *testing.T) {

	in := Input{
		Analysis: analytics.ObjectAnalysis{
			ID:   "333",
			Name: "Winner",
			Metrics: analytics.Metrics{
				Spend: 1000, Conversions: 50,
			},
			Performance: analytics.Performance{Score: 85},
			Health:      analytics.Health{Status: "HEALTHY"},
			Budget: &analytics.BudgetEfficiency{
				EfficiencyIndex: 1.8,
				PacingPct:       55,
			},
		},
		SetTotals: analytics.Metrics{
			Spend: 5000, Conversions: 80,
		},
		DaysInWindow: 30,
	}

	d := Optimize(in)

	if d.RecommendedAction != ActionIncreaseBudget {
		t.Fatalf("action = %s", d.RecommendedAction)
	}
	if got := *d.SuggestedChange.DailyBudgetPct; got <= 0 {
		t.Errorf("budget pct = %v, want positive", got)
	}
}

func TestFatigue_TriggersRefresh(t *testing.T) {

	in := Input{
		Analysis: analytics.ObjectAnalysis{
			ID: "444",
			Metrics: analytics.Metrics{
				Spend: 500, Impressions: 60000, Frequency: 4.2,
			},
			Fatigue: &analytics.CreativeFatigue{
				Level:        "high",
				Frequency:    4.2,
				CTRChangePct: -38,
			},
			Health: analytics.Health{Status: "WATCH"},
		},
		DaysInWindow: 14,
	}

	d := Optimize(in)

	if d.RecommendedAction != ActionRefreshCreative {
		t.Fatalf("action = %s", d.RecommendedAction)
	}
	if d.Confidence < 0.7 {
		t.Errorf("confidence = %.2f for high fatigue", d.Confidence)
	}
}

func TestHealthyCampaign_Keeps(t *testing.T) {

	in := Input{
		Analysis: analytics.ObjectAnalysis{
			ID: "555",
			Metrics: analytics.Metrics{
				Spend: 800, Impressions: 40000, Clicks: 600,
				Conversions: 20, ConversionValue: 4000,
			},
			Performance: analytics.Performance{Score: 82},
			Health:      analytics.Health{Status: "HEALTHY"},
		},
		Goal:         goalWith(0, 40, 2),
		DaysInWindow: 30,
	}

	d := Optimize(in)

	if d.RecommendedAction != ActionNoAction {
		t.Fatalf("action = %s, want KEEP", d.RecommendedAction)
	}
}

func TestOptimizeReport_SortsAndSumsmarizes(t *testing.T) {

	report := &analytics.Report{
		Scope:      analytics.ScopeAccount,
		DatePreset: "last_30d",
		DaysInWin:  30,
		Totals:     analytics.Metrics{Spend: 5000, Conversions: 80},
		Objects: []analytics.ObjectAnalysis{
			{
				ID: "a", Name: "Sleeper",
				Health: analytics.Health{Status: "NOT_DELIVERING"},
			},
			{
				ID: "b", Name: "Solid",
				Metrics:     analytics.Metrics{Spend: 900, Conversions: 25},
				Performance: analytics.Performance{Score: 80},
				Health:      analytics.Health{Status: "HEALTHY"},
			},
			{
				ID: "c", Name: "Burner",
				Metrics: analytics.Metrics{
					Spend: 2500, Leads: 5, CPL: 500,
				},
				Health: analytics.Health{Status: "CRITICAL"},
			},
		},
	}

	plan := OptimizeReport(report, goalWith(300, 0, 0))

	if len(plan.Recommendations) != 3 {
		t.Fatalf("recommendations = %d", len(plan.Recommendations))
	}

	first := plan.Recommendations[0]
	if first.RecommendedAction != ActionPauseCampaign {
		t.Errorf("top recommendation = %s (%s), want the PAUSE_CAMPAIGN first",
			first.RecommendedAction, first.CampaignName)
	}

	// Burner: CPL 500 vs 300 target → budget cut.
	if plan.Summary[ActionDecreaseBudget] != 1 {
		t.Errorf("summary decrease count = %d", plan.Summary[ActionDecreaseBudget])
	}

	// Solid: 25/80 conversions from 18% of spend (efficiency 1.74) → scale.
	if plan.Summary[ActionIncreaseBudget] != 1 {
		t.Errorf("summary increase count = %d", plan.Summary[ActionIncreaseBudget])
	}

	if len(plan.NeedsAttention) == 0 {
		t.Error("expected at least one campaign flagged for attention")
	}

	if plan.GeneratedAt == "" || plan.GoalName != "Test Goal" {
		t.Errorf("plan metadata incomplete: %+v", plan)
	}
}
