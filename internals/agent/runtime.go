// Package agent implements the CBK agent runtime: an explicit
// goal → plan → action → observation → decision → completion loop
// on top of a Gemini function-calling provider and the Meta Ads tools.
package agent

import (
	"fmt"
	"strings"

	"github.com/mrigangha/cbk/internals/tools"
)

// ChatProvider is the model backend the runtime drives.
type ChatProvider interface {
	Chat(req tools.GenerateContentRequest) (*tools.GenerateContentResponse, error)
}

// PlanStep is one entry of the agent's working plan.
type PlanStep struct {
	Description string `json:"description"`
	Status      string `json:"status"` // pending | in_progress | done | failed
}

// Action is a single tool invocation requested by the model.
type Action struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// Observation is what came back after executing an action.
type Observation struct {
	Tool   string `json:"tool"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Decision is the agent's choice after observing results.
type Decision struct {
	Type      string `json:"type"` // "act" | "complete"
	Reasoning string `json:"reasoning,omitempty"`
}

// TraceEntry captures one full iteration of the loop.
type TraceEntry struct {
	Iteration    int           `json:"iteration"`
	Thought      string        `json:"thought,omitempty"`
	Actions      []Action      `json:"actions"`
	Observations []Observation `json:"observations"`
	Decision     Decision      `json:"decision"`
}

// Event types emitted by the runtime while a run is in flight.
const (
	EventIteration   = "iteration"   // a new loop iteration started
	EventThought     = "thought"     // model text alongside tool calls
	EventPlan        = "plan"        // the working plan changed
	EventAction      = "action"      // a tool call is about to execute
	EventObservation = "observation" // a tool result came back
)

// Event is a progress notification streamed while the run executes.
type Event struct {
	Type        string       `json:"type"`
	Iteration   int          `json:"iteration,omitempty"`
	Thought     string       `json:"thought,omitempty"`
	Plan        []PlanStep   `json:"plan,omitempty"`
	Action      *Action      `json:"action,omitempty"`
	Observation *Observation `json:"observation,omitempty"`
}

// RunResult is the outcome of a full agent run.
type RunResult struct {
	Status     string       `json:"status"` // completed | max_iterations_reached
	Goal       string       `json:"goal"`
	Response   string       `json:"response"`
	Iterations int          `json:"iterations"`
	Plan       []PlanStep   `json:"plan"`
	Trace      []TraceEntry `json:"trace"`
}

const planToolName = "update_plan"

const systemPrompt = `You are CBK, an autonomous marketing operations agent for Meta Ads.

Work strictly in this loop:

1. GOAL: Understand the user's goal. If it is ambiguous, choose the most reasonable interpretation.
2. PLAN: Maintain your working plan with the update_plan tool. Each step has a description and a status (pending, in_progress, done, failed). Call update_plan whenever you create or change the plan or a step status changes.
3. ACTION: Use the available Meta Ads tools to gather data or make changes. Batch only independent calls; wait for results of dependent calls.
4. OBSERVATION: Tool results come back to you as observations. Treat errors as observations too: adapt instead of repeating the same failing call.
5. DECISION: After every round of observations decide the next action, a plan update, or completion.
6. COMPLETION: When the goal is achieved (or clearly impossible), stop calling tools and write the final answer: what you did, the key numbers you found, and any recommended next steps.

Rules:
- Never invent campaign data. Only rely on tool observations.
- Budgets are in minor units (1000 = 10.00 in account currency).
- Destructive actions (delete_campaign, delete_adset, delete_ad) require the user to have explicitly asked for deletion; otherwise pause the object instead and say so.`

func planToolDeclaration() tools.FunctionDeclaration {
	return tools.DeclarationFor(tools.Tool{
		Name:        planToolName,
		Description: "Create or update your working plan. Provide the complete list of steps with their current statuses.",
		Parameters: map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"steps": map[string]any{
					"type":        "ARRAY",
					"description": "The full plan, in order.",
					"items": map[string]any{
						"type": "OBJECT",
					},
				},
			},
			"required": []string{"steps"},
		},
	})
}

// Runtime executes agent runs.
type Runtime struct {
	Provider      ChatProvider
	Tools         *tools.ToolHandler
	ToolCtx       tools.ToolContext
	MaxIterations int

	// ContextBlocks are appended verbatim to the system instruction.
	// Used to inject persistent context such as the active marketing goal.
	ContextBlocks []string

	// Events, when set, receives progress notifications during Run.
	// It is invoked synchronously on the caller's goroutine.
	Events func(Event)

	plan []PlanStep
}

func (r *Runtime) emit(e Event) {
	if r.Events != nil {
		r.Events(e)
	}
}

func NewRuntime(
	provider ChatProvider,
	handler *tools.ToolHandler,
	toolCtx tools.ToolContext,
) *Runtime {
	return &Runtime{
		Provider:      provider,
		Tools:         handler,
		ToolCtx:       toolCtx,
		MaxIterations: 15,
	}
}

// Run drives the goal to completion.
func (r *Runtime) Run(goal string, history []tools.Message) (*RunResult, error) {

	result := &RunResult{
		Goal:  goal,
		Plan:  []PlanStep{},
		Trace: []TraceEntry{},
	}

	request := r.buildRequest(goal, history)

	for i := 1; i <= r.MaxIterations; i++ {

		resp, err := r.Provider.Chat(request)
		if err != nil {
			return nil, err
		}

		if len(resp.Candidates) == 0 ||
			len(resp.Candidates[0].Content.Parts) == 0 {
			return nil, fmt.Errorf("empty model response")
		}

		parts := resp.Candidates[0].Content.Parts

		var thought string
		calls := make([]*tools.FunctionCall, 0)

		for _, part := range parts {
			if part.FunctionCall != nil {
				calls = append(calls, part.FunctionCall)
			}
			if part.Text != "" {
				thought += part.Text
			}
		}

		entry := TraceEntry{
			Iteration:    i,
			Thought:      thought,
			Actions:      []Action{},
			Observations: []Observation{},
		}

		// Completion: no further actions requested.
		if len(calls) == 0 {
			entry.Decision = Decision{
				Type:      "complete",
				Reasoning: thought,
			}
			result.Trace = append(result.Trace, entry)
			result.Status = "completed"
			result.Iterations = i
			result.Response = thought
			return result, nil
		}

		entry.Decision = Decision{Type: "act"}

		r.emit(Event{
			Type:      EventIteration,
			Iteration: i,
			Thought:   thought,
		})

		// Echo the full model turn back (preserves thought signatures).
		modelParts := make([]tools.Part, 0, len(parts))
		for _, part := range parts {
			modelParts = append(modelParts, tools.Part{
				Text:             part.Text,
				FunctionCall:     part.FunctionCall,
				ThoughtSignature: part.ThoughtSignature,
			})
		}

		// Execute every requested action and collect observations.
		toolParts := make([]tools.Part, 0, len(calls))

		for _, call := range calls {

			action := Action{Tool: call.Name, Args: call.Args}
			entry.Actions = append(entry.Actions, action)

			r.emit(Event{
				Type:      EventAction,
				Iteration: i,
				Action:    &action,
			})

			var obs Observation

			if call.Name == planToolName {
				obs = r.applyPlan(call.Args)
			} else {
				raw, err := r.Tools.ExecuteTool(r.ToolCtx, call.Name, call.Args)
				if err != nil {
					obs = Observation{Tool: call.Name, Error: err.Error()}
				} else {
					obs = Observation{Tool: call.Name, Result: raw}
				}
			}

			entry.Observations = append(entry.Observations, obs)

			r.emit(Event{
				Type:        EventObservation,
				Iteration:   i,
				Observation: &obs,
			})

			payload := map[string]any{}
			if obs.Error != "" {
				payload["error"] = obs.Error
			} else {
				payload["result"] = obs.Result
			}

			toolParts = append(toolParts, tools.Part{
				FunctionResponse: &tools.FunctionResponse{
					Name:     call.Name,
					Response: payload,
				},
			})
		}

		result.Plan = append([]PlanStep{}, r.plan...)

		result.Trace = append(result.Trace, entry)

		request.Contents = append(request.Contents,
			tools.Content{Role: "model", Parts: modelParts},
			tools.Content{Role: "tool", Parts: toolParts},
		)
	}

	result.Status = "max_iterations_reached"
	result.Iterations = r.MaxIterations
	return result, nil
}

// applyPlan handles the internal update_plan tool.
func (r *Runtime) applyPlan(args map[string]any) Observation {

	raw, ok := args["steps"].([]any)
	if !ok || len(raw) == 0 {
		return Observation{
			Tool:  planToolName,
			Error: "missing steps array",
		}
	}

	plan := make([]PlanStep, 0, len(raw))

	for _, item := range raw {

		stepMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		description, _ := stepMap["description"].(string)
		if description == "" {
			continue
		}

		status, _ := stepMap["status"].(string)

		switch status {
		case "pending", "in_progress", "done", "failed":
		default:
			status = "pending"
		}

		plan = append(plan, PlanStep{
			Description: description,
			Status:      status,
		})
	}

	if len(plan) == 0 {
		return Observation{
			Tool:  planToolName,
			Error: "no valid steps provided",
		}
	}

	r.plan = plan

	r.emit(Event{
		Type: EventPlan,
		Plan: append([]PlanStep{}, plan...),
	})

	return Observation{
		Tool: planToolName,
		Result: map[string]any{
			"plan_updated": true,
			"step_count":   len(plan),
		},
	}
}

// buildRequest assembles the initial Gemini request with the agent
// system instruction and the full tool surface (Meta tools + update_plan).
func (r *Runtime) buildRequest(goal string, history []tools.Message) tools.GenerateContentRequest {

	declarations := []tools.FunctionDeclaration{planToolDeclaration()}

	for _, tool := range r.Tools.GetTools() {
		declarations = append(declarations, tools.DeclarationFor(tool))
	}

	contents := make([]tools.Content, 0, len(history)+1)

	for _, msg := range history {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, tools.Content{
			Role: role,
			Parts: []tools.Part{
				{Text: msg.Content},
			},
		})
	}

	contents = append(contents, tools.Content{
		Role: "user",
		Parts: []tools.Part{
			{Text: goal},
		},
	})

	systemText := systemPrompt

	for _, block := range r.ContextBlocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		systemText += "\n\n" + block
	}

	return tools.GenerateContentRequest{
		SystemInstruction: &tools.Content{
			Role: "system",
			Parts: []tools.Part{
				{Text: systemText},
			},
		},
		Contents: contents,
		Tools: []tools.GeminiTool{
			{FunctionDeclarations: declarations},
		},
	}
}
