package core

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

type AgentSession struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type StoredAgentMessage struct {
	ID        int64           `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Steps     []AgentStep     `json:"steps,omitempty"`
	ModelName string          `json:"model_name,omitempty"`
	CreatedAt string          `json:"created_at"`
	RawSteps  json.RawMessage `json:"-"`
}

func (a *Api) ListAgentSessions(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := a.db.Query(`
		SELECT id, title, created_at, updated_at
		FROM agent_sessions
		WHERE user_id = ?
		ORDER BY updated_at DESC
	`, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	sessions := make([]AgentSession, 0)

	for rows.Next() {
		var s AgentSession
		if err := rows.Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		SELECT id, role, content, COALESCE(steps, '[]'), COALESCE(model_name, ''), created_at
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

		if err := rows.Scan(
			&m.ID,
			&m.Role,
			&m.Content,
			&m.RawSteps,
			&m.ModelName,
			&m.CreatedAt,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if len(m.RawSteps) > 0 {
			json.Unmarshal(m.RawSteps, &m.Steps)
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
