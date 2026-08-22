package optimization

import (
	"fmt"
	"math"
)

// ===========================
// RULES
// ===========================

var notDeliveringRule = Rule{
	ID: "not_delivering",
	Evaluate: func(in Input) *Decision {

		if in.Analysis.Health.Status != "NOT_DELIVERING" {
			return nil
		}

		conf := 0.75
		if in.DaysInWindow >= 7 {
			conf = 0.9
		}

		d := baseDecision(in, "not_delivering")
		d.Reason = fmt.Sprintf(
			"active for %d days with zero impressions — no delivery",
			in.DaysInWindow,
		)
		d.RecommendedAction = PauseActionFor(in.ObjectType)
		d.Confidence = clamp01(conf)
		d.RequiresConfirmation = true
		d.SuggestedChange = &SuggestedChange{
			Status: "PAUSED",
			Note:   "stop silent budget allocation; review audience/creative",
		}
		return &d
	},
}

var cpaAboveTargetRule = Rule{
	ID: "cpa_above_target",
	Evaluate: func(in Input) *Decision {

		m := in.Analysis.Metrics

		if in.Goal == nil || in.Goal.TargetCPA == nil || m.Conversions == 0 {
			return nil
		}

		target := *in.Goal.TargetCPA
		r := ratio(m.CPA, target)

		d := baseDecision(in, "cpa_above_target")
		d.Reason = fmt.Sprintf(
			"CPA is %.2f vs target %.2f (%+.0f%%)",
			m.CPA, target, (r-1)*100,
		)

		switch {
		case r >= 2.0 && m.Spend > minSpendForPause:
			d.RecommendedAction = PauseActionFor(in.ObjectType)
			d.Confidence = clamp01(0.8 + (r-2)*0.05)
			d.SuggestedChange = &SuggestedChange{
				Status: "PAUSED",
				Note:   "cost per acquisition double the target",
			}
		case r >= 1.25:
			pct := -20.0
			if r >= 1.6 {
				pct = -30
			}
			d.RecommendedAction = ActionDecreaseBudget
			d.Confidence = clamp01(0.55 + (r-1.25)*0.3)
			d.SuggestedChange = &SuggestedChange{
				DailyBudgetPct: Pct(pct),
				Note:           "pull spend back toward target CPA",
			}
		default:
			return nil // within tolerance band
		}

		d.RequiresConfirmation = true
		return &d
	},
}

var cplAboveTargetRule = Rule{
	ID: "cpl_above_target",
	Evaluate: func(in Input) *Decision {

		m := in.Analysis.Metrics

		if in.Goal == nil || in.Goal.TargetCPL == nil || m.Leads == 0 {
			return nil
		}

		target := *in.Goal.TargetCPL
		r := ratio(m.CPL, target)

		d := baseDecision(in, "cpl_above_target")
		d.Reason = fmt.Sprintf(
			"CPL is %.2f vs target %.2f (%+.0f%%)",
			m.CPL, target, (r-1)*100,
		)

		switch {
		case r >= 2.0 && m.Spend > minSpendForPause:
			d.RecommendedAction = PauseActionFor(in.ObjectType)
			d.Confidence = clamp01(0.8 + (r-2)*0.05)
			d.SuggestedChange = &SuggestedChange{
				Status: "PAUSED",
				Note:   "cost per lead double the target",
			}
		case r >= 1.25:
			pct := -20.0
			if r >= 1.6 {
				pct = -30
			}
			d.RecommendedAction = ActionDecreaseBudget
			d.Confidence = clamp01(0.55 + (r-1.25)*0.3)
			d.SuggestedChange = &SuggestedChange{
				DailyBudgetPct: Pct(pct),
				Note:           "reduce overspend against CPL target",
			}
		default:
			return nil
		}

		d.RequiresConfirmation = true
		return &d
	},
}

var roasBelowTargetRule = Rule{
	ID: "roas_below_target",
	Evaluate: func(in Input) *Decision {

		m := in.Analysis.Metrics

		if in.Goal == nil || in.Goal.TargetROAS == nil {
			return nil
		}
		if m.Spend <= 0 || m.ConversionValue <= 0 {
			return nil
		}

		target := *in.Goal.TargetROAS
		r := ratio(m.ROAS, target)

		d := baseDecision(in, "roas_below_target")
		d.Reason = fmt.Sprintf(
			"ROAS %.2f is only %.0f%% of the %.2f target",
			m.ROAS, r*100, target,
		)

		switch {
		case r < 0.5 && m.Spend > minSpendForPause:
			d.RecommendedAction = PauseActionFor(in.ObjectType)
			d.Confidence = clamp01(0.78 + (0.5-r)*0.2)
			d.SuggestedChange = &SuggestedChange{
				Status: "PAUSED",
				Note:   "returning less than half of target ROAS",
			}
		case r < 0.85:
			d.RecommendedAction = ActionDecreaseBudget
			d.Confidence = clamp01(0.6 + (0.85-r)*0.4)
			d.SuggestedChange = &SuggestedChange{
				DailyBudgetPct: Pct(-20),
				Note:           "trim budget until ROAS recovers",
			}
		default:
			return nil
		}

		d.RequiresConfirmation = true
		return &d
	},
}

const minSpendForPause = 100

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var zeroResultSpendRule = Rule{
	ID: "zero_result_spend",
	Evaluate: func(in Input) *Decision {

		m := in.Analysis.Metrics

		if m.Spend < minSpendForPause || m.Conversions > 0 {
			return nil
		}
		if in.DaysInWindow < 7 {
			return nil // give new campaigns room to learn
		}

		d := baseDecision(in, "zero_result_spend")
		d.Reason = fmt.Sprintf(
			"%.2f spent across %d days with zero recorded conversions",
			m.Spend, in.DaysInWindow,
		)
		d.RecommendedAction = ActionDecreaseBudget
		d.Confidence = clamp01(0.6 + math.Min(m.Spend/1000, 1)*0.2)
		d.SuggestedChange = &SuggestedChange{
			DailyBudgetPct: Pct(-30),
			Note:           "spending with no results — cut and diagnose targeting/creative",
		}
		d.RequiresConfirmation = true
		return &d
	},
}

var highPerformerScaleRule = Rule{
	ID: "high_performer_underpaced",
	Evaluate: func(in Input) *Decision {

		a := in.Analysis
		m := a.Metrics

		if m.Conversions == 0 || a.Performance.Score < 70 {
			return nil
		}

		eff := 0.0
		pacing := 0.0

		if a.Budget != nil {
			eff = a.Budget.EfficiencyIndex
			pacing = a.Budget.PacingPct
		} else if in.SetTotals.Spend > 0 && m.Spend > 0 {
			spendShare := m.Spend / in.SetTotals.Spend * 100
			resultShare := m.Conversions / maxF(in.SetTotals.Conversions, 1e-9) * 100
			if spendShare > 0 {
				eff = resultShare / spendShare
			}
		} else {
			return nil
		}

		if eff < 1.3 {
			return nil
		}
		if pacing >= 80 {
			return nil // already spending its cap
		}

		d := baseDecision(in, "high_performer_underpaced")
		d.Reason = fmt.Sprintf(
			"efficiency index %.2f (>1 means outsized results per rupee), score %d, pacing only %.0f%% of cap",
			eff, a.Performance.Score, pacing,
		)

		d.RecommendedAction = ActionIncreaseBudget
		d.Confidence = clamp01(0.6 + math.Min(eff-1.3, 0.7)*0.3)
		d.SuggestedChange = &SuggestedChange{
			DailyBudgetPct: Pct(20),
			Note:           "scale the winner gradually (+20%)",
		}
		d.RequiresConfirmation = true
		return &d
	},
}

var fatigueRefreshRule = Rule{
	ID: "creative_fatigue",
	Evaluate: func(in Input) *Decision {

		a := in.Analysis

		if a.Fatigue == nil || a.Metrics.Impressions < 5000 {
			return nil
		}

		level := a.Fatigue.Level
		if level != "moderate" && level != "high" {
			return nil
		}

		d := baseDecision(in, "creative_fatigue")
		d.Reason = fmt.Sprintf(
			"creative fatigue %s: CTR %+.1f%% week-over-week at frequency %.1f",
			level, a.Fatigue.CTRChangePct, a.Fatigue.Frequency,
		)
		d.RecommendedAction = ActionRefreshCreative
		d.Confidence = clamp01(map[string]float64{
			"moderate": 0.6, "high": 0.8,
		}[level])
		d.SuggestedChange = &SuggestedChange{
			Note: "introduce fresh creatives before performance decays further",
		}
		return &d
	},
}

var keepHealthyRule = Rule{
	ID: "healthy_keep",
	Evaluate: func(in Input) *Decision {

		if in.Analysis.Performance.Score < 60 ||
			in.Analysis.Health.Status != "HEALTHY" {
			return nil
		}

		d := baseDecision(in, "healthy_keep")
		d.Reason = fmt.Sprintf(
			"performing within range (score %d, health HEALTHY)",
			in.Analysis.Performance.Score,
		)
		d.RecommendedAction = ActionNoAction

		// Deliberately low: any actionable rule must outrank "keep".
		d.Confidence = 0.45
		return &d
	},
}
