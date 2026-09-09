package core

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/agent"
)

type AgentSession struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	GoalID       *int64 `json:"goal_id,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

type StoredAgentMessage struct {
	ID        int64              `json:"id"`
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	Steps     []AgentStep        `json:"steps,omitempty"`
	Trace     []agent.TraceEntry `json:"trace,omitempty"`
	ModelName string             `json:"model_name,omitempty"`
	CreatedAt string             `json:"created_at"`
}

func (a *Api) ListAgentSessions(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := a.db.Query(`
		SELECT s.id, s.title, s.created_at, s.updated_at,
		       s.goal_id,
		       (SELECT COUNT(*) FROM agent_messages m
		        WHERE m.session_id = s.id) AS message_count
		FROM agent_sessions s
		WHERE s.user_id = ?
		ORDER BY s.updated_at DESC
	`, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	sessions := make([]AgentSession, 0)

	for rows.Next() {
		var (
			s      AgentSession
			goalID sql.NullInt64
		)
		if err := rows.Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt,
			&goalID, &s.MessageCount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if goalID.Valid {
			v := goalID.Int64
			s.GoalID = &v
		}
		sessions = append(sessions, s)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"sessions": sessions,
	})
}

func (a *Api) CreateAgentSession(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "New Chat"
	}

	res, err := a.db.Exec(`
		INSERT INTO agent_sessions (user_id, title)
		VALUES (?, ?)
	`, user.ID, title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, _ := res.LastInsertId()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AgentSession{
		ID:    id,
		Title: title,
	})
}

func (a *Api) GetAgentSessionMessages(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionID, err := strconv.ParseInt(chi.URLParam(r, "sessionID"), 10, 64)
	if err != nil || sessionID <= 0 {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}

	// Ensure the session belongs to the authenticated user.
	var ownerID int64
	err = a.db.QueryRow(`
		SELECT user_id FROM agent_sessions WHERE id = ?
	`, sessionID).Scan(&ownerID)

	if err == sql.ErrNoRows {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ownerID != user.ID {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	rows, err := a.db.Query(`
		SELECT id, role, content,
		       COALESCE(steps, '[]'), COALESCE(trace, ''),
		       COALESCE(model_name, ''), created_at
		FROM agent_messages
		WHERE session_id = ?
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	messages := make([]StoredAgentMessage, 0)

	for rows.Next() {
		var m StoredAgentMessage

		// Scan into plain []byte: drivers hand TEXT back as string,
		// which json.RawMessage cannot absorb directly.
		var rawSteps, rawTrace []byte

		if err := rows.Scan(
			&m.ID,
			&m.Role,
			&m.Content,
			&rawSteps,
			&rawTrace,
			&m.ModelName,
			&m.CreatedAt,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if len(rawSteps) > 0 {
			json.Unmarshal(rawSteps, &m.Steps)
		}
		if len(rawTrace) > 0 {
			json.Unmarshal(rawTrace, &m.Trace)
		}

		messages = append(messages, m)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"messages": messages,
	})
}

// UpdateAgentSession partially updates a chat: rename it and/or relink
// its marketing goal. Ownership enforced in the WHERE clause.
func (a *Api) UpdateAgentSession(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionID, err := strconv.ParseInt(chi.URLParam(r, "sessionID"), 10, 64)
	if err != nil || sessionID <= 0 {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}

	var req struct {
		Title  string `json:"title"`
		GoalID *int64 `json:"goal_id"` // null = leave unchanged
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	title := strings.TrimSpace(req.Title)

	res, err := a.db.Exec(`
		UPDATE agent_sessions SET
			title = CASE WHEN ? <> '' THEN ? ELSE title END,
			goal_id = CASE WHEN ? THEN ? ELSE goal_id END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ?
	`, title, title, req.GoalID != nil, req.GoalID, sessionID, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	var updated AgentSession
	var goalID sql.NullInt64

	err = a.db.QueryRow(`
		SELECT id, title, created_at, updated_at, goal_id
		FROM agent_sessions WHERE id = ?
	`, sessionID).Scan(&updated.ID, &updated.Title,
		&updated.CreatedAt, &updated.UpdatedAt, &goalID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if goalID.Valid {
		v := goalID.Int64
		updated.GoalID = &v
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

func (a *Api) DeleteAgentSession(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionID, err := strconv.ParseInt(chi.URLParam(r, "sessionID"), 10, 64)
	if err != nil || sessionID <= 0 {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}

	res, err := a.db.Exec(`
		DELETE FROM agent_sessions
		WHERE id = ? AND user_id = ?
	`, sessionID, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"message": "session deleted",
	})
}
