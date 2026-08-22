package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mrigangha/cbk/internals/tools"
)

// openRouterProvider speaks the OpenAI-compatible chat completions API
// exposed by OpenRouter, translating to/from the internal Gemini-style
// types so the agent runtime stays provider-agnostic.
type openRouterProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func newOpenRouterProvider(apiKey, model, baseURL string) *openRouterProvider {
	return &openRouterProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
	}
}

// ===========================
// OPENAI WIRE TYPES
// ===========================

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"` // string or null (tool_calls only)
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	} `json:"function"`
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ===========================
// CONVERSION: INTERNAL -> OPENAI
// ===========================

var openAISchemaTypeLower = map[string]string{
	"OBJECT":  "object",
	"STRING":  "string",
	"ARRAY":   "array",
	"NUMBER":  "number",
	"INTEGER": "integer",
	"BOOLEAN": "boolean",
}

// lowerSchema converts Gemini's uppercase JSON-schema types into the
// lowercase form used by OpenAI-compatible APIs.
func lowerSchema(v any) any {

	switch typed := v.(type) {

	case tools.ParameterSchema:
		out := map[string]any{}
		out["type"] = lowerType(typed.Type)

		props := map[string]any{}
		for name, prop := range typed.Properties {
			props[name] = lowerProperty(prop)
		}
		if len(props) > 0 {
			out["properties"] = props
		}
		if len(typed.Required) > 0 {
			out["required"] = typed.Required
		}
		return out

	case *tools.PropertySchema:
		if typed == nil {
			return nil
		}
		return lowerProperty(*typed)
	}

	return v
}

func lowerType(t string) string {
	if low, ok := openAISchemaTypeLower[t]; ok {
		return low
	}
	return strings.ToLower(t)
}

func lowerProperty(p tools.PropertySchema) map[string]any {

	out := map[string]any{
		"type": lowerType(p.Type),
	}

	if p.Description != "" {
		out["description"] = p.Description
	}

	if p.Items != nil {
		out["items"] = lowerProperty(*p.Items)
	}

	return out
}

func (p *openRouterProvider) convertRequest(
	req tools.GenerateContentRequest,
) openAIChatRequest {

	out := openAIChatRequest{Model: p.model}

	// System instruction.
	if req.SystemInstruction != nil {
		if text := concatText(req.SystemInstruction.Parts); text != "" {
			out.Messages = append(out.Messages, openAIMessage{
				Role:    "system",
				Content: text,
			})
		}
	}

	// Tool declarations.
	for _, geminiTool := range req.Tools {
		for _, decl := range geminiTool.FunctionDeclarations {

			tool := openAITool{Type: "function"}
			tool.Function.Name = decl.Name
			tool.Function.Description = decl.Description

			if params, ok := lowerSchema(decl.Parameters).(map[string]any); ok {
				tool.Function.Parameters = params
			}

			out.Tools = append(out.Tools, tool)
		}
	}

	// Conversation contents. OpenAI pairs assistant tool_calls with
	// follow-up tool messages by id; assign a FIFO of synthetic ids
	// because Gemini-style flows carry none.
	pendingIDs := []string{}

	callID := func(existing string) string {
		if existing != "" {
			return existing
		}
		id := fmt.Sprintf("call_%d", len(pendingIDs))
		return id
	}

	nextID := func() string {
		if len(pendingIDs) == 0 {
			return fmt.Sprintf("call_x%d", len(out.Messages))
		}
		id := pendingIDs[0]
		pendingIDs = pendingIDs[1:]
		return id
	}

	for _, content := range req.Contents {

		switch content.Role {

		case "user":
			if text := concatText(content.Parts); text != "" {
				out.Messages = append(out.Messages, openAIMessage{
					Role:    "user",
					Content: text,
				})
			}

		case "model", "assistant":

			msg := openAIMessage{Role: "assistant"}

			var texts []string

			for _, part := range content.Parts {

				if part.FunctionCall != nil {

					id := callID(part.FunctionCall.ID)
					pendingIDs = append(pendingIDs, id)

					args, _ := json.Marshal(part.FunctionCall.Args)

					msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
						ID:   id,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      part.FunctionCall.Name,
							Arguments: string(args),
						},
					})

				} else if part.Text != "" {
					texts = append(texts, part.Text)
				}
			}

			if len(texts) > 0 {
				msg.Content = joinTexts(texts)
			} else if len(msg.ToolCalls) == 0 {
				continue // nothing usable in this turn
			}

			out.Messages = append(out.Messages, msg)

		case "tool":
			for _, part := range content.Parts {

				if part.FunctionResponse == nil {
					continue
				}

				payload, _ := json.Marshal(part.FunctionResponse.Response)

				out.Messages = append(out.Messages, openAIMessage{
					Role:       "tool",
					ToolCallID: nextID(),
					Name:       part.FunctionResponse.Name,
					Content:    string(payload),
				})
			}
		}
	}

	return out
}

func concatText(parts []tools.Part) string {

	var b []string

	for _, part := range parts {
		if part.Text != "" {
			b = append(b, part.Text)
		}
	}

	return joinTexts(b)
}

func joinTexts(texts []string) string {

	result := ""

	for i, t := range texts {
		if i > 0 {
			result += "\n"
		}
		result += t
	}

	return result
}

// ===========================
// HTTP + RESPONSE MAPPING
// ===========================

func (p *openRouterProvider) Chat(
	req tools.GenerateContentRequest,
) (*tools.GenerateContentResponse, error) {

	body, err := json.Marshal(p.convertRequest(req))
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(
		http.MethodPost,
		p.baseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("X-Title", "CBK Marketing OS")

	client := &http.Client{}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"openrouter error (%d): %s",
			httpResp.StatusCode,
			string(respBody),
		)
	}

	var raw openAIChatResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}

	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("openrouter returned no choices")
	}

	choice := raw.Choices[0]

	parts := make([]tools.Part, 0, 1+len(choice.Message.ToolCalls))

	if choice.Message.Content != "" {
		parts = append(parts, tools.Part{Text: choice.Message.Content})
	}

	for _, tc := range choice.Message.ToolCalls {

		args := map[string]any{}

		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf(
					"invalid tool arguments for %s: %w",
					tc.Function.Name,
					err,
				)
			}
		}

		parts = append(parts, tools.Part{
			FunctionCall: &tools.FunctionCall{
				Name: tc.Function.Name,
				Args: args,
				ID:   tc.ID,
			},
		})
	}

	if len(parts) == 0 {
		return nil, fmt.Errorf("openrouter returned an empty message")
	}

	return &tools.GenerateContentResponse{
		Candidates: []tools.Candidate{
			{
				Content: tools.Content{
					Role:  "model",
					Parts: parts,
				},
				FinishReason: strings.ToUpper(choice.FinishReason),
			},
		},
		UsageMetadata: tools.UsageMetadata{
			PromptTokenCount:     raw.Usage.PromptTokens,
			CandidatesTokenCount: raw.Usage.CompletionTokens,
			TotalTokenCount:      raw.Usage.TotalTokens,
		},
	}, nil
}

// GenerateText performs a simple one-shot completion without tools.
func (p *openRouterProvider) GenerateText(prompt string) (string, error) {

	resp, err := p.Chat(tools.GenerateContentRequest{
		Contents: []tools.Content{
			{
				Role: "user",
				Parts: []tools.Part{
					{Text: prompt},
				},
			},
		},
	})

	if err != nil {
		return "", err
	}

	if len(resp.Candidates) == 0 ||
		len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no model output found in response")
	}

	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			return part.Text, nil
		}
	}

	return "", fmt.Errorf("no model output found in response")
}
