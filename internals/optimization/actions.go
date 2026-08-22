// Package optimization turns analytics intelligence into concrete,
// ranked recommended actions for campaigns, ad sets and ads.
//
// Actions are PROPOSALS ONLY. Nothing in this package executes against
// the Meta API: every decision must be explicitly approved by a human
// before an executor (future milestone) may act on it.
package optimization

// Explicit action vocabulary.
const (
	ActionIncreaseBudget  = "INCREASE_BUDGET"
	ActionDecreaseBudget  = "DECREASE_BUDGET"
	ActionPauseCampaign   = "PAUSE_CAMPAIGN"
	ActionPauseAdSet      = "PAUSE_ADSET"
	ActionPauseAd         = "PAUSE_AD"
	ActionResumeCampaign  = "RESUME_CAMPAIGN"
	ActionRefreshCreative = "REFRESH_CREATIVE"
	ActionNoAction        = "NO_ACTION"
)

// ObjectType disambiguates what an action applies to. The same rule set
// runs at campaign, ad set and ad level; the object type picks the
// correct PAUSE_* verb.
type ObjectType string

const (
	ObjectCampaign ObjectType = "CAMPAIGN"
	ObjectAdSet    ObjectType = "ADSET"
	ObjectAd       ObjectType = "AD"
)

// PauseActionFor returns the scope-correct pause verb.
func PauseActionFor(t ObjectType) string {
	switch t {
	case ObjectAdSet:
		return ActionPauseAdSet
	case ObjectAd:
		return ActionPauseAd
	default:
		return ActionPauseCampaign
	}
}

// ValidAction reports whether s is part of the vocabulary.
func ValidAction(s string) bool {
	switch s {
	case ActionIncreaseBudget,
		ActionDecreaseBudget,
		ActionPauseCampaign,
		ActionPauseAdSet,
		ActionPauseAd,
		ActionResumeCampaign,
		ActionRefreshCreative,
		ActionNoAction:
		return true
	}
	return false
}

// Lifecycle status of a stored proposal. Execution is gated on APPROVED
// but performed by the future executor milestone — never automatically.
const (
	StatusPending  = "PENDING"
	StatusApproved = "APPROVED"
	StatusRejected = "REJECTED"
	StatusExecuted = "EXECUTED"
	StatusFailed   = "FAILED"
)

// SuggestedChange is the concrete parameter delta that backs an action.
// Budget changes are expressed as percentages of the current daily
// budget so the executor can compute absolute amounts later.
type SuggestedChange struct {
	DailyBudgetPct *float64 `json:"daily_budget_pct,omitempty"` // e.g. -20 = reduce 20%
	Status         string   `json:"status,omitempty"`           // e.g. PAUSED
	Note           string   `json:"note,omitempty"`
}

// Pct is a small helper for building percentage pointers.
func Pct(v float64) *float64 { return &v }
