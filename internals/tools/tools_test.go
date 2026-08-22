package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrigangha/cbk/internals/analytics"
)

// TestAllTools_Execute runs every registered tool through ExecuteTool
// against a mock Meta Graph API to guarantee the whole registry works.
func TestAllTools_Execute(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("%s %s: missing auth header, got %q", r.Method, r.URL.Path, got)
		}

		path := r.URL.Path

		// Insights endpoints
		if strings.HasSuffix(path, "/insights") {
			writeJSON(w, http.StatusOK, map[string]any{
				"data": []any{map[string]any{"spend": "1.00", "clicks": "5"}},
			})
			return
		}

		// Account-level collections
		if strings.HasPrefix(path, "/act_") {
			switch {
			case strings.HasSuffix(path, "/campaigns") && r.Method == http.MethodGet:
				writeJSON(w, http.StatusOK, map[string]any{
					"data": []any{map[string]any{"id": "c1", "name": "Camp"}},
				})
			case strings.HasSuffix(path, "/campaigns"):
				writeJSON(w, http.StatusOK, map[string]any{"id": "new-campaign"})
			case strings.HasSuffix(path, "/adsets"):
				writeJSON(w, http.StatusOK, map[string]any{"id": "new-adset"})
			case strings.HasSuffix(path, "/ads"):
				writeJSON(w, http.StatusOK, map[string]any{"id": "new-ad"})
			default:
				t.Errorf("unexpected account path %s", path)
			}
			return
		}

		// Entity sub-resources
		if strings.HasSuffix(path, "/adsets") {
			writeJSON(w, http.StatusOK, map[string]any{
				"data": []any{map[string]any{"id": "as1", "name": "AdSet"}},
			})
			return
		}
		if strings.HasSuffix(path, "/ads") {
			writeJSON(w, http.StatusOK, map[string]any{
				"data": []any{map[string]any{"id": "a1", "name": "Ad"}},
			})
			return
		}

		// Single objects (get/update/delete/status)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      strings.TrimPrefix(path, "/"),
			"name":    "Object",
			"success": true,
		})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL

	oldAnalytics := analytics.GraphBaseURL()
	analytics.SetGraphBaseURL(ts.URL)

	defer func() {
		graphBaseURL = old
		analytics.SetGraphBaseURL(oldAnalytics)
	}()

	h := NewToolHandler()
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}

	tests := []struct {
		name string
		args map[string]any
	}{
		// Campaigns
		{"list_campaigns", nil},
		{"get_campaign", map[string]any{"campaign_id": "111"}},
		{"create_campaign", map[string]any{"name": "C", "objective": "OUTCOME_SALES"}},
		{"update_campaign", map[string]any{"campaign_id": "111", "status": "PAUSED"}},
		{"delete_campaign", map[string]any{"campaign_id": "555"}},
		{"activate_campaign", map[string]any{"campaign_id": "111"}},
		{"pause_campaign", map[string]any{"campaign_id": "111"}},
		// Ad sets
		{"list_adsets", map[string]any{"campaign_id": "123456"}},
		{"get_adset", map[string]any{"adset_id": "777"}},
		{"create_adset", map[string]any{
			"name":              "AS",
			"campaign_id":       "555",
			"optimization_goal": "REACH",
			"billing_event":     "IMPRESSIONS",
			"daily_budget":      "1000",
		}},
		{"update_adset", map[string]any{"adset_id": "888", "daily_budget": "3000"}},
		{"delete_adset", map[string]any{"adset_id": "444"}},
		{"activate_adset", map[string]any{"adset_id": "888"}},
		{"pause_adset", map[string]any{"adset_id": "888"}},
		// Ads
		{"list_ads", map[string]any{"ad_set_id": "777"}},
		{"get_ad", map[string]any{"ad_id": "999"}},
		{"create_ad", map[string]any{"name": "A", "ad_set_id": "777", "creative_id": "999"}},
		{"update_ad", map[string]any{"ad_id": "222", "name": "Renamed"}},
		{"delete_ad", map[string]any{"ad_id": "333"}},
		{"activate_ad", map[string]any{"ad_id": "222"}},
		{"pause_ad", map[string]any{"ad_id": "222"}},
		// Insights
		{"get_campaign_insights", map[string]any{"campaign_id": "888"}},
		{"get_adset_insights", map[string]any{"adset_id": "888"}},
		{"get_ad_insights", map[string]any{"ad_id": "999"}},
		{"compare_campaigns", map[string]any{"campaign_ids": []any{"888", "999"}}},
		// Intelligence
		{"get_analytics", nil},
	}

	registered := h.GetTools()
	if len(registered) != len(tests) {
		t.Fatalf("registry has %d tools but test table covers %d", len(registered), len(tests))
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := h.ExecuteTool(ctx, tc.name, tc.args)
			if err != nil {
				t.Fatalf("ExecuteTool(%s) returned error: %v", tc.name, err)
			}
			if result == nil {
				t.Fatalf("ExecuteTool(%s) returned nil result", tc.name)
			}
		})
	}
}

// TestExecuteTool_Unknown ensures unknown tool names fail cleanly.
func TestExecuteTool_Unknown(t *testing.T) {
	h := NewToolHandler()
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}

	_, err := h.ExecuteTool(ctx, "does_not_exist", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %q", err.Error())
	}
}
