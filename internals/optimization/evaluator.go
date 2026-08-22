package optimization

import (
	"fmt"

	"github.com/mrigangha/cbk/internals/analytics"
)

// Outcome verdicts stored on executed proposals.
type OutcomeVerdict string

const (
	VerdictSuccess          OutcomeVerdict = "SUCCESS"
	VerdictFailure          OutcomeVerdict = "FAILURE"
	VerdictNeutral          OutcomeVerdict = "NEUTRAL"
	VerdictInsufficientData OutcomeVerdict = "INSUFFICIENT_DATA"
)

// Evaluation is the computed answer to "was this optimization good?".
type Evaluation struct {
	Result           OutcomeVerdict `json:"result"`
	CPAChangePct     *float64       `json:"cpa_change_pct,omitempty"`
	CPLChangePct     *float64       `json:"cpl_change_pct,omitempty"`
	CTRChangePct     *float64       `json:"ctr_change_pct,omitempty"`
	ConversionsDelta *float64       `json:"conversions_delta,omitempty"`
	SpendChangePct   *float64       `json:"spend_change_pct,omitempty"`
	Reasons          []string       `json:"reasons,omitempty"`
}

// EvaluateAction scores an executed optimization by comparing the
// metric snapshot captured BEFORE execution against the AFTER window.
//
// Success criteria by action:
//
//	INCREASE_BUDGET   conversions ↑ AND CPA within target (when a target exists)
//	DECREASE_BUDGET   CPA improved OR results maintained on less spend
//	PAUSE_*           object stopped consuming meaningful spend
//	RESUME_CAMPAIGN   object started delivering again
//	REFRESH_CREATIVE  CTR ↑ AND CPA ↓
func EvaluateAction(
	action string,
	before analytics.Metrics,
	after analytics.Metrics,
	goal *analytics.GoalSnapshot,
) Evaluation {

	b := analytics.DeriveRatios(before)
	a := analytics.DeriveRatios(after)

	ev := Evaluation{}

	pctChange := func(from, to float64) *float64 {
		if from <= 0 {
			return nil // undefined relative to zero baseline
		}
		v := round2((to - from) / from * 100)
		return &v
	}

	ev.CPAChangePct = pctChange(b.CPA, a.CPA)
	ev.CPLChangePct = pctChange(b.CPL, a.CPL)
	ev.CTRChangePct = pctChange(b.CTR, a.CTR)
	ev.SpendChangePct = pctChange(b.Spend, a.Spend)
	if d := round2(a.Conversions - b.Conversions); d != 0 {
		ev.ConversionsDelta = &d
	}

	addReason := func(format string, args ...any) {
		ev.Reasons = append(ev.Reasons, fmt.Sprintf(format, args...))
	}

	// No delivery in the after window at all.
	if a.Impressions == 0 && a.Spend == 0 {
		switch action {
		case ActionPauseCampaign, ActionPauseAdSet, ActionPauseAd:
			// Silence is exactly what a pause wants.
			ev.Result = VerdictSuccess
			ev.Reasons = append(ev.Reasons,
				"object stopped consuming budget entirely")
			return ev
		default:
			ev.Result = VerdictInsufficientData
			ev.Reasons = append(ev.Reasons,
				"no impressions or spend in the after window")
			return ev
		}
	}

	switch action {

	case ActionIncreaseBudget:

		conversionsUp := b.Conversions == 0 ||
			a.Conversions > b.Conversions

		cpaWithinTarget := true
		if goal != nil && goal.TargetCPA != nil && *goal.TargetCPA > 0 && a.CPA > 0 {
			cpaWithinTarget = a.CPA <= *goal.TargetCPA*1.1 // 10% grace band
		}

		if conversionsUp && cpaWithinTarget {
			ev.Result = VerdictSuccess
			addReason("conversions rose %.0f→%.0f", b.Conversions, a.Conversions)
			if !cpaWithinTarget {
				ev.Result = VerdictNeutral
			}
			if ev.CPAChangePct != nil {
				addReason("CPA change %+.1f%%", *ev.CPAChangePct)
			}
		} else {
			ev.Result = VerdictFailure
			if !conversionsUp {
				addReason("conversions fell %.0f→%.0f", b.Conversions, a.Conversions)
			}
			if !cpaWithinTarget && goal != nil && goal.TargetCPA != nil {
				addReason("CPA %.2f exceeds target %.2f",
					a.CPA, *goal.TargetCPA)
			}
		}

	case ActionDecreaseBudget:

		cpaImproved := b.CPA > 0 && a.CPA > 0 &&
			a.CPA < b.CPA*0.95 // ≥5% better counts

		resultsHeld := b.Conversions > 0 &&
			a.Conversions >= b.Conversions*0.9 &&
			a.Spend < b.Spend

		wasteCut := b.Conversions == 0 && a.Spend < b.Spend

		switch {
		case cpaImproved:
			ev.Result = VerdictSuccess
			addReason("CPA improved %+.1f%%", *ev.CPAChangePct)
		case resultsHeld:
			ev.Result = VerdictSuccess
			addReason("results held (%.0f→%.0f conversions) on %+.1f%% less spend",
				b.Conversions, a.Conversions, *ev.SpendChangePct)
		case wasteCut:
			ev.Result = VerdictSuccess
			addReason("spending cut %+.1f%% with no conversions lost that existed",
				*ev.SpendChangePct)
		default:
			ev.Result = VerdictFailure
			if ev.CPAChangePct != nil {
				addReason("CPA worsened %+.1f%%", *ev.CPAChangePct)
			}
			if ev.ConversionsDelta != nil {
				addReason("conversions changed %+0.0f", *ev.ConversionsDelta)
			}
		}

	case ActionPauseCampaign, ActionPauseAdSet, ActionPauseAd:

		// Success = the object stopped consuming meaningful spend.
		if a.Spend <= b.Spend*0.25 {
			ev.Result = VerdictSuccess
			addReason("post-pause spend %.2f is ≤25%% of the pre-action rate %.2f",
				a.Spend, b.Spend)
		} else {
			ev.Result = VerdictFailure
			addReason("still spending %.2f after pause", a.Spend)
		}

	case ActionResumeCampaign:

		if a.Impressions > 0 {
			ev.Result = VerdictSuccess
			addReason("delivering again (%.0f impressions)", a.Impressions)
		} else {
			ev.Result = VerdictFailure
			addReason("still not delivering after resume")
		}

	case ActionRefreshCreative:

		ctrUp := ev.CTRChangePct != nil && *ev.CTRChangePct > 5
		cpaDown := ev.CPAChangePct != nil && *ev.CPAChangePct < -5

		if ctrUp && cpaDown {
			ev.Result = VerdictSuccess
			addReason("CTR %+.1f%% and CPA %+.1f%%",
				*ev.CTRChangePct, *ev.CPAChangePct)
		} else {
			ev.Result = VerdictNeutral
			addReason("CTR change %s, CPA change %s — no clear win yet",
				fmtPctPtr(ev.CTRChangePct), fmtPctPtr(ev.CPAChangePct))
		}

	default:
		ev.Result = VerdictNeutral
		addReason("no evaluation criteria defined for %s", action)
	}

	return ev
}

func fmtPctPtr(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", *p)
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
