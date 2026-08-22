package analytics

import (
	"fmt"
	"math"
	"sort"
)

// ===========================
// GOAL SNAPSHOT
// ===========================

// GoalSnapshot is a neutral view of a marketing goal so this package
// never imports the core package.
type GoalSnapshot struct {
	Name      string
	Objective string

	TargetCPL         *float64
	TargetCPA         *float64
	TargetROAS        *float64
	TargetConversions *float64

	Budget      *float64
	DailyBudget *float64
}

// ===========================
// PERFORMANCE SCORE
// ===========================

type Performance struct {
	Score   int      `json:"score"` // 0–100
	Grade   string   `json:"grade"` // excellent|good|warning|poor
	Reasons []string `json:"reasons,omitempty"`
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func ScorePerformance(mRaw Metrics, goal *GoalSnapshot) Performance {

	m := DeriveRatios(mRaw)

	p := Performance{Score: 50}

	addReason := func(format string, args ...any) {
		p.Reasons = append(p.Reasons, fmt.Sprintf(format, args...))
	}

	// CTR against a 1% benchmark (±15).
	if m.Impressions > 0 && m.Clicks > 0 {
		ratio := m.CTR / 1.0
		delta := clamp((ratio-1)*15, -15, 15)
		p.Score += int(delta)
		addReason("CTR %.2f%% (%+.0f pts vs 1%% benchmark)", m.CTR, delta)
	}

	// CPC against 1.0 (±10, lower is better).
	if m.Clicks > 0 {
		ratio := m.CPC / 1.0
		delta := clamp((1-ratio)*10, -10, 10)
		p.Score += int(delta)
		addReason("CPC %.2f (%+.0f pts)", m.CPC, delta)
	}

	// CVR against 2% (±10).
	if m.Clicks > 0 && m.Conversions > 0 {
		ratio := m.CVR / 2.0
		delta := clamp((ratio-1)*10, -10, 10)
		p.Score += int(delta)
		addReason("CVR %.2f%% (%+.0f pts vs 2%% benchmark)", m.CVR, delta)
	}

	// CPL against the goal target (±20).
	if goal != nil && goal.TargetCPL != nil && m.Leads > 0 {
		target := *goal.TargetCPL
		ratio := m.CPL / target
		delta := clamp((1-ratio)*20, -20, 20)
		p.Score += int(delta)
		addReason(
			"CPL %.2f is %+.0f%% vs target %.2f",
			m.CPL, (ratio-1)*100, target,
		)
	}

	// ROAS against target (default 2.0, ±20).
	if m.Spend > 0 && m.ConversionValue > 0 {
		target := 2.0
		if goal != nil && goal.TargetROAS != nil {
			target = *goal.TargetROAS
		}
		ratio := m.ROAS / target
		delta := clamp((ratio-1)*20, -20, 20)
		p.Score += int(delta)
		addReason("ROAS %.2f vs target %.2f (%+.0f pts)", m.ROAS, target, delta)
	}

	// Spending but nothing to show for it.
	if m.Spend > 0 && m.Conversions == 0 {
		p.Score -= 10
		addReason("no conversions recorded despite %.2f spend", m.Spend)
	}

	p.Score = int(clamp(float64(p.Score), 0, 100))

	switch {
	case p.Score >= 80:
		p.Grade = "excellent"
	case p.Score >= 60:
		p.Grade = "good"
	case p.Score >= 40:
		p.Grade = "warning"
	default:
		p.Grade = "poor"
	}

	return p
}

// ===========================
// HEALTH
// ===========================

type Health struct {
	Status string   `json:"status"` // HEALTHY|WATCH|CRITICAL|NOT_DELIVERING|PAUSED
	Notes  []string `json:"notes,omitempty"`
}

func AssessHealth(mRaw Metrics, objectStatus string, perf Performance) Health {

	m := DeriveRatios(mRaw)

	h := Health{}

	switch objectStatus {
	case "PAUSED", "ARCHIVED", "DELETED":
		h.Status = "PAUSED"
		return h
	}

	if m.Impressions == 0 {
		h.Status = "NOT_DELIVERING"
		h.Notes = append(h.Notes,
			"active but zero impressions in window")
		return h
	}

	switch {
	case perf.Score >= 70:
		h.Status = "HEALTHY"
	case perf.Score >= 45:
		h.Status = "WATCH"
	default:
		h.Status = "CRITICAL"
	}

	if m.Frequency >= 4 {
		h.Notes = append(h.Notes,
			fmt.Sprintf("frequency %.1f is high", m.Frequency))
	}
	if m.CTR > 0 && m.CTR < 0.5 {
		h.Notes = append(h.Notes,
			fmt.Sprintf("CTR %.2f%% is very low", m.CTR))
	}

	return h
}

// ===========================
// BUDGET EFFICIENCY
// ===========================

type BudgetEfficiency struct {
	SpendSharePct      float64 `json:"spend_share_pct,omitempty"`
	ConversionSharePct float64 `json:"conversion_share_pct,omitempty"`
	EfficiencyIndex    float64 `json:"efficiency_index,omitempty"` // result share ÷ spend share
	DailySpend         float64 `json:"daily_spend,omitempty"`
	DailyBudget        float64 `json:"daily_budget,omitempty"`
	PacingPct          float64 `json:"pacing_pct,omitempty"`
	Note               string  `json:"note,omitempty"`
}

// AssessBudgetEfficiency positions one object inside a set (its share of
// spend vs its share of results) and optionally paces against a cap.
// daysInWindow is used to derive daily spend.
func AssessBudgetEfficiency(
	m Metrics,
	setTotals Metrics,
	daysInWindow int,
	capBudget *float64,
	capDaily *float64,
) BudgetEfficiency {

	be := BudgetEfficiency{}

	if setTotals.Spend > 0 && m.Spend > 0 {
		be.SpendSharePct = round1(m.Spend / setTotals.Spend * 100)
		be.ConversionSharePct = round1(m.Conversions / maxF(setTotals.Conversions, 1e-9) * 100)
		if be.SpendSharePct > 0 {
			be.EfficiencyIndex = round2(be.ConversionSharePct / be.SpendSharePct)
			if be.EfficiencyIndex < 0.7 {
				be.Note = fmt.Sprintf(
					"consumes %.0f%% of spend for only %.0f%% of results",
					be.SpendSharePct, be.ConversionSharePct)
			}
		}
	}

	if daysInWindow > 0 {
		be.DailySpend = round2(m.Spend / float64(daysInWindow))
	}

	if capDaily != nil && *capDaily > 0 {
		be.DailyBudget = *capDaily
		if be.DailySpend > 0 {
			be.PacingPct = round1(be.DailySpend / *capDaily * 100)
			if be.PacingPct > 100 {
				be.Note = joinNote(be.Note, "pacing over the daily cap")
			}
		}
	}

	if capBudget != nil && *capBudget > 0 {
		used := m.Spend / *capBudget * 100
		be.Note = joinNote(be.Note,
			fmt.Sprintf("%.0f%% of total budget used", used))
	}

	return be
}

// ===========================
// CREATIVE FATIGUE
// ===========================

type CreativeFatigue struct {
	Frequency          float64 `json:"frequency"`
	CTRChangePct       float64 `json:"ctr_change_pct"`
	FrequencyChangePct float64 `json:"frequency_change_pct"`
	Score              int     `json:"score"` // 0–100 severity
	Level              string  `json:"level"` // insufficient_data|none|mild|moderate|high
	Note               string  `json:"note,omitempty"`
}

// AssessFatigue compares the last 7 days against the previous 7.
func AssessFatigue(days []TimePoint, current Metrics) CreativeFatigue {

	f := CreativeFatigue{Frequency: round2(current.Frequency)}

	if len(days) < 8 {
		f.Level = "insufficient_data"
		return f
	}

	sort.Slice(days, func(i, j int) bool {
		return days[i].Date < days[j].Date
	})

	last := days[max(0, len(days)-7):]
	prev := days[max(0, len(days)-14):max(0, len(days)-7)]

	lastCTR := avgTimePoint(last, func(t TimePoint) float64 { return t.Metrics.CTR })
	prevCTR := avgTimePoint(prev, func(t TimePoint) float64 { return t.Metrics.CTR })
	lastFreq := avgTimePoint(last, func(t TimePoint) float64 { return t.Metrics.Frequency })
	prevFreq := avgTimePoint(prev, func(t TimePoint) float64 { return t.Metrics.Frequency })

	if prevCTR > 0 {
		f.CTRChangePct = round1((lastCTR - prevCTR) / prevCTR * 100)
	}
	if prevFreq > 0 {
		f.FrequencyChangePct = round1((lastFreq - prevFreq) / prevFreq * 100)
	}

	// Severity: CTR decay weighted heavier than frequency rise.
	score := 0.0
	if f.CTRChangePct < 0 {
		score += math.Min(-f.CTRChangePct, 60)
	}
	if f.FrequencyChangePct > 0 {
		score += math.Min(f.FrequencyChangePct, 40)
	}
	if current.Frequency >= 3.5 {
		score += 10
	}

	f.Score = int(clamp(score, 0, 100))

	switch {
	case f.Score >= 60:
		f.Level = "high"
		f.Note = "CTR falling sharply while frequency rises — refresh creatives"
	case f.Score >= 35:
		f.Level = "moderate"
		f.Note = "early fatigue signals"
	case f.Score >= 15:
		f.Level = "mild"
	default:
		f.Level = "none"
	}

	return f
}

// ===========================
// ANOMALIES
// ===========================

const anomalyZThreshold = 2.0

type Anomaly struct {
	Date      string  `json:"date"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Mean      float64 `json:"mean"`
	ZScore    float64 `json:"z_score"`
	Direction string  `json:"direction"` // spike|drop
}

var anomalyMetrics = []string{"spend", "ctr", "cpc", "cpa"}

// DetectAnomalies flags days where a key metric moves ≥2σ from its mean.
func DetectAnomalies(days []TimePoint) []Anomaly {

	found := []Anomaly{}

	if len(days) < 5 {
		return found
	}

	pick := func(m Metrics, name string) float64 {
		switch name {
		case "spend":
			return m.Spend
		case "ctr":
			return m.CTR
		case "cpc":
			return m.CPC
		case "cpa":
			return m.CPA
		}
		return 0
	}

	for _, metric := range anomalyMetrics {

		vals := make([]float64, 0, len(days))

		for _, day := range days {
			v := pick(day.Metrics, metric)
			if v == 0 {
				continue // skip empty days (paused etc.)
			}
			vals = append(vals, v)
		}

		if len(vals) < 5 {
			continue
		}

		mean, std := meanStd(vals)
		if std == 0 {
			continue
		}

		for _, day := range days {

			v := pick(day.Metrics, metric)
			if v == 0 {
				continue
			}

			z := (v - mean) / std

			if math.Abs(z) >= anomalyZThreshold {
				direction := "spike"
				if z < 0 {
					direction = "drop"
				}

				found = append(found, Anomaly{
					Date:      day.Date,
					Metric:    metric,
					Value:     round2(v),
					Mean:      round2(mean),
					ZScore:    round2(z),
					Direction: direction,
				})
			}
		}
	}

	sort.Slice(found, func(i, j int) bool {
		return math.Abs(found[i].ZScore) > math.Abs(found[j].ZScore)
	})

	return found
}

// ===========================
// GOAL PROGRESS
// ===========================

type GoalProgress struct {
	GoalName               string  `json:"goal_name"`
	Status                 string  `json:"status"` // on_track|at_risk|off_track|insufficient_data
	ConversionsTarget      float64 `json:"-"`
	ConversionsProgressPct float64 `json:"conversions_progress_pct,omitempty"`
	CPLDeltaPct            float64 `json:"cpl_delta_pct,omitempty"` // positive = worse than target
	CPADeltaPct            float64 `json:"cpa_delta_pct,omitempty"`
	ROASRatio              float64 `json:"roas_ratio,omitempty"` // current ÷ target
	BudgetUsedPct          float64 `json:"budget_used_pct,omitempty"`
	PacingVsDailyPct       float64 `json:"pacing_vs_daily_pct,omitempty"`
	Summary                string  `json:"summary"`
}

func AssessGoalProgress(
	mRaw Metrics,
	goal *GoalSnapshot,
	daysInWindow int,
) GoalProgress {

	m := DeriveRatios(mRaw)

	gp := GoalProgress{GoalName: goal.Name}

	factors := 0
	penalty := 0.0

	if goal.TargetConversions != nil && *goal.TargetConversions > 0 {
		gp.ConversionsTarget = *goal.TargetConversions
		gp.ConversionsProgressPct = round1(m.Conversions / *goal.TargetConversions * 100)

		expected := expectedProgress(daysInWindow)
		if gp.ConversionsProgressPct+1e-9 < expected*100*0.6 {
			penalty++
		}
		gp.Summary += fmt.Sprintf(
			"%.0f%% of the conversion target reached (%.0f/%.0f). ",
			gp.ConversionsProgressPct, m.Conversions, *goal.TargetConversions)
		factors++
	}

	if goal.TargetCPL != nil && *goal.TargetCPL > 0 && m.Leads > 0 {
		gp.CPLDeltaPct = round1((m.CPL - *goal.TargetCPL) / *goal.TargetCPL * 100)
		if gp.CPLDeltaPct > 25 {
			penalty++
		}
		gp.Summary += fmt.Sprintf(
			"CPL is %+.0f%% vs the %.2f target (%.2f actual). ",
			gp.CPLDeltaPct, *goal.TargetCPL, m.CPL)
		factors++
	}

	if goal.TargetCPA != nil && *goal.TargetCPA > 0 && m.Conversions > 0 {
		gp.CPADeltaPct = round1((m.CPA - *goal.TargetCPA) / *goal.TargetCPA * 100)
		if gp.CPADeltaPct > 25 {
			penalty++
		}
		gp.Summary += fmt.Sprintf(
			"CPA is %+.0f%% vs target (%.2f actual). ",
			gp.CPADeltaPct, m.CPA)
		factors++
	}

	if goal.TargetROAS != nil && *goal.TargetROAS > 0 && m.Spend > 0 {
		gp.ROASRatio = round2(m.ROAS / *goal.TargetROAS)
		if gp.ROASRatio < 0.8 {
			penalty++
		}
		gp.Summary += fmt.Sprintf(
			"ROAS at %.0f%% of target (%.2f vs %.2f). ",
			gp.ROASRatio*100, m.ROAS, *goal.TargetROAS)
		factors++
	}

	if goal.Budget != nil && *goal.Budget > 0 {
		gp.BudgetUsedPct = round1(m.Spend / *goal.Budget * 100)
		gp.Summary += fmt.Sprintf("%.0f%% of budget spent. ", gp.BudgetUsedPct)
	}

	if goal.DailyBudget != nil && *goal.DailyBudget > 0 && daysInWindow > 0 {
		daily := m.Spend / float64(daysInWindow)
		gp.PacingVsDailyPct = round1(daily / *goal.DailyBudget * 100)
		gp.Summary += fmt.Sprintf(
			"Pacing at %.0f%% of the daily budget. ",
			gp.PacingVsDailyPct)
	}

	switch {
	case factors == 0:
		gp.Status = "insufficient_data"
		gp.Summary = "No comparable targets present in the goal or no data in window."
	case penalty >= 2 || (penalty == 1 && factors == 1):
		gp.Status = "off_track"
	case penalty == 1:
		gp.Status = "at_risk"
	default:
		gp.Status = "on_track"
	}

	return gp
}

// ===========================
// TREND DIRECTIONS
// ===========================

// TrendDirection compares second-half averages to first-half averages
// per key metric across the window.
func TrendDirections(days []TimePoint) map[string]string {

	out := map[string]string{}

	if len(days) < 4 {
		return out
	}

	sorted := append([]TimePoint{}, days...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Date < sorted[j].Date
	})

	half := len(sorted) / 2
	first, second := sorted[:half], sorted[half:]

	type pickFn struct {
		name    string
		fn      func(Metrics) float64
		inverse bool // falling is good
	}

	picks := []pickFn{
		{"spend", func(m Metrics) float64 { return m.Spend }, false},
		{"impressions", func(m Metrics) float64 { return m.Impressions }, false},
		{"clicks", func(m Metrics) float64 { return m.Clicks }, false},
		{"ctr", func(m Metrics) float64 { return m.CTR }, false},
		{"cpc", func(m Metrics) float64 { return m.CPC }, true},
		{"cpa", func(m Metrics) float64 { return m.CPA }, true},
		{"conversions", func(m Metrics) float64 { return m.Conversions }, false},
		{"roas", func(m Metrics) float64 { return m.ROAS }, false},
		{"frequency", func(m Metrics) float64 { return m.Frequency }, true},
	}

	for _, p := range picks {

		a := avgTimePoint(first, func(t TimePoint) float64 { return p.fn(t.Metrics) })
		b := avgTimePoint(second, func(t TimePoint) float64 { return p.fn(t.Metrics) })

		if a == 0 && b == 0 {
			continue
		}

		change := 0.0
		if a > 0 {
			change = (b - a) / a * 100
		} else {
			change = 100
		}

		switch {
		case change > 10:
			out[p.name] = "rising"
		case change < -10:
			out[p.name] = "falling"
		default:
			out[p.name] = "flat"
		}
	}

	return out
}

// ===========================
// HELPERS
// ===========================

func avgTimePoint(days []TimePoint, fn func(TimePoint) float64) float64 {
	var sum float64
	var n int
	for _, d := range days {
		v := fn(d)
		sum += v
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func meanStd(vals []float64) (float64, float64) {

	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))

	var variance float64
	for _, v := range vals {
		variance += (v - mean) * (v - mean)
	}
	variance /= float64(len(vals))

	return mean, math.Sqrt(variance)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func joinNote(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "; " + add
}

// expectedProgress returns the fraction of a 30-day target that should
// be complete after daysInWindow days (clamped to the window length).
func expectedProgress(daysInWindow int) float64 {
	if daysInWindow <= 0 {
		return 0
	}
	d := float64(min(daysInWindow, 30))
	return d / 30
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
