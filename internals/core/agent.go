package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mrigangha/cbk/internals/agent"
	"github.com/mrigangha/cbk/internals/ai/cloud"
	"github.com/mrigangha/cbk/internals/tools"
)

type AgentRequest struct {
	ModelName string          `json:"model_name"`
	Prompt    string          `json:"prompt"`
	Message   string          `json:"message"` // alias for prompt
	Messages  []tools.Message `json:"messages,omitempty"`
	SessionID int64           `json:"session_id,omitempty"`
	GoalID    int64           `json:"goal_id,omitempty"`
}

type GoalSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Objective string `json:"objective"`
	Status    string `json:"status"`
}

type AgentStep struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type AgentResponse struct {
	SessionID  int64              `json:"session_id"`
	Status     string             `json:"status"`
	Iterations int                `json:"iterations"`
	Response   string             `json:"response"`
	Goal       string             `json:"goal"`
	Plan       []agent.PlanStep   `json:"plan"`
	Steps      []AgentStep        `json:"steps"`
	Trace      []agent.TraceEntry `json:"trace"`
	GoalRef    *GoalSummary       `json:"active_goal,omitempty"`
}

// goalContextBlock renders the active marketing goal for the system
// instruction. It keeps the persistent GOAL strictly separate from the
// per-message INSTRUCTION: the goal defines what success means, the
// instruction is only what the user asked right now.
func goalContextBlock(goal *MarketingGoal) string {

	var b strings.Builder

	b.WriteString("MARKETING GOAL (persistent objective — this is NOT the current instruction):\n")
	fmt.Fprintf(&b, "Name: %s\n", goal.Name)
	fmt.Fprintf(&b, "Objective: %s\n", goal.Objective)

	if goal.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", goal.Description)
	}

	targets := []string{}
	if goal.TargetCPL != nil {
		targets = append(targets, fmt.Sprintf("cost per lead < %g", *goal.TargetCPL))
	}
	if goal.TargetCPA != nil {
		targets = append(targets, fmt.Sprintf("cost per action < %g", *goal.TargetCPA))
	}
	if goal.TargetROAS != nil {
		targets = append(targets, fmt.Sprintf("ROAS > %g", *goal.TargetROAS))
	}
	if goal.TargetConversions != nil {
		targets = append(targets, fmt.Sprintf("conversions >= %d", *goal.TargetConversions))
	}
	if len(targets) > 0 {
		fmt.Fprintf(&b, "Success targets: %s\n", strings.Join(targets, ", "))
	}

	limits := []string{}
	if goal.DailyBudget != nil {
		limits = append(limits, fmt.Sprintf("daily spend <= %g", *goal.DailyBudget))
	}
	if goal.Budget != nil {
		limits = append(limits, fmt.Sprintf("total budget <= %g", *goal.Budget))
	}
	if len(limits) > 0 {
		fmt.Fprintf(&b, "Constraints: %s\n", strings.Join(limits, ", "))
	}

	if goal.StartDate != "" || goal.EndDate != "" {
		fmt.Fprintf(&b, "Window: %s to %s\n",
			orDash(goal.StartDate), orDash(goal.EndDate))
	}

	b.WriteString(`
Rules of engagement:
- The user's message is an INSTRUCTION (e.g. "analyze campaigns"). The goal above is the standing definition of success.
- Never treat the instruction as replacing or modifying the goal.
- When you evaluate options or take optimization actions, measure them against the goal's targets and constraints.
- If the instruction conflicts with the goal's constraints, say so explicitly before acting.`)

	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// flattenTrace converts the runtime trace into the flat step list
// used by the frontend tool-call display and history storage.
func flattenTrace(trace []agent.TraceEntry) []AgentStep {

	steps := make([]AgentStep, 0)

	for _, entry := range trace {
		for i, obs := range entry.Observations {

			step := AgentStep{Tool: obs.Tool}

			if i < len(entry.Actions) {
				step.Args = entry.Actions[i].Args
			}

			if obs.Error != "" {
				step.Error = obs.Error
			} else {
				step.Result = obs.Result
			}

			steps = append(steps, step)
		}
	}

	return steps
}

func (a *Api) RunAgentChat(w http.ResponseWriter, r *http.Request) {

	var agentReq AgentRequest

	if err := json.NewDecoder(r.Body).Decode(&agentReq); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if agentReq.Prompt == "" {
		agentReq.Prompt = agentReq.Message
	}

	if agentReq.ModelName == "" || agentReq.Prompt == "" {
		http.Error(w, "model_name and prompt required", http.StatusBadRequest)
		return
	}

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := a.GetUserByEmail(user.Email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Gemini API key for the requested model.
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
	).Scan(&modelName, &apiKey)

	if err == sql.ErrNoRows {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Meta access token and default ad account for the user.
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
		http.Error(w, "no connected Meta Ads account", http.StatusBadRequest)
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

		var res sql.Result
		res, err = a.db.Exec(`
			INSERT INTO agent_sessions (user_id, title)
			VALUES (?, ?)
		`, user.ID, title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sessionID, _ = res.LastInsertId()
	}

	// Resolve the marketing goal for this run: explicit goal_id wins,
	// otherwise fall back to the session's linked goal.
	var activeGoal *MarketingGoal

	if agentReq.GoalID > 0 {
		goal, err := a.getOwnedGoal(r, agentReq.GoalID)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if goal == nil {
			http.Error(w, "goal not found", http.StatusNotFound)
			return
		}
		if goal.Status != "ACTIVE" {
			http.Error(w,
				"goal is "+strings.ToLower(goal.Status)+", only ACTIVE goals can drive runs",
				http.StatusBadRequest)
			return
		}
		activeGoal = goal

		// Persist the session ↔ goal link (a goal can have many sessions).
		a.db.Exec(`
			UPDATE agent_sessions SET goal_id = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, activeGoal.ID, sessionID)
	} else if sessionID > 0 {
		var goalID sql.NullInt64
		err = a.db.QueryRow(`
			SELECT goal_id FROM agent_sessions WHERE id = ?
		`, sessionID).Scan(&goalID)

		if err == nil && goalID.Valid {
			goal, gerr := a.getOwnedGoal(r, goalID.Int64)
			if gerr == nil && goal != nil && goal.Status == "ACTIVE" {
				activeGoal = goal
			}
		}
	}

	_, err = a.db.Exec(`
		INSERT INTO agent_messages (session_id, role, content)
		VALUES (?, 'user', ?)
	`, sessionID, agentReq.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Run the agent.
	provider := cloud.NewProvider(
		apiKey,
		agentReq.ModelName,
		"https://generativelanguage.googleapis.com/v1beta",
	)

	runtime := agent.NewRuntime(
		provider,
		tools.NewToolHandler(),
		tools.ToolContext{
			AccessToken: accessToken,
			AdAccountID: adAccountID,
		},
	)

	if activeGoal != nil {
		runtime.ContextBlocks = append(runtime.ContextBlocks, goalContextBlock(activeGoal))
	}

	result, err := runtime.Run(agentReq.Prompt, agentReq.Messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps := flattenTrace(result.Trace)

	// Persist the assistant turn with its full reasoning trace.
	stepsJSON, _ := json.Marshal(steps)
	traceJSON, _ := json.Marshal(result.Trace)

	_, err = a.db.Exec(`
		INSERT INTO agent_messages (session_id, role, content, steps, trace, model_name)
		VALUES (?, 'assistant', ?, ?, ?, ?)
	`, sessionID, result.Response, string(stepsJSON), string(traceJSON), agentReq.ModelName)

	if err == nil {
		a.db.Exec(`
			UPDATE agent_sessions
			SET updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, sessionID)
	}

	var goalSummary *GoalSummary
	if activeGoal != nil {
		goalSummary = &GoalSummary{
			ID:        activeGoal.ID,
			Name:      activeGoal.Name,
			Objective: activeGoal.Objective,
			Status:    activeGoal.Status,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AgentResponse{
		SessionID:  sessionID,
		Status:     result.Status,
		Iterations: result.Iterations,
		Response:   result.Response,
		Goal:       result.Goal,
		Plan:       result.Plan,
		Steps:      steps,
		Trace:      result.Trace,
		GoalRef:    goalSummary,
	})
}
