package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrigangha/cbk/internals/tools"
)

// stubProvider replays queued model responses and records requests.
type stubProvider struct {
	responses []*tools.GenerateContentResponse
	requests  []tools.GenerateContentRequest
	calls     int
}

func (s *stubProvider) Chat(
	req tools.GenerateContentRequest,
) (*tools.GenerateContentResponse, error) {

	s.requests = append(s.requests, req)

	if s.calls >= len(s.responses) {
		return nil, io.EOF
	}

	resp := s.responses[s.calls]
	s.calls++

	return resp, nil
}

func funcCallResponse(name string, args map[string]any) *tools.GenerateContentResponse {
	return &tools.GenerateContentResponse{
		Candidates: []tools.Candidate{
			{
				Content: tools.Content{
					Role: "model",
					Parts: []tools.Part{
						{FunctionCall: &tools.FunctionCall{Name: name, Args: args}},
					},
				},
			},
		},
	}
}

func textResponse(text string) *tools.GenerateContentResponse {
	return &tools.GenerateContentResponse{
		Candidates: []tools.Candidate{
			{
				Content: tools.Content{
					Role: "model",
					Parts: []tools.Part{
						{Text: text},
					},
				},
			},
		},
	}
}

// newTestRuntime wires a runtime against a mock Meta Graph API.
func newTestRuntime(t *testing.T, p ChatProvider) *Runtime {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/act_123/campaigns" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []any{
					map[string]any{"id": "c1", "name": "Camp", "status": "PAUSED"},
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))

	tools.SetGraphBaseURL(ts.URL)
	t.Cleanup(func() {
		tools.SetGraphBaseURL("https://graph.facebook.com/v23.0")
		ts.Close()
	})

	rt := NewRuntime(p, tools.NewToolHandler(), tools.ToolContext{
		AccessToken: "test-token",
		AdAccountID: "123",
	})

	return rt
}

func TestRun_PlanThenActThenComplete(t *testing.T) {

	stub := &stubProvider{
		responses: []*tools.GenerateContentResponse{
			funcCallResponse("update_plan", map[string]any{
				"steps": []any{
					map[string]any{"description": "List campaigns", "status": "in_progress"},
					map[string]any{"description": "Summarize", "status": "pending"},
				},
			}),
			funcCallResponse("list_campaigns", map[string]any{}),
			textResponse("You have 1 paused campaign."),
		},
	}

	rt := newTestRuntime(t, stub)

	result, err := rt.Run("Show my campaigns", nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("status = %q, want completed", result.Status)
	}

	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}

	if result.Response != "You have 1 paused campaign." {
		t.Errorf("response = %q", result.Response)
	}

	if len(result.Plan) != 2 || result.Plan[0].Description != "List campaigns" {
		t.Errorf("plan not recorded: %+v", result.Plan)
	}

	if len(result.Trace) != 3 {
		t.Fatalf("trace entries = %d, want 3", len(result.Trace))
	}

	if result.Trace[0].Decision.Type != "act" ||
		result.Trace[2].Decision.Type != "complete" {
		t.Errorf("unexpected decisions: %v %v",
			result.Trace[0].Decision.Type,
			result.Trace[2].Decision.Type)
	}

	// The tool observation must carry the campaign data.
	obs := result.Trace[1].Observations[0]
	if obs.Error != "" {
		t.Fatalf("unexpected observation error: %q", obs.Error)
	}

	data, ok := obs.Result.([]any)
	if !ok || len(data) != 1 {
		t.Errorf("expected campaign list observation, got %#v", obs.Result)
	}

	// First request must include the system instruction and update_plan.
	first := stub.requests[0]
	if first.SystemInstruction == nil ||
		!strings.Contains(first.SystemInstruction.Parts[0].Text, "GOAL") {
		t.Error("system instruction missing from first request")
	}

	foundPlanDecl := false
	for _, d := range first.Tools[0].FunctionDeclarations {
		if d.Name == "update_plan" {
			foundPlanDecl = true
		}
	}
	if !foundPlanDecl {
		t.Error("update_plan declaration missing")
	}

	// Third request must contain the function responses for iteration 2.
	last := stub.requests[len(stub.requests)-1]
	var sawToolRole bool
	for _, c := range last.Contents {
		if c.Role == "tool" && len(c.Parts) > 0 && c.Parts[0].FunctionResponse != nil {
			sawToolRole = true
		}
	}
	if !sawToolRole {
		t.Error("function responses not appended to conversation")
	}
}

func TestRun_ToolErrorFedBackToModel(t *testing.T) {

	stub := &stubProvider{
		responses: []*tools.GenerateContentResponse{
			// get_campaign without campaign_id -> handler validation error.
			funcCallResponse("get_campaign", map[string]any{}),
			textResponse("The campaign id was missing; I stopped."),
		},
	}

	rt := newTestRuntime(t, stub)

	result, err := rt.Run("Get campaign details", nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed", result.Status)
	}

	obs := result.Trace[0].Observations[0]
	if obs.Error == "" || !strings.Contains(obs.Error, "missing campaign_id") {
		t.Fatalf("expected tool error observation, got %+v", obs)
	}

	// The follow-up request must contain the error payload so the model adapts.
	last := stub.requests[len(stub.requests)-1]

	foundErr := false
	for _, c := range last.Contents {
		if c.Role != "tool" {
			continue
		}
		for _, p := range c.Parts {
			if p.FunctionResponse == nil {
				continue
			}
			payload, _ := p.FunctionResponse.Response.(map[string]any)
			if msg, ok := payload["error"].(string); ok &&
				strings.Contains(msg, "missing campaign_id") {
				foundErr = true
			}
		}
	}

	if !foundErr {
		t.Error("error observation not fed back to the model")
	}
}

func TestRun_MaxIterationsReached(t *testing.T) {

	stub := &stubProvider{
		responses: []*tools.GenerateContentResponse{
			funcCallResponse("list_campaigns", map[string]any{}),
			funcCallResponse("list_campaigns", map[string]any{}),
			funcCallResponse("list_campaigns", map[string]any{}),
		},
	}

	rt := newTestRuntime(t, stub)
	rt.MaxIterations = 3

	result, err := rt.Run("Loop forever", nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Status != "max_iterations_reached" {
		t.Errorf("status = %q, want max_iterations_reached", result.Status)
	}

	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}

	if len(result.Trace) != 3 {
		t.Errorf("trace entries = %d, want 3", len(result.Trace))
	}
}

func TestRun_ContextBlocksInSystemInstruction(t *testing.T) {

	stub := &stubProvider{
		responses: []*tools.GenerateContentResponse{
			textResponse("done"),
		},
	}

	rt := newTestRuntime(t, stub)
	rt.ContextBlocks = []string{
		"MARKETING GOAL (persistent objective): Name: Bangalore Leads, Objective: LEADS",
	}

	result, err := rt.Run("hello", nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q", result.Status)
	}

	sys := stub.requests[0].SystemInstruction.Parts[0].Text

	if !strings.Contains(sys, "MARKETING GOAL") {
		t.Error("context block missing from system instruction")
	}

	if !strings.Contains(sys, "1. GOAL") {
		t.Error("base protocol lost when context blocks are set")
	}
}

func TestApplyPlan_Validation(t *testing.T) {

	rt := &Runtime{}

	// Missing steps -> error observation.
	obs := rt.applyPlan(map[string]any{})
	if obs.Error == "" {
		t.Fatal("expected error for missing steps")
	}

	// Invalid statuses normalized to pending.
	obs = rt.applyPlan(map[string]any{
		"steps": []any{
			map[string]any{"description": "Step A", "status": "bogus"},
			map[string]any{"description": "Step B", "status": "done"},
			map[string]any{"no_description": true},
		},
	})

	if obs.Error != "" {
		t.Fatalf("unexpected error: %q", obs.Error)
	}

	if len(rt.plan) != 2 {
		t.Fatalf("plan length = %d, want 2", len(rt.plan))
	}

	if rt.plan[0].Status != "pending" {
		t.Errorf("invalid status not normalized: %q", rt.plan[0].Status)
	}

	if rt.plan[1].Status != "done" {
		t.Errorf("valid status changed: %q", rt.plan[1].Status)
	}
}
