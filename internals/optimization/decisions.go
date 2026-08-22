package optimization

import (
	"math"

	"github.com/mrigangha/cbk/internals/analytics"
)

// Input bundles everything the rules may look at.
type Input struct {
	Analysis     analytics.ObjectAnalysis
	ObjectType   ObjectType
	Goal         *analytics.GoalSnapshot
	SetTotals    analytics.Metrics
	DaysInWindow int
}

// Decision is one recommended action — the shape returned by the
// decision engine and exposed over the API and to the agent.
type Decision struct {
	CampaignID       string           `json:"campaign_id"`
	CampaignName     string           `json:"campaign_name,omitempty"`
	ObjectType       ObjectType       `json:"object_type"`
	Health           string           `json:"health"`
	PerformanceScore int              `json:"performance_score"`
	RuleID           string           `json:"rule_id"`
	Reason           string           `json:"reason"`
	RecommendedAction string          `json:"recommended_action"`
	Confidence       float64          `json:"confidence"` // 0–1
	SuggestedChange  *SuggestedChange `json:"suggested_change,omitempty"`
	Signals          []string         `json:"signals,omitempty"`

	// RequiresConfirmation marks actions a human must approve before
	// execution (pauses, increases, anything that can spend or stop money).
	RequiresConfirmation bool `json:"requires_confirmation"`
}

func baseDecision(in Input, ruleID string) Decision {
	return Decision{
		CampaignID:   in.Analysis.ID,
		CampaignName: in.Analysis.Name,
		ObjectType:   in.ObjectType,
		Health:       in.Analysis.Health.Status,
		PerformanceScore: in.Analysis.Performance.Score,
		RuleID:       ruleID,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 0.99 {
		return 0.99
	}
	return math.Round(v*100) / 100
}

// ratio returns value ÷ target (1.5 = 50% above target).
func ratio(value, target float64) float64 {
	if target <= 0 {
		return 0
	}
	return value / target
}
