package analytics

import (
	"fmt"
	"sort"
)

// ObjectAnalysis is the intelligence payload for a single campaign,
// ad set or ad.
type ObjectAnalysis struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status,omitempty"`
	Objective   string            `json:"objective,omitempty"`
	Metrics     Metrics           `json:"metrics"`
	Performance Performance       `json:"performance"`
	Health      Health            `json:"health"`
	Fatigue     *CreativeFatigue  `json:"creative_fatigue,omitempty"`
	Budget      *BudgetEfficiency `json:"budget_efficiency,omitempty"`

	Daily []TimePoint `json:"daily,omitempty"`
}

// Report is the full analytics payload for an endpoint or agent tool.
type Report struct {
	Scope      Scope            `json:"scope"`
	ObjectID   string           `json:"object_id,omitempty"`
	DatePreset string           `json:"date_preset"`
	DaysInWin  int              `json:"days_in_window,omitempty"`

	Totals        Metrics          `json:"totals"`
	Objects       []ObjectAnalysis `json:"objects,omitempty"`
	TrendDir      map[string]string `json:"trend_directions,omitempty"`
	Anomalies     []Anomaly        `json:"anomalies,omitempty"`
	GoalProgress  *GoalProgress    `json:"goal_progress,omitempty"`
}

// AnalyzeSet fetches metrics for each object, scores them and returns
// analyses sorted by spend (descending). Daily series are fetched when
// withDaily is true to power fatigue detection.
func AnalyzeSet(
	token string,
	scope Scope,
	objects []map[string]any,
	datePreset string,
	goal *GoalSnapshot,
	withDaily bool,
) ([]ObjectAnalysis, Metrics, error) {

	setTotals := Metrics{}
	out := make([]ObjectAnalysis, 0, len(objects))

	for _, obj := range objects {

		id, _ := obj["id"].(string)
		if id == "" {
			continue
		}

		metrics, err := FetchMetrics(token, id, scope, datePreset)
		if err != nil {
			return nil, Metrics{}, fmt.Errorf(
				"insights for %s failed: %w", id, err)
		}

		name, _ := obj["name"].(string)
		status, _ := obj["status"].(string)
		objective, _ := obj["objective"].(string)

		a := ObjectAnalysis{
			ID:        id,
			Name:      name,
			Status:    status,
			Objective: objective,
			Metrics:   metrics,
		}

		a.Performance = ScorePerformance(metrics, goal)
		a.Health = AssessHealth(metrics, status, a.Performance)

		if withDaily {
			days, derr := FetchDaily(token, id, scope, datePreset)
			if derr == nil && len(days) > 0 {
				a.Fatigue = ptr(AssessFatigue(days, metrics))
				a.Daily = days
			}
		}

		setTotals = Sum(append(setTotalsToList(setTotals), metrics))

		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Metrics.Spend > out[j].Metrics.Spend
	})

	return out, setTotals, nil
}

func setTotalsToList(t Metrics) []Metrics {
	return []Metrics{t}
}

func ptr[T any](v T) *T { return &v }

// AttachBudgetEfficiency fills the budget block for each analysis using
// the set totals for share calculations. dailyCaps maps object ID → cap.
func AttachBudgetEfficiency(
	items []ObjectAnalysis,
	setTotals Metrics,
	daysInWindow int,
	dailyCaps map[string]*float64,
	totalCap *float64,
) {

	for i := range items {

		var daily *float64
		if dailyCaps != nil {
			daily = dailyCaps[items[i].ID]
		}

		b := AssessBudgetEfficiency(
			items[i].Metrics,
			setTotals,
			daysInWindow,
			totalCap,
			daily,
		)

		items[i].Budget = &b
	}
}

// BuildReport produces the complete report for overview / campaigns /
// adsets / ads endpoints and the get_analytics tool.
func BuildReport(
	token,
	accountID, objectID string,
	scope Scope,
	datePreset string,
	goal *GoalSnapshot,
	withTrends bool,
) (*Report, error) {

	report := &Report{
		Scope:      scope,
		DatePreset: datePreset,
	}

	var objects []map[string]any

	switch scope {
	case ScopeAccount:
		report.ObjectID = accountID

		list, err := ListObjects(token, accountID, "campaigns")
		if err != nil {
			return nil, err
		}
		objects = list

	case ScopeCampaign:
		report.ObjectID = objectID

	case ScopeAdSet:
		report.ObjectID = objectID

	case ScopeAd:
		report.ObjectID = objectID
	}

	// Account scope aggregates at account level; child scopes analyze
	// the single object.
	if scope == ScopeAccount {

		totals, err := FetchMetrics(token, accountID, ScopeAccount, datePreset)
		if err != nil {
			return nil, err
		}
		report.Totals = totals

		analyses, _, err := AnalyzeSet(
			token, ScopeCampaign, objects, datePreset, goal, true,
		)
		if err != nil {
			return nil, err
		}

		AttachBudgetEfficiency(analyses, totals, presetDays(datePreset), nil, nil)

		report.Objects = analyses

	} else {

		metrics, err := FetchMetrics(token, objectID, scope, datePreset)
		if err != nil {
			return nil, err
		}
		report.Totals = metrics

		analysis := ObjectAnalysis{
			ID:      objectID,
			Metrics: metrics,
		}
		analysis.Performance = ScorePerformance(metrics, goal)
		analysis.Health = AssessHealth(metrics, "ACTIVE", analysis.Performance)

		days, derr := FetchDaily(token, objectID, scope, datePreset)
		if derr == nil && len(days) > 0 {
			analysis.Fatigue = ptr(AssessFatigue(days, metrics))
			analysis.Daily = days
			report.DaysInWin = len(days)
			report.TrendDir = TrendDirections(days)
		}

		report.Objects = []ObjectAnalysis{analysis}
	}

	// Trends + anomalies from account-level daily data.
	if withTrends || scope == ScopeAccount {

		days, err := FetchDaily(token, accountID, ScopeAccount, datePreset)
		if err == nil && len(days) > 0 {
			report.DaysInWin = len(days)
			report.TrendDir = TrendDirections(days)
			report.Anomalies = DetectAnomalies(days)
		}
	}

	// Goal progress against account totals in window.
	if goal != nil {
		gp := AssessGoalProgress(report.Totals, goal, presetDays(datePreset))
		report.GoalProgress = &gp
	}

	return report, nil
}

// Compare ranks objects against each other on normalized results and
// reports relative deltas versus the best performer.
type Comparison struct {
	Scope      Scope             `json:"scope"`
	DatePreset string            `json:"date_preset"`
	Ranked     []RankedObject    `json:"ranked"`
	Notes      []string          `json:"notes,omitempty"`
}

type RankedObject struct {
	ObjectAnalysis
	Rank            int     `json:"rank"`
	CPLDeltaVsBestPct float64 `json:"cpl_delta_vs_best_pct,omitempty"`
	CPADeltaVsBestPct float64 `json:"cpa_delta_vs_best_pct,omitempty"`
	SpendSharePct     float64 `json:"spend_share_pct,omitempty"`
	ResultSharePct    float64 `json:"result_share_pct,omitempty"`
	EfficiencyIndex   float64 `json:"efficiency_index,omitempty"`
	PrimaryMetric   string  `json:"primary_metric"`
	PrimaryValue    float64 `json:"primary_value"`
}

// enrichObject fills name/status/objective for an object from its
// parent listing (insights responses carry no metadata).
func (c *Comparison) enrich(token, accountID string, scope Scope) {

	kind := "campaigns"
	switch scope {
	case ScopeAdSet:
		kind = "adsets"
	case ScopeAd:
		kind = "ads"
	}

	list, err := ListObjects(token, accountID, kind)
	if err != nil {
		return
	}

	meta := map[string]map[string]any{}
	for _, obj := range list {
		if id, _ := obj["id"].(string); id != "" {
			meta[id] = obj
		}
	}

	for i := range c.Ranked {
		if m, ok := meta[c.Ranked[i].ID]; ok {
			c.Ranked[i].Name, _ = m["name"].(string)
			c.Ranked[i].Status, _ = m["status"].(string)
			c.Ranked[i].Objective, _ = m["objective"].(string)
		}
	}
}

// CompareObjects fetches and ranks a set of object IDs of one scope.
func CompareObjects(
	token string,
	accountID string,
	ids []string,
	scope Scope,
	datePreset string,
	goal *GoalSnapshot,
) (*Comparison, error) {

	cmp := &Comparison{
		Scope:      scope,
		DatePreset: datePreset,
	}

	analyses := make([]ObjectAnalysis, 0, len(ids))
	totals := Metrics{}

	for _, id := range ids {

		metrics, err := FetchMetrics(token, id, scope, datePreset)
		if err != nil {
			return nil, err
		}

		a := ObjectAnalysis{ID: id, Metrics: metrics}
		a.Performance = ScorePerformance(metrics, goal)

		analyses = append(analyses, a)
		totals = Sum([]Metrics{totals, metrics})
	}

	// Pick primary result metric by what the set actually optimizes for.
	primary, metricName := pickPrimaryMetric(totals)

	sort.SliceStable(analyses, func(i, j int) bool {
		return primary(analyses[i].Metrics) > primary(analyses[j].Metrics)
	})

	var bestCPL, bestCPA float64

	for _, a := range analyses {
		if a.Metrics.Leads > 0 && (bestCPL == 0 || a.Metrics.CPL < bestCPL) {
			bestCPL = a.Metrics.CPL
		}
		if a.Metrics.Conversions > 0 && (bestCPA == 0 || a.Metrics.CPA < bestCPA) {
			bestCPA = a.Metrics.CPA
		}
	}

	for i := range analyses {

		a := &analyses[i]

		r := RankedObject{
			ObjectAnalysis: *a,
			Rank:           i + 1,
			PrimaryMetric:  metricName,
			PrimaryValue:   round2(primary(a.Metrics)),
		}
		if totals.Spend > 0 {
			r.SpendSharePct = round1(a.Metrics.Spend / totals.Spend * 100)
		}
		if totals.Conversions > 0 {
			r.ResultSharePct = round1(a.Metrics.Conversions / totals.Conversions * 100)
			if r.SpendSharePct > 0 {
				r.EfficiencyIndex = round2(r.ResultSharePct / r.SpendSharePct)
			}
		}

		if bestCPL > 0 && a.Metrics.Leads > 0 {
			r.CPLDeltaVsBestPct = round1((a.Metrics.CPL - bestCPL) / bestCPL * 100)
		}
		if bestCPA > 0 && a.Metrics.Conversions > 0 {
			r.CPADeltaVsBestPct = round1((a.Metrics.CPA - bestCPA) / bestCPA * 100)
		}

		cmp.Ranked = append(cmp.Ranked, r)
	}

	cmp.enrich(token, accountID, scope)

	return cmp, nil
}

func pickPrimaryMetric(totals Metrics) (func(Metrics) float64, string) {

	if totals.Purchases > 0 {
		return func(m Metrics) float64 { return m.ConversionValue }, "conversion_value"
	}
	if totals.Leads > 0 {
		return func(m Metrics) float64 { return m.Leads }, "leads"
	}
	if totals.Conversions > 0 {
		return func(m Metrics) float64 { return m.Conversions }, "conversions"
	}
	return func(m Metrics) float64 { return m.Clicks }, "clicks"
}

// presetDays approximates the number of days in a date_preset window,
// used for daily-spend pacing math.
func presetDays(preset string) int {

	switch preset {
	case "today", "yesterday":
		return 1
	case "last_7d", "last_7_days":
		return 7
	case "last_14d":
		return 14
	case "this_month", "last_month":
		return 30
	default:
		return 30
	}
}
