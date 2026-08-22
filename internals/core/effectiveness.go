package core

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/mrigangha/cbk/internals/analytics"
	"github.com/mrigangha/cbk/internals/optimization"
)

// ===========================
// DECISION → OUTCOME ATTRIBUTION
// ===========================
//
// The full chain this module stitches together:
//
//	Decision (optimization_actions: rule, reason, confidence)
//	  → Action (status EXECUTED, executed_at)
//	    → Before (before_metrics snapshot at execution time)
//	      → After (optimization_outcomes.latest window metrics)
//	        → Verdict (evaluation SUCCESS/FAILURE/...)
//	          → Effectiveness (this file)

// attributedAction is one fully-linked proposal: its decision context,
// its before snapshot, and its latest measured after-window.
type attributedAction struct {
	Action      string
	DatePreset  string
	Scope       analytics.Scope
	ExecutedAt  time.Time
	WindowDays  int
	DailyBefore float64 // spend/day over the decision window
	DailyAfter  float64 // spend/day over the outcome window

	Before analytics.Metrics
	After  analytics.Metrics

	Verdict optimization.OutcomeVerdict
}

// loadAttributedActions joins every EXECUTED proposal inside the window
// with its before snapshot and latest captured outcome.
func (a *Api) loadAttributedActions(
	userID int64,
	sinceDays int,
) []attributedAction {

	cutoff := time.Now().UTC().AddDate(0, 0, -sinceDays)

	rows, err := a.db.Query(`
		SELECT oa.action, COALESCE(oa.date_preset, ''),
		       CAST(oa.executed_at AS TEXT),
		       COALESCE(oa.before_metrics, ''),
		       COALESCE(o.metrics, ''),
		       COALESCE(o.evaluation, ''),
		       o.window_days
		FROM optimization_actions oa
		JOIN optimization_outcomes o ON o.action_id = oa.id
		WHERE oa.user_id = ? AND oa.status = 'EXECUTED'
		  AND o.window_days = (
		      SELECT MAX(window_days) FROM optimization_outcomes x
		      WHERE x.action_id = oa.id
		  )
		  AND CAST(oa.executed_at AS TEXT) >= ?
	`, userID, cutoff.Format("2006-01-02"))
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []attributedAction{}

	for rows.Next() {

		var (
			row       attributedAction
			rawExec   string
			beforeRaw []byte
			afterRaw  []byte
			evalRaw   []byte
		)

		if err := rows.Scan(&row.Action, &row.DatePreset, &rawExec,
			&beforeRaw, &afterRaw, &evalRaw, &row.WindowDays); err != nil {
			continue
		}

		execAt, err := time.Parse("2006-01-02 15:04:05", rawExec)
		if err != nil {
			continue
		}
		row.ExecutedAt = execAt.UTC()
		row.Scope = scopeForObjectType("CAMPAIGN")

		if json.Unmarshal(beforeRaw, &row.Before) != nil ||
			json.Unmarshal(afterRaw, &row.After) != nil {
			continue
		}

		row.Before = analytics.DeriveRatios(row.Before)
		row.After = analytics.DeriveRatios(row.After)

		var ev optimization.Evaluation
		if json.Unmarshal(evalRaw, &ev) == nil {
			row.Verdict = ev.Result
		}

		days := presetDaysInt(row.DatePreset)
		if days <= 0 {
			days = 30
		}
		if row.WindowDays <= 0 {
			continue
		}

		row.DailyBefore = row.Before.Spend / float64(days)
		row.DailyAfter = row.After.Spend / float64(row.WindowDays)

		out = append(out, row)
	}

	return out
}

// Effectiveness is the answer to "is our AI actually good at this?".
type Effectiveness struct {
	WindowDays int `json:"window_days"`

	Proposed  int `json:"actions_proposed"`
	Executed  int `json:"actions_executed"`
	Failed    int `json:"actions_failed"`
	Evaluated int `json:"actions_evaluated"`

	Successful  int     `json:"verdict_success"`
	Failure     int     `json:"verdict_failure"`
	Neutral     int     `json:"verdict_neutral"`
	NoData      int     `json:"verdict_insufficient_data"`
	SuccessRate float64 `json:"success_rate"`

	// Negative CPA change = cheaper acquisitions (improvement).
	CPAImprovementPct *float64 `json:"cpa_improvement_pct"`
	// Positive ROAS change = more value per unit spend (improvement).
	ROASImprovementPct *float64 `json:"roas_improvement_pct"`

	ConversionsDelta float64 `json:"conversions_delta"`

	// Cumulative dollars of spend removed by pauses/budget cuts,
	// accrued daily since each execution (capped at the window).
	WasteReduction float64 `json:"waste_reduction"`

	// 0-100 composite. Transparent formula:
	//   weighted mean of success-rate subscore (0.5),
	//   CPA subscore (0.25) and ROAS subscore (0.25);
	//   a metric subscore maps [-50%, +50%] → [0, 100] (flat = 50).
	// Missing dimensions redistribute their weight.
	Score      *float64 `json:"score"`
	ScoreBasis string   `json:"score_basis,omitempty"`
}

func (a *Api) GetOptimizationEffectiveness(
	w http.ResponseWriter,
	r *http.Request,
) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	window := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= 365 {
			window = n
		}
	}

	eff := Effectiveness{WindowDays: window}
	cutoff := time.Now().UTC().AddDate(0, 0, -window)

	// Proposed + failed within the window.
	a.db.QueryRow(`
		SELECT
		    COALESCE(SUM(CASE WHEN status = 'PENDING' THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN status = 'EXECUTED' THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN status = 'FAILED' THEN 1 ELSE 0 END), 0)
		FROM optimization_actions
		WHERE user_id = ? AND created_at >= ?
	`, user.ID, cutoff.Format("2006-01-02")).Scan(
		&eff.Proposed, &eff.Executed, &eff.Failed)

	attributed := a.loadAttributedActions(user.ID, window)

	var (
		cpaSum, cpaWeight   float64
		roasSum, roasWeight float64
	)

	for _, act := range attributed {

		eff.Evaluated++
		switch act.Verdict {
		case optimization.VerdictSuccess:
			eff.Successful++
		case optimization.VerdictFailure:
			eff.Failure++
		case optimization.VerdictNeutral:
			eff.Neutral++
		default:
			eff.NoData++
		}

		// CPA direction: negative is better. Weighted by pre-spend so
		// big-budget decisions dominate the account-level number.
		if act.Before.CPA > 0 && act.After.CPA > 0 {
			change := (act.After.CPA - act.Before.CPA) /
				act.Before.CPA * 100
			w := math.Max(act.Before.Spend, 1)
			cpaSum += change * w
			cpaWeight += w
		}

		if act.Before.ROAS > 0 && act.After.ROAS > 0 {
			change := (act.After.ROAS - act.Before.ROAS) /
				act.Before.ROAS * 100
			w := math.Max(act.Before.Spend, 1)
			roasSum += change * w
			roasWeight += w
		}

		eff.ConversionsDelta += act.After.Conversions -
			act.Before.Conversions

		// Waste accrues only from cuts and pauses, only while they hold.
		switch act.Action {
		case optimization.ActionDecreaseBudget,
			optimization.ActionPauseCampaign,
			optimization.ActionPauseAdSet,
			optimization.ActionPauseAd:

			savedPerDay := act.DailyBefore - act.DailyAfter
			if savedPerDay > 0 && act.Verdict != optimization.VerdictFailure {
				daysSince := int(time.Since(act.ExecutedAt).Hours() / 24)
				if daysSince > window {
					daysSince = window
				}
				accrued := savedPerDay * float64(daysSince+1)
				eff.WasteReduction += accrued
			}
		}
	}

	if eff.Successful+eff.Failure > 0 {
		eff.SuccessRate = roundMoney(
			float64(eff.Successful) /
				float64(eff.Successful+eff.Failure) * 100)
	}

	if cpaWeight > 0 {
		v := roundMoney(cpaSum / cpaWeight)
		eff.CPAImprovementPct = &v
	}
	if roasWeight > 0 {
		v := roundMoney(roasSum / roasWeight)
		eff.ROASImprovementPct = &v
	}

	eff.WasteReduction = roundMoney(eff.WasteReduction)

	// ---- Composite score ----
	if eff.Evaluated > 0 {

		subscore := func(p float64) float64 {
			if p < -50 {
				p = -50
			}
			if p > 50 {
				p = 50
			}
			return p + 50 // [-50,+50]% → [0,100]
		}

		total := eff.SuccessRate * 0.5 // success dimension, weighted
		weight := 0.5

		addDimension := func(sub, w float64) {
			total += sub * w
			weight += w
		}

		if eff.CPAImprovementPct != nil {
			addDimension(subscore(*eff.CPAImprovementPct), 0.25)
		}
		if eff.ROASImprovementPct != nil {
			addDimension(subscore(*eff.ROASImprovementPct), 0.25)
		}

		score := roundMoney(total / weight)
		eff.Score = &score
		eff.ScoreBasis = "success_rate x0.5 + CPA x0.25 + ROAS x0.25 ([-50,+50]% -> [0,100])"
	}

	a.writeJSONValue(w, eff)
}
