package analytics

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrigangha/cbk/internals/metaclient"
)

var graphBaseURL = "https://graph.facebook.com/v23.0"

// GraphBaseURL returns the current Meta Graph API base URL.
func GraphBaseURL() string {
	return graphBaseURL
}

// SetGraphBaseURL overrides the Meta Graph API base URL (tests).
func SetGraphBaseURL(u string) {
	graphBaseURL = u
	metaclient.SetBaseURL(u)
}

// get fetches a Graph API path through the shared metaclient so all
// analytics traffic benefits from rate-limit protection.
func get(
	accessToken string,
	path string,
	query url.Values,
) (map[string]any, error) {

	var q map[string][]string
	if query != nil {
		q = query
	}

	out, status, err := metaclient.Request(
		http.MethodGet, path, accessToken, q, nil,
	)
	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		msg := fmt.Sprintf("meta error (%d)", status)
		if e, ok := out["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				msg = m
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}

	return out, nil
}

const insightFields = "spend,impressions,reach,clicks,ctr,cpc,cpm,frequency,actions,action_values,purchase_roas"

// Scope of an insights query.
type Scope string

const (
	ScopeAccount  Scope = "account"
	ScopeCampaign Scope = "campaign"
	ScopeAdSet    Scope = "adset"
	ScopeAd       Scope = "ad"
)

func (s Scope) path(objectID string) string {
	switch s {
	case ScopeCampaign:
		return "/" + objectID + "/insights"
	case ScopeAdSet:
		return "/" + objectID + "/insights"
	case ScopeAd:
		return "/" + objectID + "/insights"
	default:
		return "/act_" + objectID + "/insights"
	}
}

func normalizeScope(s string) Scope {
	switch Scope(strings.ToLower(s)) {
	case ScopeCampaign:
		return ScopeCampaign
	case ScopeAdSet:
		return ScopeAdSet
	case ScopeAd:
		return ScopeAd
	default:
		return ScopeAccount
	}
}

// NormalizeScopePublic parses a scope string for external callers.
func NormalizeScopePublic(s string) Scope {
	return normalizeScope(s)
}

// FetchMetrics pulls aggregated normalized metrics for one object.
func FetchMetrics(
	token, objectID string,
	scope Scope,
	datePreset string,
) (Metrics, error) {

	q := url.Values{
		"fields":      []string{insightFields},
		"date_preset": []string{datePreset},
		"limit":       []string{"500"},
	}

	out, err := get(token, scope.path(objectID), q)
	if err != nil {
		return Metrics{}, err
	}

	return rowsToMetrics(out), nil
}

// FetchDaily pulls per-day normalized metrics for trends/anomalies.
func FetchDaily(
	token, objectID string,
	scope Scope,
	datePreset string,
) ([]TimePoint, error) {

	q := url.Values{
		"fields":         []string{insightFields},
		"date_preset":    []string{datePreset},
		"time_increment": []string{"1"},
		"limit":          []string{"400"},
	}

	out, err := get(token, scope.path(objectID), q)
	if err != nil {
		return nil, err
	}

	days := []TimePoint{}

	if data, ok := out["data"].([]any); ok {
		for _, row := range data {
			rowMap, ok := row.(map[string]any)
			if !ok {
				continue
			}

			date, _ := rowMap["date_start"].(string)

			days = append(days, TimePoint{
				Date:    date,
				Metrics: NormalizeInsight(rowMap),
			})
		}
	}

	return days, nil
}

func rowsToMetrics(out map[string]any) Metrics {

	if data, ok := out["data"].([]any); ok && len(data) > 0 {
		if row, ok := data[0].(map[string]any); ok {
			return NormalizeInsight(row)
		}
	}

	return Metrics{} // no spend in window
}

// ListObjects lists child objects with basic fields.
func ListObjects(
	token string,
	accountID string,
	kind string, // campaigns | adsets | ads
) ([]map[string]any, error) {

	var path string
	fields := ""

	switch kind {
	case "campaigns":
		path = "/act_" + accountID + "/campaigns"
		fields = "id,name,status,objective,daily_budget,lifetime_budget"
	case "adsets":
		path = "/act_" + accountID + "/adsets"
		fields = "id,name,status,campaign_id,daily_budget,lifetime_budget"
	default:
		path = "/act_" + accountID + "/ads"
		fields = "id,name,status,adset_id,creative"
	}

	out, err := get(token, path, url.Values{"fields": []string{fields}, "limit": []string{"200"}})
	if err != nil {
		return nil, err
	}

	data, _ := out["data"].([]any)

	list := make([]map[string]any, 0, len(data))

	for _, item := range data {
		if m, ok := item.(map[string]any); ok {
			list = append(list, m)
		}
	}

	return list, nil
}
