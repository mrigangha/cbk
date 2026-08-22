package optimization

import (
	"testing"

	"github.com/mrigangha/cbk/internals/analytics"
)

func cpaTarget(v float64) *analytics.GoalSnapshot {
	g := &analytics.GoalSnapshot{Name: "G"}
	g.TargetCPA = &v
	return g
}

func TestEvaluate_IncreaseBudget_Success(t *testing.T) {

	before := analytics.Metrics{Spend: 1000, Clicks: 200, Conversions: 10, CPA: 100}
	after := analytics.Metrics{Spend: 1200, Clicks: 260, Conversions: 16, CPA: 75}

	ev := EvaluateAction(ActionIncreaseBudget, before, after, cpaTarget(80))

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s (%v)", ev.Result, ev.Reasons)
	}
}

func TestEvaluate_IncreaseBudget_FailsOnConversionsDrop(t *testing.T) {

	before := analytics.Metrics{Spend: 1000, Clicks: 200, Conversions: 10, CPA: 100}
	after := analytics.Metrics{Spend: 1250, Clicks: 240, Conversions: 6, CPA: 208}

	ev := EvaluateAction(ActionIncreaseBudget, before, after, cpaTarget(150))

	if ev.Result != VerdictFailure {
		t.Fatalf("result = %s", ev.Result)
	}
}

func TestEvaluate_DecreaseBudget_CPAImproved(t *testing.T) {

	before := analytics.Metrics{Spend: 2000, Clicks: 300, Conversions: 8, CPA: 250}
	after := analytics.Metrics{Spend: 1400, Clicks: 240, Conversions: 7, CPA: 200}

	ev := EvaluateAction(ActionDecreaseBudget, before, after, nil)

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s (%v)", ev.Result, ev.Reasons)
	}
	if ev.CPAChangePct == nil || *ev.CPAChangePct >= 0 {
		t.Errorf("cpa change = %v, want negative", ev.CPAChangePct)
	}
}

func TestEvaluate_DecreaseBudget_ResultsHeldOnLessSpend(t *testing.T) {

	before := analytics.Metrics{Spend: 3000, Impressions: 50000, Clicks: 800, Conversions: 20}
	after := analytics.Metrics{Spend: 2100, Impressions: 42000, Clicks: 690, Conversions: 19}

	ev := EvaluateAction(ActionDecreaseBudget, before, after, nil)

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s (%v)", ev.Result, ev.Reasons)
	}
}

func TestEvaluate_Pause_SuccessWhenSpendStops(t *testing.T) {

	before := analytics.Metrics{Spend: 900, Impressions: 40000, Conversions: 0}
	after := analytics.Metrics{}

	ev := EvaluateAction(ActionPauseCampaign, before, after, nil)

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s", ev.Result)
	}
}

func TestEvaluate_Pause_FailureWhenStillSpending(t *testing.T) {

	before := analytics.Metrics{Spend: 500}
	after := analytics.Metrics{Spend: 480, Impressions: 1000}

	ev := EvaluateAction(ActionPauseCampaign, before, after, nil)

	if ev.Result != VerdictFailure {
		t.Fatalf("result = %s — still spending after pause should fail", ev.Result)
	}
}

func TestEvaluate_RefreshCreative_Success(t *testing.T) {

	before := analytics.Metrics{Spend: 800, Impressions: 40000, Clicks: 320, Conversions: 12} // CTR .8, CPA 66
	after := analytics.Metrics{Spend: 800, Impressions: 40000, Clicks: 440, Conversions: 14}  // CTR 1.1, CPA 57

	ev := EvaluateAction(ActionRefreshCreative, before, after, nil)

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s (%v)", ev.Result, ev.Reasons)
	}
}

func TestEvaluate_Resume_SuccessWhenDelivering(t *testing.T) {

	before := analytics.Metrics{}
	after := analytics.Metrics{Impressions: 5200, Spend: 120, Clicks: 60}

	ev := EvaluateAction(ActionResumeCampaign, before, after, nil)

	if ev.Result != VerdictSuccess {
		t.Fatalf("result = %s", ev.Result)
	}
}

func TestEvaluate_InsufficientData(t *testing.T) {

	before := analytics.Metrics{Spend: 500, Impressions: 1000, Conversions: 2}
	after := analytics.Metrics{} // nothing delivered since

	ev := EvaluateAction(ActionIncreaseBudget, before, after, nil)

	if ev.Result != VerdictInsufficientData {
		t.Fatalf("result = %s, want INSUFFICIENT_DATA", ev.Result)
	}
}

func TestEvaluate_ChangePercentagesPopulated(t *testing.T) {

	before := analytics.Metrics{Spend: 1000, Clicks: 100, Impressions: 10000, Conversions: 10, ConversionValue: 2000}
	after := analytics.Metrics{Spend: 1100, Clicks: 120, Impressions: 10500, Conversions: 14, ConversionValue: 3080}

	ev := EvaluateAction(ActionIncreaseBudget, before, after, nil)

	checks := []struct {
		name string
		got  *float64
		want float64
	}{
		{"spend", ev.SpendChangePct, 10},
		{"ctr", ev.CTRChangePct, 14.29},
		{"conversions delta", ev.ConversionsDelta, 4},
	}

	for _, c := range checks {
		if c.got == nil {
			t.Errorf("%s change missing", c.name)
			continue
		}
		diff := *c.got - c.want
		if diff < -0.1 || diff > 0.1 {
			t.Errorf("%s change = %v, want ~%v", c.name, *c.got, c.want)
		}
	}
}
