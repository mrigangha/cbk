package cloud

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrigangha/cbk/internals/tools"
)

func TestNew_UnknownType(t *testing.T) {
	_, err := New("WATGPT", "key", "model")
	if err == nil || !strings.Contains(err.Error(), "unsupported provider type") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestNew_EmptyTypeDefaultsToGemini(t *testing.T) {
	p, err := New("", "key", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*geminiProvider); !ok {
		t.Fatalf("expected geminiProvider, got %T", p)
	}
}

func TestOpenRouter_RequestConversion(t *testing.T) {

	var gotBody map[string]any
	var gotAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if gotAuth = r.Header.Get("Authorization"); gotAuth != "Bearer or-key" {
			t.Errorf("auth header = %q", gotAuth)
		}

		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("invalid request body: %v", err)
		}

		writeJSON(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer ts.Close()

	p := newOpenRouterProvider("or-key", "openai/gpt-4o-mini", ts.URL)

	req := tools.GenerateContentRequest{
		SystemInstruction: &tools.Content{
			Role:  "system",
			Parts: []tools.Part{{Text: "You are an agent."}},
		},
		Contents: []tools.Content{
			{Role: "user", Parts: []tools.Part{{Text: "List campaigns"}}},
			{Role: "model", Parts: []tools.Part{
				{FunctionCall: &tools.FunctionCall{Name: "list_campaigns", Args: map[string]any{}}},
			}},
			{Role: "tool", Parts: []tools.Part{
				{FunctionResponse: &tools.FunctionResponse{
					Name:     "list_campaigns",
					Response: map[string]any{"result": "ok"},
				}},
			}},
		},
		Tools: []tools.GeminiTool{
			{
				FunctionDeclarations: []tools.FunctionDeclaration{
					{
						Name:        "create_adset",
						Description: "Create an ad set",
						Parameters: tools.ParameterSchema{
							Type: "OBJECT",
							Properties: map[string]tools.PropertySchema{
								"name":   {Type: "STRING", Description: "Name"},
								"budget": {Type: "NUMBER"},
								"days":   {Type: "ARRAY", Items: &tools.PropertySchema{Type: "STRING"}},
							},
							Required: []string{"name"},
						},
					},
				},
			},
		},
	}

	if _, err := p.Chat(req); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	// Model field.
	if gotBody["model"] != "openai/gpt-4o-mini" {
		t.Errorf("model = %v", gotBody["model"])
	}

	messages := gotBody["messages"].([]any)

	// system, user, assistant(tool_calls), tool.
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4: %s", len(messages), mustJSON(gotBody))
	}

	if messages[0].(map[string]any)["role"] != "system" ||
		messages[0].(map[string]any)["content"] != "You are an agent." {
		t.Errorf("system message wrong: %v", messages[0])
	}

	assistant := messages[2].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call0 := calls[0].(map[string]any)
	fn := call0["function"].(map[string]any)

	if fn["name"] != "list_campaigns" {
		t.Errorf("tool call name = %v", fn["name"])
	}
	if fn["arguments"] != "{}" {
		t.Errorf("arguments = %v, want JSON object string", fn["arguments"])
	}

	toolMsg := messages[3].(map[string]any)
	if toolMsg["role"] != "tool" ||
		toolMsg["tool_call_id"] != call0["id"] {
		t.Errorf("tool message not paired with tool_call id: %v vs %v",
			toolMsg["tool_call_id"], call0["id"])
	}

	// Tool schema lowered to OpenAI style.
	toolsArr := gotBody["tools"].([]any)
	decl := toolsArr[0].(map[string]any)["function"].(map[string]any)
	params := decl["parameters"].(map[string]any)

	if params["type"] != "object" {
		t.Errorf("parameters.type = %v, want object", params["type"])
	}

	props := params["properties"].(map[string]any)
	if props["name"].(map[string]any)["type"] != "string" {
		t.Errorf("property type not lowered: %v", props["name"])
	}
	if props["days"].(map[string]any)["items"].(map[string]any)["type"] != "string" {
		t.Errorf("items type not lowered: %v", props["days"])
	}
	if params["required"].([]any)[0] != "name" {
		t.Errorf("required lost: %v", params["required"])
	}
	if props["name"].(map[string]any)["description"] != "Name" {
		t.Errorf("description lost: %v", props["name"])
	}
}

func TestOpenRouter_ResponseMapping(t *testing.T) {

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role":    "assistant",
					"content": "Checking your campaigns.",
					"tool_calls": []any{map[string]any{
						"id":   "call_abc",
						"type": "function",
						"function": map[string]any{
							"name":      "pause_campaign",
							"arguments": `{"campaign_id":"123"}`,
						},
					}},
				},
			}},
			"usage": map[string]any{
				"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			},
		})
	}))
	defer ts.Close()

	p := newOpenRouterProvider("k", "m", ts.URL)

	resp, err := p.Chat(tools.GenerateContentRequest{
		Contents: []tools.Content{
			{Role: "user", Parts: []tools.Part{{Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	parts := resp.Candidates[0].Content.Parts

	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text + tool call)", len(parts))
	}

	if parts[0].Text != "Checking your campaigns." {
		t.Errorf("text part = %q", parts[0].Text)
	}

	fc := parts[1].FunctionCall
	if fc == nil || fc.Name != "pause_campaign" || fc.ID != "call_abc" {
		t.Fatalf("function call mapped wrong: %+v", fc)
	}
	if fc.Args["campaign_id"] != "123" {
		t.Errorf("args not parsed: %v", fc.Args)
	}

	if resp.UsageMetadata.TotalTokenCount != 15 {
		t.Errorf("usage = %d", resp.UsageMetadata.TotalTokenCount)
	}
}

func TestOpenRouter_GenerateText(t *testing.T) {

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "Hello there."},
			}},
		})
	}))
	defer ts.Close()

	p := newOpenRouterProvider("k", "m", ts.URL)

	out, err := p.GenerateText("Say hi")
	if err != nil {
		t.Fatalf("GenerateText error: %v", err)
	}

	if out != "Hello there." {
		t.Errorf("output = %q", out)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}
