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
	SessionID int64           `json:"session_id,omitempty"`
}

type AgentStep struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type AgentResponse struct {
	SessionID int64       `json:"session_id"`
	Response  string      `json:"response"`
	Steps     []AgentStep `json:"steps"`
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

	// Resolve or create the chat session for history.
	sessionID := agentReq.SessionID

	if sessionID > 0 {
		var ownerID int64
		err = a.db.QueryRow(`
			SELECT user_id FROM agent_sessions WHERE id = ?
		`, sessionID).Scan(&ownerID)

		if err != nil || ownerID != user.ID {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	} else {
		title := agentReq.Prompt
		if len(title) > 60 {
			title = title[:60] + "..."
		}

		res, err := a.db.Exec(`
			INSERT INTO agent_sessions (user_id, title)
			VALUES (?, ?)
		`, user.ID, title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sessionID, _ = res.LastInsertId()
	}

	// Persist the user prompt.
	_, err = a.db.Exec(`
		INSERT INTO agent_messages (session_id, role, content)
		VALUES (?, 'user', ?)
	`, sessionID, agentReq.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

		parts := resp.Candidates[0].Content.Parts

		// Collect text and every function call from all parts.
		var text string
		calls := make([]*tools.FunctionCall, 0)

		for _, part := range parts {
			if part.FunctionCall != nil {
				calls = append(calls, part.FunctionCall)
			}
			if part.Text != "" {
				text += part.Text
			}
		}

		// Finished: model answered with text only.
		if len(calls) == 0 {

			// Persist the assistant response with its tool trace.
			stepsJSON, _ := json.Marshal(steps)

			_, err = a.db.Exec(`
				INSERT INTO agent_messages (session_id, role, content, steps, model_name)
				VALUES (?, 'assistant', ?, ?, ?)
			`, sessionID, text, string(stepsJSON), agentReq.ModelName)
			if err == nil {
				a.db.Exec(`
					UPDATE agent_sessions
					SET updated_at = CURRENT_TIMESTAMP
					WHERE id = ?
				`, sessionID)
			}

			json.NewEncoder(w).Encode(AgentResponse{
				SessionID: sessionID,
				Response:  text,
				Steps:     steps,
			})
			return
		}

		// Echo the full model turn back (preserves thought signatures).
		modelParts := make([]tools.Part, 0, len(parts))

		for _, part := range parts {
			modelParts = append(modelParts, tools.Part{
				Text:             part.Text,
				FunctionCall:     part.FunctionCall,
				ThoughtSignature: part.ThoughtSignature,
			})
		}

		// Execute every requested tool and collect responses.
		toolParts := make([]tools.Part, 0, len(calls))

		for _, call := range calls {

			result, toolErr := handler.ExecuteTool(
				tools.ToolContext{
					AccessToken: accessToken,
					AdAccountID: adAccountID,
				},
				call.Name,
				call.Args,
			)

			payload := map[string]any{}

			if toolErr != nil {
				// Feed failures back so the model can recover
				// instead of aborting the whole request.
				payload["error"] = toolErr.Error()
			} else {
				payload["result"] = result
			}

			step := AgentStep{
				Tool:   call.Name,
				Args:   call.Args,
				Result: result,
			}

			if toolErr != nil {
				step.Error = toolErr.Error()
			}

			steps = append(steps, step)

			toolParts = append(toolParts, tools.Part{
				FunctionResponse: &tools.FunctionResponse{
					Name:     call.Name,
					Response: payload,
				},
			})
		}

		request.Contents = append(request.Contents,
			tools.Content{
				Role:  "model",
				Parts: modelParts,
			},
			tools.Content{
				Role:  "tool",
				Parts: toolParts,
			},
		)
	}

	http.Error(
		w,
		"maximum tool iterations exceeded",
		http.StatusInternalServerError,
	)
}