package tools

import (
	"fmt"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     func(ctx ToolContext, args map[string]any) (any, error)
}

type ToolContext struct {
	AccessToken string
	AdAccountID string
	UserID      int64
}

type ToolHandler struct {
	tools map[string]Tool
}

func NewToolHandler() *ToolHandler {
	h := &ToolHandler{
		tools: make(map[string]Tool),
	}

	// ===========================
	// CAMPAIGNS
	// ===========================

	h.RegisterTool(Tool{
		Name:        "list_campaigns",
		Description: "Returns all campaigns for the connected Meta Ads account.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_account_id": map[string]any{
					"type":        "STRING",
					"description": "The ad account ID. Defaults to the connected account.",
				},
			},
		},
		Handler: ListCampaigns,
	})

	h.RegisterTool(Tool{
		Name:        "get_campaign",
		Description: "Returns a single campaign with its configuration (status, objective, budgets, special ad categories).",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: GetCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "create_campaign",
		Description: "Creates a new campaign in the connected Meta Ads account. Provide a name, objective (e.g. OUTCOME_TRAFFIC, OUTCOME_LEADS, OUTCOME_SALES), and optionally status, buying_type and daily_budget in minor units.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_account_id": map[string]any{
					"type":        "STRING",
					"description": "The ad account ID. Defaults to the connected account.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "The name of the campaign.",
				},
				"objective": map[string]any{
					"type":        "STRING",
					"description": "The campaign objective, e.g. OUTCOME_TRAFFIC, OUTCOME_ENGAGEMENT, OUTCOME_LEADS, OUTCOME_APP_PROMOTION, OUTCOME_SALES.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "ACTIVE or PAUSED. Defaults to PAUSED.",
				},
				"buying_type": map[string]any{
					"type":        "STRING",
					"description": "AUCTION or RESERVED. Defaults to AUCTION.",
				},
				"daily_budget": map[string]any{
					"type":        "STRING",
					"description": "Daily budget in minor units (e.g. 1000 = $10.00).",
				},
				"lifetime_budget": map[string]any{
					"type":        "STRING",
					"description": "Lifetime budget in minor units (e.g. 10000 = $100.00).",
				},
				"special_ad_categories": map[string]any{
					"type":        "ARRAY",
					"description": "Special ad categories, e.g. CREDIT, EMPLOYMENT, HOUSING, SOCIAL_ISSUES_ELECTIONS_POLITICS.",
					"items": map[string]any{
						"type": "STRING",
					},
				},
			},
			"required": []string{"name", "objective"},
		},
		Handler: CreateCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "update_campaign",
		Description: "Updates mutable fields (name, status, daily_budget, lifetime_budget) on an existing campaign.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign to update.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "New campaign name.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "New status: ACTIVE or PAUSED.",
				},
				"daily_budget": map[string]any{
					"type":        "STRING",
					"description": "New daily budget in minor units.",
				},
				"lifetime_budget": map[string]any{
					"type":        "STRING",
					"description": "New lifetime budget in minor units.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: UpdateCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "delete_campaign",
		Description: "Permanently deletes a campaign. Use with caution.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign to delete.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: DeleteCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "activate_campaign",
		Description: "Activates a campaign so it can spend and deliver.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: ActivateCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "pause_campaign",
		Description: "Pauses a campaign to stop it from spending.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: PauseCampaign,
	})

	// ===========================
	// AD SETS
	// ===========================

	h.RegisterTool(Tool{
		Name:        "list_adsets",
		Description: "Returns all ad sets that belong to a campaign.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: ListAdSets,
	})

	h.RegisterTool(Tool{
		Name:        "get_adset",
		Description: "Returns a single ad set with its configuration (budgets, optimization goal, targeting, schedule).",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: GetAdSet,
	})

	h.RegisterTool(Tool{
		Name:        "create_adset",
		Description: "Creates a new ad set under a campaign. Requires name, campaign_id, optimization_goal, billing_event, one of daily_budget or lifetime_budget (minor units), and targeting like {\"geo_locations\":{\"countries\":[\"US\"]},\"age_min\":18}. Constraints: billing_event must pair with optimization_goal (REACH->IMPRESSIONS, LINK_CLICKS->LINK_CLICKS, POST_ENGAGEMENT->POST_ENGAGEMENT; OFFSITE_CONVERSIONS/QUALITY_LEAD/VALUE allow IMPRESSIONS or LINK_CLICKS). The optimization_goal must be allowed by the parent campaign's objective (e.g. an OUTCOME_LEADS campaign does not accept LINK_CLICKS). OFFSITE_CONVERSIONS, QUALITY_LEAD and VALUE additionally require promoted_object with a Meta pixel id.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_account_id": map[string]any{
					"type":        "STRING",
					"description": "The ad account ID. Defaults to the connected account.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "The name of the ad set.",
				},
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the parent campaign.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "ACTIVE or PAUSED. Defaults to PAUSED.",
				},
				"daily_budget": map[string]any{
					"type":        "STRING",
					"description": "Daily budget in minor units. Required unless lifetime_budget is set.",
				},
				"lifetime_budget": map[string]any{
					"type":        "STRING",
					"description": "Lifetime budget in minor units. Required unless daily_budget is set.",
				},
				"optimization_goal": map[string]any{
					"type":        "STRING",
					"description": "e.g. REACH, IMPRESSIONS, LINK_CLICKS, POST_ENGAGEMENT, THRUPLAY, OFFSITE_CONVERSIONS, QUALITY_LEAD, VALUE.",
				},
				"billing_event": map[string]any{
					"type":        "STRING",
					"description": "IMPRESSIONS, LINK_CLICKS or POST_ENGAGEMENT — must be valid for the chosen optimization_goal.",
				},
				"promoted_object": map[string]any{
					"type":        "OBJECT",
					"description": "Required for OFFSITE_CONVERSIONS/QUALITY_LEAD/VALUE. E.g. {\"pixel_id\":\"123\"} or {\"page_id\":\"123\"}.",
				},
				"bid_strategy": map[string]any{
					"type":        "STRING",
					"description": "e.g. LOWEST_COST_WITHOUT_CAP, COST_CAP, LOWEST_COST_WITH_MIN_ROAS.",
				},
				"bid_amount": map[string]any{
					"type":        "STRING",
					"description": "Bid amount in minor units when using a capped bid strategy.",
				},
				"targeting": map[string]any{
					"type":        "OBJECT",
					"description": "Targeting spec object, e.g. {\"geo_locations\":{\"countries\":[\"US\"]},\"age_min\":18}.",
				},
				"start_time": map[string]any{
					"type":        "STRING",
					"description": "Start time ISO 8601, e.g. 2026-09-01T00:00:00-0700.",
				},
				"end_time": map[string]any{
					"type":        "STRING",
					"description": "End time ISO 8601.",
				},
			},
			"required": []string{"name", "campaign_id", "optimization_goal", "billing_event"},
		},
		Handler: CreateAdSet,
	})

	h.RegisterTool(Tool{
		Name:        "update_adset",
		Description: "Updates mutable fields (name, status, budgets, bid_amount, targeting) on an existing ad set.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set to update.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "New ad set name.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "New status: ACTIVE or PAUSED.",
				},
				"daily_budget": map[string]any{
					"type":        "STRING",
					"description": "New daily budget in minor units.",
				},
				"lifetime_budget": map[string]any{
					"type":        "STRING",
					"description": "New lifetime budget in minor units.",
				},
				"bid_amount": map[string]any{
					"type":        "STRING",
					"description": "New bid amount in minor units.",
				},
				"targeting": map[string]any{
					"type":        "OBJECT",
					"description": "Replacement targeting spec object.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: UpdateAdSet,
	})

	h.RegisterTool(Tool{
		Name:        "delete_adset",
		Description: "Permanently deletes an ad set. Use with caution.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set to delete.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: DeleteAdSet,
	})

	h.RegisterTool(Tool{
		Name:        "activate_adset",
		Description: "Activates an ad set so it can spend and deliver.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: ActivateAdSet,
	})

	h.RegisterTool(Tool{
		Name:        "pause_adset",
		Description: "Pauses an ad set to stop it from spending.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: PauseAdSet,
	})

	// ===========================
	// ADS
	// ===========================

	h.RegisterTool(Tool{
		Name:        "list_ads",
		Description: "Returns all ads that belong to an ad set.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_set_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set.",
				},
			},
			"required": []string{"ad_set_id"},
		},
		Handler: ListAds,
	})

	h.RegisterTool(Tool{
		Name:        "get_ad",
		Description: "Returns a single ad with its configuration and creative reference.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: GetAd,
	})

	h.RegisterTool(Tool{
		Name:        "create_ad",
		Description: "Creates a new ad under an ad set using an existing creative. Requires name, ad_set_id and creative_id.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_account_id": map[string]any{
					"type":        "STRING",
					"description": "The ad account ID. Defaults to the connected account.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "The name of the ad.",
				},
				"ad_set_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the parent ad set.",
				},
				"creative_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of an existing ad creative to use.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "ACTIVE or PAUSED. Defaults to PAUSED.",
				},
			},
			"required": []string{"name", "ad_set_id", "creative_id"},
		},
		Handler: CreateAd,
	})

	h.RegisterTool(Tool{
		Name:        "update_ad",
		Description: "Updates mutable fields (name, status) on an existing ad.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad to update.",
				},
				"name": map[string]any{
					"type":        "STRING",
					"description": "New ad name.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "New status: ACTIVE or PAUSED.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: UpdateAd,
	})

	h.RegisterTool(Tool{
		Name:        "delete_ad",
		Description: "Permanently deletes an ad. Use with caution.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad to delete.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: DeleteAd,
	})

	h.RegisterTool(Tool{
		Name:        "activate_ad",
		Description: "Activates an ad so it can spend and deliver.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: ActivateAd,
	})

	h.RegisterTool(Tool{
		Name:        "pause_ad",
		Description: "Pauses an ad to stop it from spending.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: PauseAd,
	})

	// ===========================
	// INSIGHTS
	// ===========================

	h.RegisterTool(Tool{
		Name:        "get_campaign_insights",
		Description: "Returns performance metrics (spend, impressions, clicks, ctr, actions, roas) for a campaign.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date range preset, e.g. today, yesterday, last_7d, last_30d, this_month, last_month. Defaults to last_30d.",
				},
			},
			"required": []string{"campaign_id"},
		},
		Handler: GetCampaignInsights,
	})

	h.RegisterTool(Tool{
		Name:        "get_adset_insights",
		Description: "Returns performance metrics (spend, impressions, clicks, ctr, actions, roas) for an ad set.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"adset_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad set.",
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date range preset, e.g. today, yesterday, last_7d, last_30d, this_month, last_month. Defaults to last_30d.",
				},
			},
			"required": []string{"adset_id"},
		},
		Handler: GetAdSetInsights,
	})

	h.RegisterTool(Tool{
		Name:        "get_ad_insights",
		Description: "Returns performance metrics (spend, impressions, clicks, ctr, actions, roas) for a single ad.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"ad_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the ad.",
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date range preset, e.g. today, yesterday, last_7d, last_30d, this_month, last_month. Defaults to last_30d.",
				},
			},
			"required": []string{"ad_id"},
		},
		Handler: GetAdInsights,
	})

	h.RegisterTool(Tool{
		Name:        "compare_campaigns",
		Description: "Fetches insights for up to 10 campaigns at once so their performance can be compared side by side.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_ids": map[string]any{
					"type":        "ARRAY",
					"description": "The IDs of the campaigns to compare (max 10).",
					"items": map[string]any{
						"type": "STRING",
					},
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date range preset, e.g. today, yesterday, last_7d, last_30d, this_month, last_month. Defaults to last_30d.",
				},
			},
			"required": []string{"campaign_ids"},
		},
		Handler: CompareCampaigns,
	})

	RegisterAnalyticsTool(h)

	h.RegisterTool(Tool{
		Name:        "get_recommendations",
		Description: "Runs the optimization decision engine over all campaigns and returns one concrete recommended action per campaign (PAUSE_CAMPAIGN, DECREASE_BUDGET, INCREASE_BUDGET, REFRESH_CREATIVE or NO_ACTION) with a confidence score, the reason, and the suggested budget/status change. Use this when the user asks what to do, what to optimize, or which campaigns to fix.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date window: last_7d, last_30d (default), this_month, etc.",
				},
			},
			"required": []string{},
		},
		Handler: GetRecommendations,
	})

	h.RegisterTool(Tool{
		Name:        "optimize_campaign",
		Description: "Runs the full optimization cycle over campaigns or ad sets and proposes concrete actions with confidence scores. ALWAYS call with dry_run=true first and present the recommendations to the user; only use dry_run=false when the user explicitly asked to create optimization proposals. Nothing ever executes automatically — stored proposals wait for human approval.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"dry_run": map[string]any{
					"type":        "BOOLEAN",
					"description": "true (default): report only, change nothing. false: store PENDING proposals for approval.",
				},
				"level": map[string]any{
					"type":        "STRING",
					"description": "campaigns (default) or adsets.",
				},
				"date_preset": map[string]any{
					"type":        "STRING",
					"description": "Date window: last_7d, last_30d (default), this_month, etc.",
				},
			},
			"required": []string{},
		},
		Handler: OptimizeCampaign,
	})

	h.RegisterTool(Tool{
		Name:        "explain_action",
		Description: "Returns the full audit trail behind a stored optimization proposal: the reason, the rule that fired, its confidence, every evidence signal (metrics vs goal targets, pacing, fatigue), the before-snapshot, and any captured outcomes. Use this whenever the user asks why an action was taken or proposed.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"action_id": map[string]any{
					"type":        "STRING",
					"description": "The optimization action ID to explain.",
				},
			},
			"required": []string{"action_id"},
		},
		Handler: ExplainAction,
	})

	return h
}

func (h *ToolHandler) RegisterTool(tool Tool) {
	h.tools[tool.Name] = tool
}

func (h *ToolHandler) DeleteTool(name string) {
	delete(h.tools, name)
}

func (h *ToolHandler) ExecuteTool(
	ctx ToolContext,
	name string,
	args map[string]any,
) (any, error) {

	tool, ok := h.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool.Handler(ctx, args)
}

func (h *ToolHandler) GetTools() []Tool {

	list := make([]Tool, 0, len(h.tools))

	for _, t := range h.tools {
		list = append(list, t)
	}

	return list
}

// ===========================
// GEMINI SCHEMA TYPES
// ===========================

type GenerateContentRequest struct {
	SystemInstruction *Content     `json:"systemInstruction,omitempty"`
	Contents          []Content    `json:"contents"`
	Tools             []GeminiTool `json:"tools,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text string `json:"text,omitempty"`

	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

type GeminiTool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations"`
}

type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  ParameterSchema `json:"parameters"`
}

type ParameterSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]PropertySchema `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

type PropertySchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Items       *PropertySchema `json:"items,omitempty"`
}

type FunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

type FunctionResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// ===========================
// RESPONSE
// ===========================

type GenerateContentResponse struct {
	Candidates    []Candidate   `json:"candidates"`
	UsageMetadata UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string        `json:"modelVersion,omitempty"`
	ResponseID    string        `json:"responseId,omitempty"`
}

type Candidate struct {
	Content       Content `json:"content"`
	FinishReason  string  `json:"finishReason"`
	FinishMessage string  `json:"finishMessage,omitempty"`
	Index         int     `json:"index"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
}

// DeclarationFor converts a Tool into its Gemini function declaration schema.
func DeclarationFor(tool Tool) FunctionDeclaration {

	properties := make(map[string]PropertySchema)

	if props, ok := tool.Parameters["properties"].(map[string]any); ok {

		for name, value := range props {

			propMap, ok := value.(map[string]any)
			if !ok {
				continue
			}

			property := PropertySchema{}

			if t, ok := propMap["type"].(string); ok {
				property.Type = t
			}

			if d, ok := propMap["description"].(string); ok {
				property.Description = d
			}

			// Array item type (e.g. special_ad_categories)
			if itemMap, ok := propMap["items"].(map[string]any); ok {
				items := &PropertySchema{}

				if t, ok := itemMap["type"].(string); ok {
					items.Type = t
				}

				if d, ok := itemMap["description"].(string); ok {
					items.Description = d
				}

				property.Items = items
			}

			properties[name] = property
		}
	}

	// Required fields
	required := []string{}

	switch req := tool.Parameters["required"].(type) {

	case []string:
		required = req

	case []any:
		for _, v := range req {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}

	// Parameter type
	paramType := "OBJECT"

	if t, ok := tool.Parameters["type"].(string); ok {
		paramType = t
	}

	return FunctionDeclaration{
		Name:        tool.Name,
		Description: tool.Description,
		Parameters: ParameterSchema{
			Type:       paramType,
			Properties: properties,
			Required:   required,
		},
	}
}

func ContentRequest(
	h *ToolHandler,
	history []Message,
) GenerateContentRequest {

	declarations := make([]FunctionDeclaration, 0)

	for _, tool := range h.GetTools() {
		declarations = append(declarations, DeclarationFor(tool))
	}

	contents := make([]Content, 0, len(history)+1)

	for _, msg := range history {

		// Gemini only accepts "user" and "model" roles.
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, Content{
			Role: role,
			Parts: []Part{
				{
					Text: msg.Content,
				},
			},
		})
	}

	return GenerateContentRequest{
		Contents: contents,
		Tools: []GeminiTool{
			{
				FunctionDeclarations: declarations,
			},
		},
	}
}
