package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var graphBaseURL = "https://graph.facebook.com/v23.0"

func metaRequest(
	method string,
	path string,
	accessToken string,
	query url.Values,
	body map[string]any,
) (map[string]any, error) {

	var bodyReader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, graphBaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if query != nil {
		req.URL.RawQuery = query.Encode()
	}

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("meta error (%d)", resp.StatusCode)
		if metaErr, ok := result["error"].(map[string]any); ok {
			if m, ok := metaErr["message"].(string); ok {
				msg = m
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}

	return result, nil
}

// CreateCampaign creates a new campaign for the connected ad account.
func CreateCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adAccountID, ok := args["ad_account_id"].(string)
	if !ok {
		adAccountID = ctx.AdAccountID
	}
	if adAccountID == "" {
		return nil, fmt.Errorf("missing ad_account_id")
	}

	name, ok := args["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("missing name")
	}

	objective, ok := args["objective"].(string)
	if !ok || objective == "" {
		return nil, fmt.Errorf("missing objective")
	}

	body := map[string]any{
		"name":      name,
		"objective": objective,
		"status":    "PAUSED",
	}

	if status, ok := args["status"].(string); ok && status != "" {
		body["status"] = status
	}

	if buyingType, ok := args["buying_type"].(string); ok && buyingType != "" {
		body["buying_type"] = buyingType
	}

	if budget, ok := args["daily_budget"].(string); ok && budget != "" {
		body["daily_budget"] = budget
	}

	if budget, ok := args["lifetime_budget"].(string); ok && budget != "" {
		body["lifetime_budget"] = budget
	}

	if categories, ok := args["special_ad_categories"].([]any); ok {
		body["special_ad_categories"] = categories
	}

	result, err := metaRequest(
		http.MethodPost,
		fmt.Sprintf("/act_%s/campaigns", adAccountID),
		ctx.AccessToken,
		nil,
		body,
	)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// UpdateCampaign updates mutable fields on an existing campaign.
func UpdateCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	body := map[string]any{}

	if name, ok := args["name"].(string); ok && name != "" {
		body["name"] = name
	}

	if status, ok := args["status"].(string); ok && status != "" {
		body["status"] = status
	}

	if budget, ok := args["daily_budget"].(string); ok && budget != "" {
		body["daily_budget"] = budget
	}

	if budget, ok := args["lifetime_budget"].(string); ok && budget != "" {
		body["lifetime_budget"] = budget
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no updatable fields provided")
	}

	result, err := metaRequest(
		http.MethodPost,
		"/"+campaignID,
		ctx.AccessToken,
		nil,
		body,
	)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// DeleteCampaign permanently deletes a campaign.
func DeleteCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	result, err := metaRequest(
		http.MethodDelete,
		"/"+campaignID,
		ctx.AccessToken,
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetAdSets lists ad sets that belong to a campaign.
func GetAdSets(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	query := url.Values{
		"fields": []string{
			"id,name,status,effective_status,daily_budget,lifetime_budget,optimization_goal,billing_event,bid_strategy,targeting,start_time,end_time",
		},
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/adsets", campaignID),
		ctx.AccessToken,
		query,
		nil,
	)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].([]any); ok {
		return data, nil
	}

	return result, nil
}

// GetAds lists ads that belong to an ad set.
func GetAds(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, ok := args["ad_set_id"].(string)
	if !ok || adSetID == "" {
		return nil, fmt.Errorf("missing ad_set_id")
	}

	query := url.Values{
		"fields": []string{
			"id,name,status,effective_status,configured_status,creative",
		},
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/ads", adSetID),
		ctx.AccessToken,
		query,
		nil,
	)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].([]any); ok {
		return data, nil
	}

	return result, nil
}

// GetCampaignInsights returns performance metrics for a campaign.
func GetCampaignInsights(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	datePreset := "last_30d"
	if preset, ok := args["date_preset"].(string); ok && preset != "" {
		datePreset = preset
	}

	query := url.Values{
		"fields": []string{
			"spend,impressions,reach,clicks,ctr,cpc,cpm,frequency,actions,cost_per_action_type,purchase_roas,date_start,date_stop",
		},
		"date_preset": []string{datePreset},
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/insights", campaignID),
		ctx.AccessToken,
		query,
		nil,
	)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].([]any); ok {
		return data, nil
	}

	return result, nil
}

// UpdateCampaignStatus pauses or activates a campaign.
func UpdateCampaignStatus(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	status, ok := args["status"].(string)
	if !ok || status == "" {
		return nil, fmt.Errorf("missing status (ACTIVE, PAUSED or ARCHIVED)")
	}

	status = strings.ToUpper(status)

	switch status {
	case "ACTIVE", "PAUSED", "ARCHIVED":
	default:
		return nil, fmt.Errorf("invalid status %q, must be ACTIVE, PAUSED or ARCHIVED", status)
	}

	result, err := metaRequest(
		http.MethodPost,
		"/"+campaignID,
		ctx.AccessToken,
		nil,
		map[string]any{"status": status},
	)
	if err != nil {
		return nil, err
	}

	return result, nil
}