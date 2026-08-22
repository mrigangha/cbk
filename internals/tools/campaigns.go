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

// SetGraphBaseURL overrides the Meta Graph API base URL.
// Used by tests to point handlers at a mock server.
func SetGraphBaseURL(url string) {
	graphBaseURL = url
}

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

			base := msg
			if m, ok := metaErr["message"].(string); ok && m != "" {
				base = m
			}

			extras := []string{}

			// Human-readable hints Meta often includes.
			if u, ok := metaErr["error_user_msg"].(string); ok && u != "" {
				extras = append(extras, u)
			}
			if ti, ok := metaErr["error_user_title"].(string); ok && ti != "" {
				extras = append(extras, ti)
			}

			// Points at the exact offending request field(s).
			if ed, ok := metaErr["error_data"].(map[string]any); ok {
				if blame, ok := ed["blame_field_specs"].([]any); ok && len(blame) > 0 {
					if b, err := json.Marshal(blame); err == nil {
						extras = append(extras,
							"invalid field: "+string(b))
					}
				}
			}

			if c, ok := metaErr["code"].(float64); ok {
				codes := fmt.Sprintf("(code %d", int(c))
				if sc, ok := metaErr["error_subcode"].(float64); ok {
					codes += fmt.Sprintf(", subcode %d", int(sc))
				}
				codes += ")"
				base = base + " " + codes
			}

			msg = base
			if len(extras) > 0 {
				msg = base + "; " + strings.Join(extras, "; ")
			}
		}

		return nil, fmt.Errorf("%s", msg)
	}

	return result, nil
}

// ===========================
// ARG HELPERS
// ===========================

func argString(args map[string]any, key string) (string, bool) {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func requireString(args map[string]any, key string) (string, error) {
	v, ok := argString(args, key)
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	return v, nil
}

func extractData(result map[string]any) any {
	if data, ok := result["data"].([]any); ok {
		return data
	}
	return result
}

// setEntityStatus sets ACTIVE/PAUSED/ARCHIVED on any campaign/ad set/ad object.
func setEntityStatus(
	ctx ToolContext,
	objectID string,
	status string,
) (any, error) {

	status = strings.ToUpper(status)

	switch status {
	case "ACTIVE", "PAUSED", "ARCHIVED":
	default:
		return nil, fmt.Errorf(
			"invalid status %q, must be ACTIVE, PAUSED or ARCHIVED",
			status,
		)
	}

	return metaRequest(
		http.MethodPost,
		"/"+objectID,
		ctx.AccessToken,
		nil,
		map[string]any{"status": status},
	)
}

// ===========================
// CAMPAIGNS
// ===========================

// ListCampaigns returns all campaigns for the connected ad account.
func ListCampaigns(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adAccountID, ok := argString(args, "ad_account_id")
	if !ok {
		adAccountID = ctx.AdAccountID
	}
	if adAccountID == "" {
		return nil, fmt.Errorf("missing ad_account_id")
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/act_%s/campaigns", adAccountID),
		ctx.AccessToken,
		url.Values{
			"fields": []string{"id,name,status,objective"},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}

	return extractData(result), nil
}

// GetCampaign returns a single campaign with its configuration.
func GetCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodGet,
		"/"+campaignID,
		ctx.AccessToken,
		url.Values{
			"fields": []string{
				"id,name,status,effective_status,objective,daily_budget,lifetime_budget,buying_type,start_time,stop_time,created_time,updated_time,special_ad_categories",
			},
		},
		nil,
	)
}

// CreateCampaign creates a new campaign for the connected ad account.
func CreateCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adAccountID, ok := argString(args, "ad_account_id")
	if !ok {
		adAccountID = ctx.AdAccountID
	}
	if adAccountID == "" {
		return nil, fmt.Errorf("missing ad_account_id")
	}

	name, err := requireString(args, "name")
	if err != nil {
		return nil, err
	}

	objective, err := requireString(args, "objective")
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"name":      name,
		"objective": objective,
		"status":    "PAUSED",
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	if buyingType, ok := argString(args, "buying_type"); ok {
		body["buying_type"] = buyingType
	}

	if budget, ok := argString(args, "daily_budget"); ok {
		body["daily_budget"] = budget
	}

	if budget, ok := argString(args, "lifetime_budget"); ok {
		body["lifetime_budget"] = budget
	}

	if categories, ok := args["special_ad_categories"].([]any); ok {
		body["special_ad_categories"] = categories
	}

	return metaRequest(
		http.MethodPost,
		fmt.Sprintf("/act_%s/campaigns", adAccountID),
		ctx.AccessToken,
		nil,
		body,
	)
}

// UpdateCampaign updates mutable fields on an existing campaign.
func UpdateCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	body := map[string]any{}

	if name, ok := argString(args, "name"); ok {
		body["name"] = name
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	if budget, ok := argString(args, "daily_budget"); ok {
		body["daily_budget"] = budget
	}

	if budget, ok := argString(args, "lifetime_budget"); ok {
		body["lifetime_budget"] = budget
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no updatable fields provided")
	}

	return metaRequest(
		http.MethodPost,
		"/"+campaignID,
		ctx.AccessToken,
		nil,
		body,
	)
}

// DeleteCampaign permanently deletes a campaign.
func DeleteCampaign(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodDelete,
		"/"+campaignID,
		ctx.AccessToken,
		nil,
		nil,
	)
}

// ActivateCampaign activates a campaign.
func ActivateCampaign(ctx ToolContext, args map[string]any) (any, error) {
	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, campaignID, "ACTIVE")
}

// PauseCampaign pauses a campaign.
func PauseCampaign(ctx ToolContext, args map[string]any) (any, error) {
	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, campaignID, "PAUSED")
}

// ===========================
// AD SETS
// ===========================

// ListAdSets lists ad sets that belong to a campaign.
func ListAdSets(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/adsets", campaignID),
		ctx.AccessToken,
		url.Values{
			"fields": []string{
				"id,name,status,effective_status,daily_budget,lifetime_budget,optimization_goal,billing_event,bid_strategy,bid_amount,targeting,start_time,end_time,campaign_id",
			},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}

	return extractData(result), nil
}

// GetAdSet returns a single ad set with its configuration.
func GetAdSet(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodGet,
		"/"+adSetID,
		ctx.AccessToken,
		url.Values{
			"fields": []string{
				"id,name,status,effective_status,daily_budget,lifetime_budget,optimization_goal,billing_event,bid_strategy,bid_amount,targeting,start_time,end_time,campaign_id,created_time,updated_time",
			},
		},
		nil,
	)
}

// validGoalBillingPairs lists optimization goals we can pre-validate,
// with the billing events Meta allows for each.
var validGoalBillingPairs = map[string][]string{
	"REACH":                {"IMPRESSIONS"},
	"IMPRESSIONS":          {"IMPRESSIONS"},
	"LINK_CLICKS":          {"LINK_CLICKS"},
	"POST_ENGAGEMENT":      {"POST_ENGAGEMENT"},
	"OFFSITE_CONVERSIONS":  {"IMPRESSIONS", "LINK_CLICKS"},
	"QUALITY_LEAD":         {"IMPRESSIONS", "LINK_CLICKS"},
	"VALUE":                {"IMPRESSIONS", "LINK_CLICKS"},
	"THRUPLAY":             {"IMPRESSIONS"},
}

// goalsRequiringPromotedObject need a promoted_object (e.g. a Meta Pixel)
// on the ad set; without it the API rejects with a vague
// "Invalid parameter".
var goalsRequiringPromotedObject = map[string]bool{
	"OFFSITE_CONVERSIONS": true,
	"QUALITY_LEAD":        true,
	"VALUE":               true,
}

// validateAdSetGoal catches the common misconfigurations locally so the
// agent gets an actionable error instead of burning iterations on the API.
func validateAdSetGoal(
	optimizationGoal string,
	billingEvent string,
	promotedObject map[string]any,
) error {

	goal := strings.ToUpper(optimizationGoal)

	if allowed, known := validGoalBillingPairs[goal]; known {
		if !containsFold(allowed, billingEvent) {
			return fmt.Errorf(
				"optimization_goal %s requires billing_event %s (got %s)",
				goal,
				strings.Join(allowed, " or "),
				billingEvent,
			)
		}
	}

	if goalsRequiringPromotedObject[goal] && len(promotedObject) == 0 {
		return fmt.Errorf(
			"optimization_goal %s requires a promoted_object "+
				"(e.g. {\"pixel_id\": \"<your meta pixel id>\"}); "+
				"alternatively pick a goal like REACH or LINK_CLICKS that needs no pixel",
			goal,
		)
	}

	return nil
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

// CreateAdSet creates a new ad set under a campaign.
func CreateAdSet(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adAccountID, ok := argString(args, "ad_account_id")
	if !ok {
		adAccountID = ctx.AdAccountID
	}
	if adAccountID == "" {
		return nil, fmt.Errorf("missing ad_account_id")
	}

	name, err := requireString(args, "name")
	if err != nil {
		return nil, err
	}

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	optimizationGoal, err := requireString(args, "optimization_goal")
	if err != nil {
		return nil, err
	}

	billingEvent, err := requireString(args, "billing_event")
	if err != nil {
		return nil, err
	}

	dailyBudget, hasDaily := argString(args, "daily_budget")
	lifetimeBudget, hasLifetime := argString(args, "lifetime_budget")

	if !hasDaily && !hasLifetime {
		return nil, fmt.Errorf("one of daily_budget or lifetime_budget is required")
	}

	promotedObject, _ := args["promoted_object"].(map[string]any)

	if err := validateAdSetGoal(
		optimizationGoal,
		billingEvent,
		promotedObject,
	); err != nil {
		return nil, err
	}

	body := map[string]any{
		"name":              name,
		"campaign_id":       campaignID,
		"optimization_goal": optimizationGoal,
		"billing_event":     billingEvent,
		"status":            "PAUSED",
	}

	if promotedObject != nil {
		body["promoted_object"] = promotedObject
	}

	if hasDaily {
		body["daily_budget"] = dailyBudget
	}
	if hasLifetime {
		body["lifetime_budget"] = lifetimeBudget
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	if bidStrategy, ok := argString(args, "bid_strategy"); ok {
		body["bid_strategy"] = bidStrategy
	}

	if bidAmount, ok := argString(args, "bid_amount"); ok {
		body["bid_amount"] = bidAmount
	}

	if targeting, ok := args["targeting"].(map[string]any); ok {
		body["targeting"] = targeting
	}

	if startTime, ok := argString(args, "start_time"); ok {
		body["start_time"] = startTime
	}

	if endTime, ok := argString(args, "end_time"); ok {
		body["end_time"] = endTime
	}

	return metaRequest(
		http.MethodPost,
		fmt.Sprintf("/act_%s/adsets", adAccountID),
		ctx.AccessToken,
		nil,
		body,
	)
}

// UpdateAdSet updates mutable fields on an existing ad set.
func UpdateAdSet(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}

	body := map[string]any{}

	if name, ok := argString(args, "name"); ok {
		body["name"] = name
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	if budget, ok := argString(args, "daily_budget"); ok {
		body["daily_budget"] = budget
	}

	if budget, ok := argString(args, "lifetime_budget"); ok {
		body["lifetime_budget"] = budget
	}

	if bidAmount, ok := argString(args, "bid_amount"); ok {
		body["bid_amount"] = bidAmount
	}

	if targeting, ok := args["targeting"].(map[string]any); ok {
		body["targeting"] = targeting
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no updatable fields provided")
	}

	return metaRequest(
		http.MethodPost,
		"/"+adSetID,
		ctx.AccessToken,
		nil,
		body,
	)
}

// DeleteAdSet permanently deletes an ad set.
func DeleteAdSet(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodDelete,
		"/"+adSetID,
		ctx.AccessToken,
		nil,
		nil,
	)
}

// ActivateAdSet activates an ad set.
func ActivateAdSet(ctx ToolContext, args map[string]any) (any, error) {
	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, adSetID, "ACTIVE")
}

// PauseAdSet pauses an ad set.
func PauseAdSet(ctx ToolContext, args map[string]any) (any, error) {
	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, adSetID, "PAUSED")
}

// ===========================
// ADS
// ===========================

// ListAds lists ads that belong to an ad set.
func ListAds(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, err := requireString(args, "ad_set_id")
	if err != nil {
		return nil, err
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/ads", adSetID),
		ctx.AccessToken,
		url.Values{
			"fields": []string{
				"id,name,status,effective_status,configured_status,creative,adset_id,campaign_id",
			},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}

	return extractData(result), nil
}

// GetAd returns a single ad with its configuration.
func GetAd(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodGet,
		"/"+adID,
		ctx.AccessToken,
		url.Values{
			"fields": []string{
				"id,name,status,effective_status,configured_status,creative,adset_id,campaign_id,created_time,updated_time",
			},
		},
		nil,
	)
}

// CreateAd creates a new ad under an ad set using an existing ad creative.
func CreateAd(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adAccountID, ok := argString(args, "ad_account_id")
	if !ok {
		adAccountID = ctx.AdAccountID
	}
	if adAccountID == "" {
		return nil, fmt.Errorf("missing ad_account_id")
	}

	name, err := requireString(args, "name")
	if err != nil {
		return nil, err
	}

	adSetID, err := requireString(args, "ad_set_id")
	if err != nil {
		return nil, err
	}

	creative, ok := args["creative"].(map[string]any)

	if !ok {
		creativeID, err := requireString(args, "creative_id")
		if err != nil {
			return nil, err
		}
		creative = map[string]any{"creative_id": creativeID}
	}

	body := map[string]any{
		"name":     name,
		"adset_id": adSetID,
		"creative": creative,
		"status":   "PAUSED",
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	return metaRequest(
		http.MethodPost,
		fmt.Sprintf("/act_%s/ads", adAccountID),
		ctx.AccessToken,
		nil,
		body,
	)
}

// UpdateAd updates mutable fields on an existing ad.
func UpdateAd(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}

	body := map[string]any{}

	if name, ok := argString(args, "name"); ok {
		body["name"] = name
	}

	if status, ok := argString(args, "status"); ok {
		body["status"] = status
	}

	if creative, ok := args["creative"].(map[string]any); ok {
		body["creative"] = creative
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("no updatable fields provided")
	}

	return metaRequest(
		http.MethodPost,
		"/"+adID,
		ctx.AccessToken,
		nil,
		body,
	)
}

// DeleteAd permanently deletes an ad.
func DeleteAd(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}

	return metaRequest(
		http.MethodDelete,
		"/"+adID,
		ctx.AccessToken,
		nil,
		nil,
	)
}

// ActivateAd activates an ad.
func ActivateAd(ctx ToolContext, args map[string]any) (any, error) {
	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, adID, "ACTIVE")
}

// PauseAd pauses an ad.
func PauseAd(ctx ToolContext, args map[string]any) (any, error) {
	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}
	return setEntityStatus(ctx, adID, "PAUSED")
}

// ===========================
// INSIGHTS
// ===========================

const insightFields = "spend,impressions,reach,clicks,ctr,cpc,cpm,frequency,actions,cost_per_action_type,purchase_roas,date_start,date_stop"

func fetchInsights(
	ctx ToolContext,
	objectID string,
	datePreset string,
) (any, error) {

	if datePreset == "" {
		datePreset = "last_30d"
	}

	result, err := metaRequest(
		http.MethodGet,
		fmt.Sprintf("/%s/insights", objectID),
		ctx.AccessToken,
		url.Values{
			"fields":      []string{insightFields},
			"date_preset": []string{datePreset},
		},
		nil,
	)
	if err != nil {
		return nil, err
	}

	return extractData(result), nil
}

// GetCampaignInsights returns performance metrics for a campaign.
func GetCampaignInsights(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, err := requireString(args, "campaign_id")
	if err != nil {
		return nil, err
	}

	datePreset, _ := argString(args, "date_preset")

	return fetchInsights(ctx, campaignID, datePreset)
}

// GetAdSetInsights returns performance metrics for an ad set.
func GetAdSetInsights(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adSetID, err := requireString(args, "adset_id")
	if err != nil {
		return nil, err
	}

	datePreset, _ := argString(args, "date_preset")

	return fetchInsights(ctx, adSetID, datePreset)
}

// GetAdInsights returns performance metrics for an ad.
func GetAdInsights(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	adID, err := requireString(args, "ad_id")
	if err != nil {
		return nil, err
	}

	datePreset, _ := argString(args, "date_preset")

	return fetchInsights(ctx, adID, datePreset)
}

// CompareCampaigns fetches insights for several campaigns so the model
// can compare their performance side by side.
func CompareCampaigns(
	ctx ToolContext,
	args map[string]any,
) (any, error) {

	raw, ok := args["campaign_ids"].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("missing campaign_ids")
	}

	const maxIDs = 10
	if len(raw) > maxIDs {
		return nil, fmt.Errorf("too many campaign_ids (%d), maximum is %d", len(raw), maxIDs)
	}

	datePreset, _ := argString(args, "date_preset")

	comparisons := make([]map[string]any, 0, len(raw))

	for _, v := range raw {
		id, ok := v.(string)
		if !ok || id == "" {
			continue
		}

		entry := map[string]any{"campaign_id": id}

		insights, err := fetchInsights(ctx, id, datePreset)
		if err != nil {
			entry["error"] = err.Error()
		} else {
			entry["insights"] = insights
		}

		comparisons = append(comparisons, entry)
	}

	if len(comparisons) == 0 {
		return nil, fmt.Errorf("no valid campaign_ids provided")
	}

	return comparisons, nil
}
