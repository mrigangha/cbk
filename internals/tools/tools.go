package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

type ToolHandler struct {
	tools map[string]Tool
}

func NewToolHandler() *ToolHandler {
	h := &ToolHandler{
		tools: make(map[string]Tool),
	}

	h.RegisterTool(Tool{
		Name:        "get_campaigns",
		Description: "Returns all campaigns for the connected Meta Ads account.",
		Parameters: map[string]any{
			"type":       "OBJECT",
			"properties": map[string]any{},
		},
		Handler: GetCampaigns,
	})

	h.RegisterTool(Tool{
		Name:        "get_campaign_details",
		Description: "Returns performance metrics for a Meta Ads campaign.",
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
		Handler: CampaignInsights,
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
		Name:        "update_campaign_status",
		Description: "Pauses or activates a campaign by setting its status to ACTIVE, PAUSED or ARCHIVED.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"campaign_id": map[string]any{
					"type":        "STRING",
					"description": "The ID of the campaign.",
				},
				"status": map[string]any{
					"type":        "STRING",
					"description": "ACTIVE, PAUSED or ARCHIVED.",
				},
			},
			"required": []string{"campaign_id", "status"},
		},
		Handler: UpdateCampaignStatus,
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
		Name:        "get_adsets",
		Description: "Returns all ad sets for a campaign.",
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
		Handler: GetAdSets,
	})

	h.RegisterTool(Tool{
		Name:        "get_ads",
		Description: "Returns all ads for an ad set.",
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
		Handler: GetAds,
	})

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

type CampaignResponse struct {
	Data []Campaign `json:"data"`
}

type Campaign struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func GetCampaigns(
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

	url := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/act_%s/campaigns?fields=id,name,status,objective",
		adAccountID,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+ctx.AccessToken,
	)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {

		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"meta error (%d): %s",
			resp.StatusCode,
			string(body),
		)
	}

	var campaigns CampaignResponse

	if err := json.NewDecoder(resp.Body).Decode(&campaigns); err != nil {
		return nil, err
	}

	return campaigns.Data, nil
}

func CampaignInsights(ctx ToolContext,
	args map[string]any,
) (any, error) {

	campaignID, ok := args["campaign_id"].(string)
	if !ok || campaignID == "" {
		return nil, fmt.Errorf("missing campaign_id")
	}

	var accessToken string = ctx.AccessToken

	graphURL := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/%s?fields=id,name,status,objective,daily_budget,lifetime_budget,created_time,updated_time,special_ad_categories",
		campaignID,
	)

	req, err := http.NewRequest(http.MethodGet, graphURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result, nil
}

type GenerateContentRequest struct {
	Contents []Content    `json:"contents"`
	Tools    []GeminiTool `json:"tools,omitempty"`
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

func ContentRequest(
	h *ToolHandler,
	history []Message,
) GenerateContentRequest {

	declarations := make([]FunctionDeclaration, 0)

	for _, tool := range h.GetTools() {

		properties := make(map[string]PropertySchema)

		// Properties
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

		declarations = append(declarations, FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters: ParameterSchema{
				Type:       paramType,
				Properties: properties,
				Required:   required,
			},
		})
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
