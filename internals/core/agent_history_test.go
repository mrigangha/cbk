package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// agentSessionsRouter wires only the session endpoints under auth.
func agentSessionsRouter(a *Api) http.Handler {

	r := chi.NewRouter()
	r.With(AuthMiddleware).Get("/agent/sessions", a.ListAgentSessions)
	r.With(AuthMiddleware).Patch("/agent/sessions/{sessionID}", a.UpdateAgentSession)
	r.With(AuthMiddleware).Get("/agent/sessions/{sessionID}/messages", a.GetAgentSessionMessages)
	return r
}

// histReq builds an authenticated request for a specific user.
func histReq(
	t *testing.T,
	a *Api,
	userID int64,
	method, path string,
	body any,
) *http.Request {

	t.Helper()

	var email string
	if err := a.db.QueryRow(
		`SELECT email FROM users WHERE id = ?`, userID,
	).Scan(&email); err != nil {
		t.Fatal(err)
	}

	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+NewJWT(email))

	ctx := context.WithValue(req.Context(), UserContextKey,
		map[string]any{"email": email})

	return req.WithContext(ctx)
}

// seedChat creates one session (+ optional goal) and n messages.
func seedChat(
	t *testing.T,
	a *Api,
	userID int64,
	title string,
	goalID *int64,
	msgCount int,
) int64 {

	t.Helper()

	res, err := a.db.Exec(`
		INSERT INTO agent_sessions (user_id, title, goal_id)
		VALUES (?, ?, ?)
	`, userID, title, goalID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	for i := 0; i < msgCount; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		a.db.Exec(`
			INSERT INTO agent_messages (session_id, role, content)
			VALUES (?, ?, 'test content')
		`, id, role)
	}

	return id
}

func TestAgentSessions_ListIncludesCountsAndGoal(t *testing.T) {

	a, uid := newExecTestApi(t)

	res, err := a.db.Exec(`
		INSERT INTO marketing_goals (user_id, name, objective)
		VALUES (?, 'Hist Goal', 'LEADS')
	`, uid)
	if err != nil {
		t.Fatal(err)
	}
	goalID, _ := res.LastInsertId()

	withGoal := seedChat(t, a, uid, "With goal", &goalID, 3)
	seedChat(t, a, uid, "No goal", nil, 0)
	_ = withGoal

	router := agentSessionsRouter(a)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uid,
		http.MethodGet, "/agent/sessions", nil))

	if rec.Code != 200 {
		t.Fatalf("list returned %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Sessions []struct {
			ID           int64  `json:"id"`
			Title        string `json:"title"`
			GoalID       *int64 `json:"goal_id"`
			MessageCount int    `json:"message_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	if len(payload.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(payload.Sessions))
	}

	for _, s := range payload.Sessions {
		switch s.Title {
		case "With goal":
			if s.GoalID == nil || *s.GoalID != goalID {
				t.Errorf("goal_id = %v, want %d", s.GoalID, goalID)
			}
			if s.MessageCount != 3 {
				t.Errorf("message_count = %d, want 3", s.MessageCount)
			}
		case "No goal":
			if s.GoalID != nil {
				t.Errorf("goal_id = %v, want unset", s.GoalID)
			}
			if s.MessageCount != 0 {
				t.Errorf("message_count = %d, want 0", s.MessageCount)
			}
		}
	}
}

func TestAgentSessions_RenameAndRelink(t *testing.T) {

	a, uid := newExecTestApi(t)

	res, _ := a.db.Exec(`
		INSERT INTO marketing_goals (user_id, name, objective)
		VALUES (?, 'Relink Goal', 'SALES')
	`, uid)
	newGoal, _ := res.LastInsertId()

	id := seedChat(t, a, uid, "Old title", nil, 0)

	router := agentSessionsRouter(a)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uid,
		http.MethodPatch, fmt.Sprintf("/agent/sessions/%d", id),
		map[string]any{"title": "Renamed chat"}))
	if rec.Code != 200 {
		t.Fatalf("rename returned %d: %s", rec.Code, rec.Body.String())
	}

	// Relink the goal without touching the new title.
	rec = httptest.NewRecorder()
	gid := newGoal
	router.ServeHTTP(rec, histReq(t, a, uid,
		http.MethodPatch, fmt.Sprintf("/agent/sessions/%d", id),
		map[string]any{"goal_id": gid}))
	if rec.Code != 200 {
		t.Fatalf("relink returned %d: %s", rec.Code, rec.Body.String())
	}

	var (
		title  string
		stored sql.NullInt64
	)
	a.db.QueryRow(
		`SELECT title, COALESCE(goal_id, 0) FROM agent_sessions WHERE id = ?`,
		id,
	).Scan(&title, &stored)

	if title != "Renamed chat" {
		t.Errorf("title = %q, want renamed", title)
	}
	if stored.Int64 != newGoal {
		t.Errorf("goal_id = %d, want %d", stored.Int64, newGoal)
	}

	// Omitted fields must stay untouched.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uid,
		http.MethodPatch, fmt.Sprintf("/agent/sessions/%d", id), map[string]any{}))
	if rec.Code != 200 {
		t.Fatalf("empty patch returned %d", rec.Code)
	}

	a.db.QueryRow(`SELECT title FROM agent_sessions WHERE id = ?`, id).
		Scan(&title)
	if title != "Renamed chat" {
		t.Errorf("empty PATCH changed title to %q", title)
	}
}

// Restoring an old chat must return the FULL run: tool calls with args,
// results, and the reasoning trace — not just the text answers.
func TestAgentSessionMessages_StepsAndTraceRoundTrip(t *testing.T) {

	a, uid := newExecTestApi(t)

	sessionID := seedChat(t, a, uid, "Run history", nil, 1)

	steps := `[
		{"tool":"list_campaigns","args":{"ad_account_id":"777"},
		 "result":{"data":[{"id":"111"}]}}
	]`
	trace := `[
		{"iteration":1,"thought":"Need the campaign list first.",
		 "actions":[{"tool":"list_campaigns","args":{"ad_account_id":"777"}}],
		 "observations":[{"tool":"list_campaigns","result":{"data":[]}}],
		 "decision":{"type":"act","reasoning":"fetch details next"}}
	]`

	a.db.Exec(`
		INSERT INTO agent_messages (session_id, role, content, steps, trace, model_name)
		VALUES (?, 'assistant', 'Here is what I found.', ?, ?, 'gemini-2.0-flash')
	`, sessionID, steps, trace)

	router := agentSessionsRouter(a)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uid,
		http.MethodGet, fmt.Sprintf("/agent/sessions/%d/messages", sessionID),
		nil))
	if rec.Code != 200 {
		t.Fatalf("messages returned %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ModelName string `json:"model_name"`
			Steps     []struct {
				Tool   string         `json:"tool"`
				Args   map[string]any `json:"args"`
				Result any            `json:"result"`
			} `json:"steps"`
			Trace []struct {
				Iteration int    `json:"iteration"`
				Thought   string `json:"thought"`
				Decision  struct {
					Type string `json:"type"`
				} `json:"decision"`
			} `json:"trace"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("bad body: %s", rec.Body.String())
	}

	if len(payload.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(payload.Messages))
	}

	asstIdx := -1
	for i := range payload.Messages {
		if payload.Messages[i].Role == "assistant" {
			asstIdx = i
		}
	}
	if asstIdx < 0 {
		t.Fatal("no assistant message returned")
	}
	asst := &payload.Messages[asstIdx]

	if len(asst.Steps) != 1 || asst.Steps[0].Tool != "list_campaigns" {
		t.Errorf("steps = %+v, want list_campaigns with args", asst.Steps)
	}
	if len(asst.Trace) != 1 || asst.Trace[0].Thought == "" ||
		asst.Trace[0].Decision.Type != "act" {
		t.Errorf("trace = %+v, want thought + decision", asst.Trace)
	}
	if asst.ModelName != "gemini-2.0-flash" {
		t.Errorf("model_name = %q", asst.ModelName)
	}
}

func TestAgentSessions_OwnershipEnforced(t *testing.T) {
	a, uidA := newExecTestApi(t)

	// Second user owns the target session.
	var uidB int64
	res, _ := a.db.Exec(`
		INSERT INTO users (username, email, hashed_password)
		VALUES ('hist-b', 'hist-b@test.com', 'x')
	`)
	uidB, _ = res.LastInsertId()

	victimID := seedChat(t, a, uidB, "B's chat", nil, 0)

	router := agentSessionsRouter(a)

	// A tries to rename B's session → not found (no existence leak).
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uidA,
		http.MethodPatch, fmt.Sprintf("/agent/sessions/%d", victimID),
		map[string]any{"title": "hijack"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user rename returned %d, want 404", rec.Code)
	}

	// A's list must never contain B's chat.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uidA,
		http.MethodGet, "/agent/sessions", nil))
	if bytes.Contains(rec.Body.Bytes(), []byte("B's chat")) {
		t.Error("another user's chat leaked into the session list")
	}

	// Invalid id → 400.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, histReq(t, a, uidA,
		http.MethodPatch, "/agent/sessions/notanumber",
		map[string]any{"title": "x"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id returned %d, want 400", rec.Code)
	}
}
