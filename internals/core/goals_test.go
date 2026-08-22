package core

import (
	"strings"
	"testing"
)

func TestGoalContextBlock_RendersTargetsAndRules(t *testing.T) {

	cpl := 300.0
	conversions := int64(500)
	daily := 10000.0

	goal := &MarketingGoal{
		ID:                1,
		Name:              "Generate Bangalore Leads",
		Description:       "Qualified leads for Bangalore.",
		Objective:         "LEADS",
		Status:            "ACTIVE",
		TargetCPL:         &cpl,
		TargetConversions: &conversions,
		DailyBudget:       &daily,
		StartDate:         "2026-09-01",
	}

	block := goalContextBlock(goal)

	checks := []string{
		"MARKETING GOAL (persistent objective",
		"Name: Generate Bangalore Leads",
		"Objective: LEADS",
		"cost per lead < 300",
		"conversions >= 500",
		"daily spend <= 10000",
		"2026-09-01 to -",
		"Never treat the instruction as replacing or modifying the goal",
	}

	for _, want := range checks {
		if !strings.Contains(block, want) {
			t.Errorf("goal context missing %q\nblock:\n%s", want, block)
		}
	}
}

func TestGoalContextBlock_Minimal(t *testing.T) {

	block := goalContextBlock(&MarketingGoal{
		Name:      "Awareness Push",
		Objective: "AWARENESS",
		Status:    "ACTIVE",
	})

	if !strings.Contains(block, "Objective: AWARENESS") {
		t.Errorf("objective missing:\n%s", block)
	}

	if strings.Contains(block, "Success targets") {
		t.Errorf("targets section should be omitted when empty:\n%s", block)
	}
}
