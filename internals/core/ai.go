package core

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/mrigangha/cbk/internals/ai/cloud"
)

type CreateProviderRequest struct {
	ModelName string `json:"model_name"`
	APIKey    string `json:"api_key"`
}

type ProviderInfo struct {
	ID        int64  `json:"id"`
	ModelName string `json:"model_name"`
	CreatedAt string `json:"created_at"`
}

func (a *Api) ListProviders(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := a.db.Query(`
		SELECT id, provider_name, created_at
		FROM providers
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	providers := make([]ProviderInfo, 0)

	for rows.Next() {
		var p ProviderInfo
		if err := rows.Scan(&p.ID, &p.ModelName, &p.CreatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		providers = append(providers, p)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"providers": providers,
	})
}

func (a *Api) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var req CreateProviderRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ModelName == "" || req.APIKey == "" {
		http.Error(w, "model_name and api_key are required", http.StatusBadRequest)
		return
	}
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	res, err := a.db.Exec(`
		INSERT INTO providers (user_id, provider_name, api_key)
		VALUES (?, ?, ?)
	`,
		user.ID,
		req.ModelName, // Stored in provider_name column for now
		req.APIKey,
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := res.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":         id,
		"model_name": req.ModelName,
		"message":    "Provider created successfully",
	})
}

type GenerateRequest struct {
	ModelName string `json:"model_name"`
	Prompt    string `json:"prompt"`
}

func (a *Api) Generate(w http.ResponseWriter, r *http.Request) {
	var req GenerateRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ModelName == "" || req.Prompt == "" {
		http.Error(w, "model_name and prompt are required", http.StatusBadRequest)
		return
	}

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var apiKey string
	var modelName string
	user, err := a.GetUserByEmail(user.Email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = a.db.QueryRow(`
		SELECT provider_name, api_key
		FROM providers
		WHERE user_id = ?
		AND provider_name = ?
	`,
		user.ID,
		req.ModelName,
	).Scan(
		&modelName,
		&apiKey,
	)

	if err == sql.ErrNoRows {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	provider := cloud.NewProvider(
		apiKey,
		modelName,
		"https://generativelanguage.googleapis.com/v1beta",
	)

	response, err := provider.GenerateText(req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"response": response,
	})
}
