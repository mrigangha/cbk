package core

import (
	"fmt"
	"math"
	"time"

	"github.com/mrigangha/cbk/internals/optimization"
)

// Guardrails are the hard safety limits for executing optimization
// actions against the Meta API. They apply on top of the approval gate:
// an APPROVED action can still be refused here.
type Guardrails struct {
	// MaxIncreasePct caps any single budget raise.
	MaxIncreasePct float64

	// MaxDecreasePct caps any single budget cut.
	MaxDecreasePct float64

	// MinConversionsForScale: campaigns with fewer conversions in the
	// window are never scaled up.
	MinConversionsForScale int

	// MinDataDays: objects with a shorter observation window have
	// insufficient data — no modifications at all.
	MinDataDays int

	// Cooldown window between two executions on the same object.
	Cooldown time.Duration
}

// DefaultGuardrails encodes the product's spend-safety policy.
func DefaultGuardrails() Guardrails {
	return Guardrails{
		MaxIncreasePct:         20,
		MaxDecreasePct:         30,
		MinConversionsForScale: 5,
		MinDataDays:            7,
		Cooldown:               24 * time.Hour,
	}
}

// ValidationContext carries what the executor learned before touching
// the Meta API.
type ValidationContext struct {
	ObjectType     string
	Action         string
	CurrentStatus  string
	Conversions    float64
	DaysInWindow   int
	RequestedPct   *float64 // from suggested_change, if any
	LastExecutedAt *time.Time
	Now            time.Time
}

// Validate applies every guardrail. It returns the (possibly clamped)
// budget percentage to apply plus a rejection reason when invalid.
func (g Guardrails) Validate(vc ValidationContext) (pct *float64, ok bool, reason string) {

	if !optimization.ValidAction(vc.Action) {
		return nil, false, fmt.Sprintf("unknown action %q", vc.Action)
	}

	// --- Insufficient data blocks everything except refresh proposals.
	if vc.Action != optimization.ActionRefreshCreative &&
		vc.DaysInWindow < g.MinDataDays {
		return nil, false, fmt.Sprintf(
			"insufficient data: only %d days observed, need %d",
			vc.DaysInWindow, g.MinDataDays)
	}

	// --- Cooldown: never modify the same object twice inside the window.
	if vc.LastExecutedAt != nil && vc.Now.Sub(*vc.LastExecutedAt) < g.Cooldown {
		return nil, false, fmt.Sprintf(
			"cooldown active: %s was modified %.0f minutes ago (limit %dh)",
			vc.ObjectType,
			vc.Now.Sub(*vc.LastExecutedAt).Minutes(),
			int(g.Cooldown.Hours()),
		)
	}

	switch vc.Action {

	case optimization.ActionPauseCampaign,
		optimization.ActionPauseAdSet,
		optimization.ActionPauseAd:

		if vc.CurrentStatus == "PAUSED" || vc.CurrentStatus == "ARCHIVED" {
			return nil, false, "object is already paused"
		}
		return nil, true, ""

	case optimization.ActionResumeCampaign:
		if vc.CurrentStatus != "PAUSED" {
			return nil, false, "only paused campaigns can be resumed"
		}
		return nil, true, ""

	case optimization.ActionIncreaseBudget:

		if vc.CurrentStatus == "PAUSED" {
			return nil, false, "never increase budget on a paused campaign"
		}
		if vc.Conversions < float64(g.MinConversionsForScale) {
			return nil, false, fmt.Sprintf(
				"refusing to scale: only %.0f conversions in window, need %d",
				vc.Conversions, g.MinConversionsForScale)
		}
		if vc.RequestedPct == nil {
			return nil, false, "missing budget percentage"
		}

		p := *vc.RequestedPct
		if p <= 0 {
			return nil, false, "increase percentage must be positive"
		}
		if p > g.MaxIncreasePct {
			p = g.MaxIncreasePct // clamp, never reject downward drift of intent
		}
		return &p, true, ""

	case optimization.ActionDecreaseBudget:

		if vc.CurrentStatus == "PAUSED" {
			return nil, false, "never modify a paused campaign"
		}
		if vc.RequestedPct == nil {
			return nil, false, "missing budget percentage"
		}

		p := *vc.RequestedPct
		if p >= 0 {
			return nil, false, "decrease percentage must be negative"
		}
		if -p > g.MaxDecreasePct {
			p = -g.MaxDecreasePct // clamp cuts to the policy ceiling
		}
		return &p, true, ""

	case optimization.ActionRefreshCreative:
		// Not executable by the API yet — surfaced as manual guidance.
		return nil, false, "REFRESH_CREATIVE is a manual action; create new ads in Ads Manager"
	}

	return nil, false, fmt.Sprintf("action %q is not executable", vc.Action)
}

// ApplyBudgetPct computes the new daily budget in minor units.
func ApplyBudgetPct(current float64, pct float64) int64 {
	if current <= 0 {
		return 0
	}
	next := current * (1 + pct/100)
	return int64(math.Round(next))
}
