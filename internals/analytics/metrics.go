// Package analytics converts raw Meta Ads data into marketing
// intelligence: normalized metrics, performance scores, health,
// budget efficiency, creative fatigue, anomalies and goal progress.
package analytics

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Metrics is the normalized metric set every endpoint and tool speaks.
type Metrics struct {
	Spend       float64 `json:"spend"`
	Impressions float64 `json:"impressions"`
	Reach       float64 `json:"reach"`
	Clicks      float64 `json:"clicks"`
	CTR         float64 `json:"ctr"` // %
	CPC         float64 `json:"cpc"` // cost per click
	CPM         float64 `json:"cpm"` // cost per 1000 impressions
	Frequency   float64 `json:"frequency"`

	Leads     float64 `json:"leads"`
	Purchases float64 `json:"purchases"`

	Conversions     float64 `json:"conversions"`
	ConversionValue float64 `json:"conversion_value"`

	CVR  float64 `json:"cvr"` // conversions / clicks, %
	CPL  float64 `json:"cpl"` // spend per lead
	CPA  float64 `json:"cpa"` // spend per conversion
	ROAS float64 `json:"roas"`
}

// TimePoint is one day of normalized metrics (trends).
type TimePoint struct {
	Date    string  `json:"date"`
	Metrics Metrics `json:"metrics"`
}

// Action type fragments used to classify Meta "actions" entries.
const leadFragment = "lead"

// isPurchaseType reports whether a Meta action_type denotes a purchase.
func isPurchaseType(actionType string) bool {
	return strings.Contains(strings.ToLower(actionType), "purchase")
}

func parseFloat(v any) float64 {
	switch t := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case int:
		return float64(t)
	default:
		return 0
	}
}

// NormalizeInsight converts one raw Meta insights row into Metrics.
// Rows come from the Graph API as loosely typed maps with string values.
func NormalizeInsight(row map[string]any) Metrics {

	var m Metrics

	m.Spend = parseFloat(row["spend"])
	m.Impressions = parseFloat(row["impressions"])
	m.Reach = parseFloat(row["reach"])
	m.Clicks = parseFloat(row["clicks"])
	m.CTR = parseFloat(row["ctr"])
	m.CPC = parseFloat(row["cpc"])
	m.CPM = parseFloat(row["cpm"])
	m.Frequency = parseFloat(row["frequency"])

	// Classify actions: leads, purchases, total conversion value.
	if actions, ok := row["actions"].([]any); ok {
		for _, a := range actions {

			actionMap, ok := a.(map[string]any)
			if !ok {
				continue
			}

			actionType, _ := actionMap["action_type"].(string)
			value := parseFloat(actionMap["value"])

			lower := strings.ToLower(actionType)

			if strings.Contains(lower, leadFragment) {
				m.Leads += value
			}

			if isPurchaseType(actionType) {
				m.Purchases += value
			}
		}
	}

	// Conversion value from action_values (purchase revenue).
	if values, ok := row["action_values"].([]any); ok {
		for _, a := range values {

			actionMap, ok := a.(map[string]any)
			if !ok {
				continue
			}

			actionType, _ := actionMap["action_type"].(string)

			if isPurchaseType(actionType) {
				m.ConversionValue += parseFloat(actionMap["value"])
			}
		}
	}

	// ROAS: prefer purchase_roas field, fall back to value/spend.
	if roasList, ok := row["purchase_roas"].([]any); ok && len(roasList) > 0 {
		if first, ok := roasList[0].(map[string]any); ok {
			m.ROAS = parseFloat(first["roas"])
		}
	}
	if m.ROAS == 0 && m.Spend > 0 && m.ConversionValue > 0 {
		m.ROAS = m.ConversionValue / m.Spend
	}

	// Conversions: purchases when present, otherwise leads.
	m.Conversions = m.Purchases
	if m.Conversions == 0 {
		m.Conversions = m.Leads
	}

	// Derived ratios — recompute rather than trust raw fields so all
	// metrics are internally consistent.
	if m.Impressions > 0 {
		if m.CTR == 0 && m.Clicks > 0 {
			m.CTR = m.Clicks / m.Impressions * 100
		}
		if m.Frequency == 0 && m.Reach > 0 {
			m.Frequency = m.Impressions / m.Reach
		}
		if m.CPM == 0 {
			m.CPM = m.Spend / m.Impressions * 1000
		}
	}
	if m.Clicks > 0 {
		if m.CPC == 0 {
			m.CPC = m.Spend / m.Clicks
		}
		if m.Conversions > 0 {
			m.CVR = m.Conversions / m.Clicks * 100
		}
	}
	if m.Leads > 0 {
		m.CPL = m.Spend / m.Leads
	}
	if m.Conversions > 0 {
		m.CPA = m.Spend / m.Conversions
	}

	return m
}

// DeriveRatios recomputes any missing derived metrics (CTR, CPC, CVR,
// CPL, CPA, ROAS, frequency, conversions) from their components. This
// makes the intelligence functions safe on hand-built Metrics values.
func DeriveRatios(m Metrics) Metrics {

	// Never clobber an explicitly provided Conversions value.
	if m.Conversions == 0 {
		m.Conversions = m.Purchases
	}
	if m.Conversions == 0 {
		m.Conversions = m.Leads
	}

	if m.CTR == 0 && m.Impressions > 0 && m.Clicks > 0 {
		m.CTR = m.Clicks / m.Impressions * 100
	}
	if m.Frequency == 0 && m.Reach > 0 && m.Impressions > 0 {
		m.Frequency = m.Impressions / m.Reach
	}
	if m.CPM == 0 && m.Impressions > 0 {
		m.CPM = m.Spend / m.Impressions * 1000
	}
	if m.CPC == 0 && m.Clicks > 0 {
		m.CPC = m.Spend / m.Clicks
	}
	if m.CVR == 0 && m.Clicks > 0 && m.Conversions > 0 {
		m.CVR = m.Conversions / m.Clicks * 100
	}
	if m.CPL == 0 && m.Leads > 0 {
		m.CPL = m.Spend / m.Leads
	}
	if m.CPA == 0 && m.Conversions > 0 {
		m.CPA = m.Spend / m.Conversions
	}
	if m.ROAS == 0 && m.Spend > 0 && m.ConversionValue > 0 {
		m.ROAS = m.ConversionValue / m.Spend
	}

	return m
}

// Sum aggregates normalized metrics across rows (e.g. campaigns → account).
// Ratio metrics are recomputed from their components where possible.
func Sum(rows []Metrics) Metrics {
	var t Metrics

	for _, r := range rows {
		t.Spend += r.Spend
		t.Impressions += r.Impressions
		t.Reach += r.Reach
		t.Clicks += r.Clicks
		t.Leads += r.Leads
		t.Purchases += r.Purchases
		t.Conversions += r.Conversions
		t.ConversionValue += r.ConversionValue
	}

	// Weighted averages for frequency; ROAS/CVR etc recomputed below.
	var weightedFreq float64
	if t.Impressions > 0 {
		for _, r := range rows {
			weightedFreq += r.Frequency * r.Impressions
		}
		t.Frequency = weightedFreq / t.Impressions
		t.CTR = t.Clicks / t.Impressions * 100
		t.CPM = t.Spend / t.Impressions * 1000
	}
	if t.Clicks > 0 {
		t.CPC = t.Spend / t.Clicks
		t.CVR = t.Conversions / t.Clicks * 100
	}
	if t.Leads > 0 {
		t.CPL = t.Spend / t.Leads
	}
	if t.Conversions > 0 {
		t.CPA = t.Spend / t.Conversions
	}
	if t.Spend > 0 {
		t.ROAS = t.ConversionValue / t.Spend
	}

	return t
}
