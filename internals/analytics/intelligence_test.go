package analytics

import (
	"math"
	"testing"
)

func TestNormalizeInsight_FullRow(t *testing.T) {

	row := map[string]any{
		"spend":       "300.00",
		"impressions": "10000",
		"reach":       "4000",
		"clicks":      "120",
		"ctr":         "1.2",
		"cpc":         "2.5",
		"cpm":         "30.0",
		"frequency":   "2.5",
		"actions": []any{
			map[string]any{"action_type": "offsite_conversion.fb_pixel_lead", "value": "10"},
			map[string]any{"action_type": "link_click", "value": "118"},
			map[string]any{"action_type": "offsite_conversion.fb_pixel_purchase", "value": "4"},
		},
		"purchase_roas": []any{
			map[string]any{"action_type": "purchase", "roas": "2.5"},
		},
	}

	m := NormalizeInsight(row)

	if m.Spend != 300 || m.Impressions != 10000 || m.Clicks != 120 {
		t.Fatalf("base metrics wrong: %+v", m)
	}

	if m.Leads != 10 {
		t.Errorf("leads = %v, want 10", m.Leads)
	}
	if m.Purchases != 4 {
		t.Errorf("purchases = %v, want 4", m.Purchases)
	}
	if m.Conversions != 4 {
		t.Errorf("conversions = %v, want purchases (4)", m.Conversions)
	}
	if m.CVR != 4.0/120*100 {
		t.Errorf("cvr = %v", m.CVR)
	}
	if m.CPL != 30 {
		t.Errorf("cpl = %v, want 30", m.CPL)
	}
	if m.CPA != 75 {
		t.Errorf("cpa = %v, want 75", m.CPA)
	}
	if m.ROAS != 2.5 {
		t.Errorf("roas = %v, want 2.5", m.ROAS)
	}
}

func TestNormalizeInsight_LeadOnly(t *testing.T) {

	row := map[string]any{
		"spend":  "150",
		"clicks": "50",
		"actions": []any{
			map[string]any{"action_type": "leadgen_other", "value": "3"},
		},
	}

	m := NormalizeInsight(row)

	if m.Leads != 3 || m.Conversions != 3 {
		t.Fatalf("lead classification wrong: %+v", m)
	}

	if m.CPL != 50 {
		t.Errorf("cpl = %v, want 50", m.CPL)
	}
	if m.CPA != 50 {
		t.Errorf("cpa = %v, want 50 (conversions=leads)", m.CPA)
	}
	if math.Abs(m.CVR-6) > 0.001 {
		t.Errorf("cvr = %v, want 6", m.CVR)
	}
}

func TestScorePerformance_GoalCPLPenalty(t *testing.T) {

	cplTarget := 300.0

	goal := &GoalSnapshot{Name: "Leads", TargetCPL: &cplTarget}

	good := Metrics{Spend: 3000, Clicks: 500, Impressions: 20000, Leads: 12}
	bad := Metrics{Spend: 3000, Clicks: 500, Impressions: 20000, Leads: 4}

	pg := ScorePerformance(good, goal)
	pb := ScorePerformance(bad, goal)

	if pg.Score <= pb.Score {
		t.Fatalf("good CPL (%d) should outscore bad CPL (%d)", pg.Score, pb.Score)
	}

	found := false
	for _, r := range pb.Reasons {
		if len(r) > 8 && r[:4] == "CPL " {
			found = true
		}
	}
	if !found {
		t.Error("expected a CPL reason on the scored analysis")
	}
}

func TestAssessHealth_States(t *testing.T) {

	delivering := Metrics{Impressions: 100, Spend: 10, CTR: 2}
	notDelivering := Metrics{}

	if h := AssessHealth(delivering, "ACTIVE", Performance{Score: 80}); h.Status != "HEALTHY" {
		t.Errorf("healthy case got %q", h.Status)
	}
	if h := AssessHealth(notDelivering, "ACTIVE", Performance{}); h.Status != "NOT_DELIVERING" {
		t.Errorf("zero impressions got %q", h.Status)
	}
	if h := AssessHealth(delivering, "PAUSED", Performance{}); h.Status != "PAUSED" {
		t.Errorf("paused got %q", h.Status)
	}
	if h := AssessHealth(delivering, "ACTIVE", Performance{Score: 20}); h.Status != "CRITICAL" {
		t.Errorf("low score got %q", h.Status)
	}
}

func TestDetectAnomalies_SpendSpike(t *testing.T) {

	days := make([]TimePoint, 0, 14)

	for i := 1; i <= 13; i++ {
		days = append(days, TimePoint{
			Date:    sprintDay(i),
			Metrics: Metrics{Spend: 100, Clicks: 20, Impressions: 2000},
		})
	}

	// Day 14 spends 6x — must be flagged.
	days = append(days, TimePoint{
		Date:    sprintDay(14),
		Metrics: Metrics{Spend: 600, Clicks: 25, Impressions: 2200},
	})

	anomalies := DetectAnomalies(days)

	found := false
	for _, a := range anomalies {
		if a.Metric == "spend" && a.Value == 600 && a.Direction == "spike" {
			found = true
		}
	}
	if !found {
		t.Fatalf("spend spike not detected: %+v", anomalies)
	}
}

func TestDetectAnomalies_QuietWindow(t *testing.T) {

	var days []TimePoint
	for i := 1; i <= 10; i++ {
		days = append(days, TimePoint{
			Date:    sprintDay(i),
			Metrics: Metrics{Spend: 100},
		})
	}

	if anomalies := DetectAnomalies(days); len(anomalies) != 0 {
		t.Errorf("flat window produced anomalies: %+v", anomalies)
	}
}

func TestGoalProgress_CPLAboveTarget(t *testing.T) {

	target := 300.0
	goal := &GoalSnapshot{Name: "Bangalore Leads", TargetCPL: &target}

	// CPL 450 = 50% worse than target → off_track.
	m := Metrics{Spend: 900, Leads: 2} // cpl 450

	gp := AssessGoalProgress(m, goal, 15)

	if gp.Status != "off_track" && gp.Status != "at_risk" {
		t.Errorf("status = %q for 50%% over target CPL", gp.Status)
	}

	if gp.CPLDeltaPct != 50 {
		t.Errorf("cpl delta = %v, want 50", gp.CPLDeltaPct)
	}
}

func TestTrendDirections(t *testing.T) {

	var days []TimePoint

	for i := 1; i <= 10; i++ {
		spend := 50.0
		if i > 5 {
			spend = 150 // second half doubles+ down
		}
		days = append(days, TimePoint{
			Date:    sprintDay(i),
			Metrics: Metrics{Spend: spend, Impressions: 100, Clicks: 5},
		})
	}

	dir := TrendDirections(days)

	if dir["spend"] != "rising" {
		t.Errorf("spend direction = %q, want rising", dir["spend"])
	}
}

func TestFatigue_InsufficientData(t *testing.T) {

	f := AssessFatigue([]TimePoint{{Date: "2026-08-01"}}, Metrics{})
	if f.Level != "insufficient_data" {
		t.Errorf("level = %q", f.Level)
	}
}

func sprintDay(n int) string {
	return "2026-08-" + string(rune('0'+n/10)) + string(rune('0'+n%10))
}
