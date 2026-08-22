package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
		},
	})
}

func TestCreateCampaign_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/act_123/campaigns" {
			t.Errorf("expected path /act_123/campaigns, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("expected Authorization Bearer test-token, got %q", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if payload["name"] != "Spring Sale" {
			t.Errorf("expected name Spring Sale, got %v", payload["name"])
		}
		if payload["objective"] != "OUTCOME_SALES" {
			t.Errorf("expected objective OUTCOME_SALES, got %v", payload["objective"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "123456789"})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := CreateCampaign(ctx, map[string]any{
		"name":      "Spring Sale",
		"objective": "OUTCOME_SALES",
	})
	if err != nil {
		t.Fatalf("CreateCampaign returned error: %v", err)
	}
	res, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if res["id"] != "123456789" {
		t.Errorf("expected id 123456789, got %v", res["id"])
	}
}

func TestCreateCampaign_MissingName(t *testing.T) {
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := CreateCampaign(ctx, map[string]any{"objective": "OUTCOME_SALES"})
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	if !strings.Contains(err.Error(), "missing name") {
		t.Errorf("expected missing name error, got %q", err.Error())
	}
}

func TestCreateCampaign_MetaError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "Invalid token")
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := CreateCampaign(ctx, map[string]any{
		"name":      "Spring Sale",
		"objective": "OUTCOME_SALES",
	})
	if err == nil {
		t.Fatal("expected error from Meta, got nil")
	}
	if err.Error() != "Invalid token" {
		t.Errorf("expected Invalid token error, got %q", err.Error())
	}
}

func TestUpdateCampaign_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/987654321" {
			t.Errorf("expected path /987654321, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("expected Authorization Bearer test-token, got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if payload["status"] != "PAUSED" {
			t.Errorf("expected status PAUSED, got %v", payload["status"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := UpdateCampaign(ctx, map[string]any{
		"campaign_id": "987654321",
		"status":      "PAUSED",
	})
	if err != nil {
		t.Fatalf("UpdateCampaign returned error: %v", err)
	}
	res, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if res["success"] != true {
		t.Errorf("expected success true, got %v", res["success"])
	}
}

func TestUpdateCampaign_NoUpdatableFields(t *testing.T) {
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := UpdateCampaign(ctx, map[string]any{"campaign_id": "987654321"})
	if err == nil {
		t.Fatal("expected error for no updatable fields, got nil")
	}
	if err.Error() != "no updatable fields provided" {
		t.Errorf("expected no updatable fields error, got %q", err.Error())
	}
}

func TestActivateCampaign_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/123456" {
			t.Errorf("expected path /123456, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if payload["status"] != "ACTIVE" {
			t.Errorf("expected status ACTIVE, got %v", payload["status"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := ActivateCampaign(ctx, map[string]any{
		"campaign_id": "123456",
	})
	if err != nil {
		t.Fatalf("ActivateCampaign returned error: %v", err)
	}
	if res, ok := result.(map[string]any); !ok || res["success"] != true {
		t.Errorf("expected success true, got %v", result)
	}
}

func TestPauseCampaign_MissingID(t *testing.T) {
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := PauseCampaign(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing campaign_id, got nil")
	}
	if !strings.Contains(err.Error(), "missing campaign_id") {
		t.Errorf("expected missing campaign_id error, got %q", err.Error())
	}
}

func TestSetEntityStatus_InvalidStatus(t *testing.T) {
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := setEntityStatus(ctx, "123456", "DELETED")
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("expected invalid status error, got %q", err.Error())
	}
}

func TestDeleteCampaign_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/555666" {
			t.Errorf("expected path /555666, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("expected Authorization Bearer test-token, got %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := DeleteCampaign(ctx, map[string]any{"campaign_id": "555666"})
	if err != nil {
		t.Fatalf("DeleteCampaign returned error: %v", err)
	}
	if res, ok := result.(map[string]any); !ok || res["success"] != true {
		t.Errorf("expected success true, got %v", result)
	}
}

func TestListAdSets_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/123456/adsets" {
			t.Errorf("expected path /123456/adsets, got %s", r.URL.Path)
		}
		fields := r.URL.Query().Get("fields")
		if fields == "" {
			t.Error("expected fields query param")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []any{
				map[string]any{"id": "1", "name": "AdSet A"},
				map[string]any{"id": "2", "name": "AdSet B"},
			},
		})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := ListAdSets(ctx, map[string]any{"campaign_id": "123456"})
	if err != nil {
		t.Fatalf("ListAdSets returned error: %v", err)
	}
	data, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any result, got %T", result)
	}
	if len(data) != 2 {
		t.Errorf("expected 2 ad sets, got %d", len(data))
	}
}

func TestListAds_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/777/ads" {
			t.Errorf("expected path /777/ads, got %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []any{
				map[string]any{"id": "10", "name": "Ad X"},
			},
		})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := ListAds(ctx, map[string]any{"ad_set_id": "777"})
	if err != nil {
		t.Fatalf("ListAds returned error: %v", err)
	}
	data, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any result, got %T", result)
	}
	if len(data) != 1 {
		t.Errorf("expected 1 ad, got %d", len(data))
	}
}

func TestGetCampaignInsights_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/888/insights" {
			t.Errorf("expected path /888/insights, got %s", r.URL.Path)
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatalf("invalid query: %v", err)
		}
		if got := q.Get("date_preset"); got != "last_7d" {
			t.Errorf("expected date_preset last_7d, got %q", got)
		}
		if q.Get("fields") == "" {
			t.Error("expected fields query param")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []any{
				map[string]any{"spend": "12.34", "impressions": "1000"},
			},
		})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := GetCampaignInsights(ctx, map[string]any{
		"campaign_id": "888",
		"date_preset": "last_7d",
	})
	if err != nil {
		t.Fatalf("GetCampaignInsights returned error: %v", err)
	}
	data, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any result, got %T", result)
	}
	if len(data) != 1 {
		t.Errorf("expected 1 insight row, got %d", len(data))
	}
}

func TestCreateAdSet_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/act_123/adsets" {
			t.Errorf("expected path /act_123/adsets, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if payload["name"] != "Retargeting" {
			t.Errorf("expected name Retargeting, got %v", payload["name"])
		}
		if payload["campaign_id"] != "555" {
			t.Errorf("expected campaign_id 555, got %v", payload["campaign_id"])
		}
		if payload["optimization_goal"] != "LINK_CLICKS" {
			t.Errorf("expected optimization_goal LINK_CLICKS, got %v", payload["optimization_goal"])
		}
		if payload["billing_event"] != "IMPRESSIONS" {
			t.Errorf("expected billing_event IMPRESSIONS, got %v", payload["billing_event"])
		}
		if payload["daily_budget"] != "2000" {
			t.Errorf("expected daily_budget 2000, got %v", payload["daily_budget"])
		}
		targeting, ok := payload["targeting"].(map[string]any)
		if !ok {
			t.Fatalf("expected targeting object, got %T", payload["targeting"])
		}
		if targeting["age_min"] != float64(18) {
			t.Errorf("expected age_min 18, got %v", targeting["age_min"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "777888"})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := CreateAdSet(ctx, map[string]any{
		"name":              "Retargeting",
		"campaign_id":       "555",
		"optimization_goal": "LINK_CLICKS",
		"billing_event":     "IMPRESSIONS",
		"daily_budget":      "2000",
		"targeting": map[string]any{
			"geo_locations": map[string]any{"countries": []any{"US"}},
			"age_min":       18,
		},
	})
	if err != nil {
		t.Fatalf("CreateAdSet returned error: %v", err)
	}
	if res, ok := result.(map[string]any); !ok || res["id"] != "777888" {
		t.Errorf("expected id 777888, got %v", result)
	}
}

func TestCreateAdSet_MissingBudget(t *testing.T) {
	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := CreateAdSet(ctx, map[string]any{
		"name":              "Retargeting",
		"campaign_id":       "555",
		"optimization_goal": "LINK_CLICKS",
		"billing_event":     "IMPRESSIONS",
	})
	if err == nil {
		t.Fatal("expected error for missing budget, got nil")
	}
	if !strings.Contains(err.Error(), "daily_budget or lifetime_budget") {
		t.Errorf("expected missing budget error, got %q", err.Error())
	}
}

func TestCreateAd_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/act_123/ads" {
			t.Errorf("expected path /act_123/ads, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if payload["adset_id"] != "777" {
			t.Errorf("expected adset_id 777, got %v", payload["adset_id"])
		}
		creative, ok := payload["creative"].(map[string]any)
		if !ok {
			t.Fatalf("expected creative object, got %T", payload["creative"])
		}
		if creative["creative_id"] != "999" {
			t.Errorf("expected creative_id 999, got %v", creative["creative_id"])
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "111222"})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := CreateAd(ctx, map[string]any{
		"name":        "Summer Creative",
		"ad_set_id":   "777",
		"creative_id": "999",
	})
	if err != nil {
		t.Fatalf("CreateAd returned error: %v", err)
	}
	if res, ok := result.(map[string]any); !ok || res["id"] != "111222" {
		t.Errorf("expected id 111222, got %v", result)
	}
}

func TestCompareCampaigns_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/insights") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []any{
				map[string]any{"spend": "10.00"},
			},
		})
	}))
	defer ts.Close()

	old := graphBaseURL
	graphBaseURL = ts.URL
	defer func() { graphBaseURL = old }()

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	result, err := CompareCampaigns(ctx, map[string]any{
		"campaign_ids": []any{"888", "999"},
	})
	if err != nil {
		t.Fatalf("CompareCampaigns returned error: %v", err)
	}
	rows, ok := result.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any result, got %T", result)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 comparison rows, got %d", len(rows))
	}
	if rows[0]["campaign_id"] != "888" || rows[1]["campaign_id"] != "999" {
		t.Errorf("unexpected campaign ids: %v %v", rows[0]["campaign_id"], rows[1]["campaign_id"])
	}
	if _, hasErr := rows[0]["error"]; hasErr {
		t.Errorf("unexpected error in first row: %v", rows[0]["error"])
	}
}

func TestCompareCampaigns_TooMany(t *testing.T) {
	ids := make([]any, 11)
	for i := range ids {
		ids[i] = fmt.Sprintf("%d", i)
	}

	ctx := ToolContext{AccessToken: "test-token", AdAccountID: "123"}
	_, err := CompareCampaigns(ctx, map[string]any{"campaign_ids": ids})
	if err == nil {
		t.Fatal("expected error for too many campaign_ids, got nil")
	}
	if !strings.Contains(err.Error(), "maximum is 10") {
		t.Errorf("expected maximum error, got %q", err.Error())
	}
}

