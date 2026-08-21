package core

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/mrigangha/cbk/internals/ai/cloud"
	"github.com/mrigangha/cbk/internals/tools"
)

type AgentRequest struct {
	ModelName string          `json:"model_name"`
	Prompt    string          `json:"prompt"`
	Messages  []tools.Message `json:"messages,omitempty"`
}

type AgentStep struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result any            `json:"result"`
}

type AgentResponse struct {
	Response string      `json:"response"`
	Steps    []AgentStep `json:"steps"`
}

func (a *Api) RunAgent(w http.ResponseWriter, r *http.Request) {

	// Decode request
	var agentReq AgentRequest

	err := json.NewDecoder(r.Body).Decode(&agentReq)
	if err != nil {
		http.Error(
			w,
			"invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	if agentReq.ModelName == "" || agentReq.Prompt == "" {
		http.Error(
			w,
			"model_name and prompt required",
			http.StatusBadRequest,
		)
		return
	}

	// Get authenticated user
	user := a.GetUser(r)

	if user == nil {
		http.Error(
			w,
			"Unauthorized",
			http.StatusUnauthorized,
		)
		return
	}

	// Get full user
	user, err = a.GetUserByEmail(user.Email)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	// Get Gemini API key
	var apiKey string
	var modelName string

	err = a.db.QueryRow(`
		SELECT provider_name, api_key
		FROM providers
		WHERE user_id = ?
		AND provider_name = ?
	`,
		user.ID,
		agentReq.ModelName,
	).Scan(
		&modelName,
		&apiKey,
	)

	if err == sql.ErrNoRows {

		http.Error(
			w,
			"provider not found",
			http.StatusNotFound,
		)

		return
	}

	if err != nil {

		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)

		return
	}

	// Meta access token and default ad account for the user
	var accessToken string
	var adAccountID string

	err = a.db.QueryRow(`
		SELECT access_token, ad_account_id
		FROM meta_ads_accounts
		WHERE user_id = ?
		LIMIT 1
	`,
		user.ID,
	).Scan(&accessToken, &adAccountID)

	if err != nil {
		http.Error(
			w,
			"no connected Meta Ads account",
			http.StatusBadRequest,
		)
		return
	}

	// Create tool handler
	handler := tools.NewToolHandler()

	// Build conversation from history + current prompt
	history := agentReq.Messages
	history = append(history, tools.Message{
		Role:    "user",
		Content: agentReq.Prompt,
	})

	request := tools.ContentRequest(
		handler,
		history,
	)

	// Gemini provider
	provider := cloud.NewProvider(
		apiKey,
		agentReq.ModelName,
		"https://generativelanguage.googleapis.com/v1beta",
	)

	steps := make([]AgentStep, 0)

	const maxIterations = 10

	for i := 0; i < maxIterations; i++ {

		resp, err := provider.Chat(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if len(resp.Candidates) == 0 ||
			len(resp.Candidates[0].Content.Parts) == 0 {

			http.Error(w, "empty model response", http.StatusInternalServerError)
			return
		}

		part := resp.Candidates[0].Content.Parts[0]

		// Finished: model returned text
		if part.FunctionCall == nil {

			json.NewEncoder(w).Encode(AgentResponse{
				Response: part.Text,
				Steps:    steps,
			})
			return
		}

		// Execute tool
		result, err := handler.ExecuteTool(
			tools.ToolContext{
				AccessToken: accessToken,
				AdAccountID: adAccountID,
			},
			part.FunctionCall.Name,
			part.FunctionCall.Args,
		)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Record the tool call for the frontend trace
		steps = append(steps, AgentStep{
			Tool:   part.FunctionCall.Name,
			Args:   part.FunctionCall.Args,
			Result: result,
		})

		// Continue conversation
		request = tools.GenerateContentRequest{
			Contents: append(
				request.Contents,
				tools.Content{
					Role: "model",
					Parts: []tools.Part{
						{
							FunctionCall:     part.FunctionCall,
							ThoughtSignature: part.ThoughtSignature,
						},
					},
				},
				tools.Content{
					Role: "tool",
					Parts: []tools.Part{
						{
							FunctionResponse: &tools.FunctionResponse{
								Name: part.FunctionCall.Name,
								Response: map[string]any{
									"result": result,
								},
							},
						},
					},
				},
			),
		}
	}

	http.Error(
		w,
		"maximum tool iterations exceeded",
		http.StatusInternalServerError,
	)
}