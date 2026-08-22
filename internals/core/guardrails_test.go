package core

import (
	"strings"
	"testing"
	"time"

	"github.com/mrigangha/cbk/internals/optimization"
)

func g() Guardrails { return DefaultGuardrails() }

func vc(action string) ValidationContext {
	return ValidationContext{
		ObjectType:    "CAMPAIGN",
		Action:        action,
		CurrentStatus: "ACTIVE",
		Conversions:   50,
		DaysInWindow:  30,
		Now:           time.Now(),
	}
}

func pct(v float64) *float64 { return &v }

func TestGuardrails_BudgetIncreaseClampedToCap(t *testing.T) {

	v := vc(optimization.ActionIncreaseBudget)
	v.RequestedPct = pct(35) // asks for +35%

	pctOut, ok, reason := g().Validate(v)

	if !ok {
		t.Fatalf("rejected: %s", reason)
	}
	if *pctOut != 20 {
		t.Errorf("clamped to %v, want 20", *pctOut)
	}
}

func TestGuardrails_BudgetDecreaseClampedToCap(t *testing.T) {

	v := vc(optimization.ActionDecreaseBudget)
	v.RequestedPct = pct(-50) // asks for -50%

	pctOut, ok, reason := g().Validate(v)

	if !ok {
		t.Fatalf("rejected: %s", reason)
	}
	if *pctOut != -30 {
		t.Errorf("clamped to %v, want -30", *pctOut)
	}
}

func TestGuardrails_NeverIncreaseOnPaused(t *testing.T) {

	v := vc(optimization.ActionIncreaseBudget)
	v.CurrentStatus = "PAUSED"
	v.RequestedPct = pct(10)

	_, ok, reason := g().Validate(v)

	if ok {
		t.Fatal("increase on paused campaign must be refused")
	}
	if reason != "never increase budget on a paused campaign" {
		t.Errorf("reason = %q", reason)
	}
}

func TestGuardrails_NeverDecreaseOnPaused(t *testing.T) {

	v := vc(optimization.ActionDecreaseBudget)
	v.CurrentStatus = "PAUSED"
	v.RequestedPct = pct(-20)

	if _, ok, _ := g().Validate(v); ok {
		t.Fatal("decrease on paused campaign must be refused")
	}
}

func TestGuardrails_MinConversionsForScale(t *testing.T) {

	v := vc(optimization.ActionIncreaseBudget)
	v.Conversions = 3 // below threshold of 5
	v.RequestedPct = pct(20)

	_, ok, reason := g().Validate(v)

	if ok {
		t.Fatal("scaling below conversion threshold must be refused")
	}
	if !strings.Contains(reason, "refusing to scale") {
		t.Errorf("reason = %q", reason)
	}
}

func TestGuardrails_InsufficientDataBlocksModification(t *testing.T) {

	for _, action := range []string{
		optimization.ActionIncreaseBudget,
		optimization.ActionDecreaseBudget,
		optimization.ActionPauseCampaign,
	} {
		v := vc(action)
		v.DaysInWindow = 3
		v.RequestedPct = pct(-10)

		if _, ok, reason := g().Validate(v); ok {
			t.Errorf("%s allowed with %d days of data", action, v.DaysInWindow)
		} else if want := "insufficient data"; len(reason) < len(want) || reason[:len(want)] != want {
			t.Errorf("reason = %q", reason)
		}
	}
}

func TestGuardrails_CooldownBlocksRepeatExecution(t *testing.T) {

	recent := time.Now().Add(-2 * time.Hour)

	v := vc(optimization.ActionPauseCampaign)
	v.LastExecutedAt = &recent

	_, ok, reason := g().Validate(v)

	if ok {
		t.Fatal("second execution inside cooldown must be refused")
	}
	if len(reason) < 8 || reason[:8] != "cooldown" {
		t.Errorf("reason = %q", reason)
	}
}

func TestGuardrails_CooldownExpires(t *testing.T) {

	old := time.Now().Add(-48 * time.Hour)

	v := vc(optimization.ActionPauseCampaign)
	v.LastExecutedAt = &old

	if _, ok, _ := g().Validate(v); !ok {
		t.Fatal("execution after cooldown window should pass")
	}
}

func TestGuardrails_PauseAlreadyPausedRejected(t *testing.T) {

	v := vc(optimization.ActionPauseCampaign)
	v.CurrentStatus = "PAUSED"

	_, ok, reason := g().Validate(v)

	if ok {
		t.Fatal("pausing a paused campaign must be refused")
	}
	if reason != "object is already paused" {
		t.Errorf("reason = %q", reason)
	}
}

func TestGuardrails_ResumeRequiresPausedState(t *testing.T) {

	active := vc(optimization.ActionResumeCampaign)
	if _, ok, _ := g().Validate(active); ok {
		t.Error("resuming an ACTIVE campaign must be refused")
	}

	paused := vc(optimization.ActionResumeCampaign)
	paused.CurrentStatus = "PAUSED"
	if _, ok, _ := g().Validate(paused); !ok {
		t.Error("resuming a PAUSED campaign should be allowed")
	}
}

func TestGuardrails_PauseAllowedWithSufficientData(t *testing.T) {

	v := vc(optimization.ActionPauseCampaign)

	if _, ok, reason := g().Validate(v); !ok {
		t.Errorf("valid pause rejected: %s", reason)
	}
}

func TestApplyBudgetPct_Math(t *testing.T) {

	cases := []struct {
		current float64
		pct     float64
		want    int64
	}{
		{10000, 20, 12000},
		{10000, -30, 7000},
		{5555, -20, 4444},
		{0, 20, 0},
	}

	for _, c := range cases {
		got := ApplyBudgetPct(c.current, c.pct)
		if got != c.want {
			t.Errorf("ApplyBudgetPct(%v, %v) = %d, want %d",
				c.current, c.pct, got, c.want)
		}
	}
}

func TestGuardrails_RefreshCreativeIsManual(t *testing.T) {

	v := vc(optimization.ActionRefreshCreative)

	_, ok, reason := g().Validate(v)

	if ok {
		t.Fatal("REFRESH_CREATIVE is not executable")
	}
	if !strings.Contains(reason, "manual action") {
		t.Errorf("reason = %q", reason)
	}
}
