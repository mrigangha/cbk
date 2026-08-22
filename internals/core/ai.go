package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mrigangha/cbk/internals/ai/cloud"
)

type CreateProviderRequest struct {
	ModelName    string `json:"model_name"`
	APIKey       string `json:"api_key"`
	ProviderType string `json:"provider_type"` // GEMINI | OPENROUTER
}

type ProviderInfo struct {
	ID           int64  `json:"id"`
	ModelName    string `json:"model_name"`
	ProviderType string `json:"provider_type"`
	CreatedAt    string `json:"created_at"`
}

// normalizeProviderType defaults and validates the provider type.
func normalizeProviderType(t string) (string, error) {

	switch strings.ToUpper(strings.TrimSpace(t)) {

	case "":
		return "GEMINI", nil
	case "GEMINI", "OPENROUTER":
		return strings.ToUpper(strings.TrimSpace(t)), nil

	default:
		return "", fmt.Errorf("unsupported provider_type %q (use GEMINI or OPENROUTER)", t)
	}
}

func (a *Api) ListProviders(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := a.db.Query(`
		SELECT id, provider_name, COALESCE(provider_type, 'GEMINI'), created_at
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
		if err := rows.Scan(&p.ID, &p.ModelName, &p.ProviderType, &p.CreatedAt); err != nil {
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

func (a *Api) DeleteProvider(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "providerID"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid provider id", http.StatusBadRequest)
		return
	}

	res, err := a.db.Exec(`
		DELETE FROM providers
		WHERE id = ? AND user_id = ?
	`, id, user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"message": "provider deleted",
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

	providerType, err := normalizeProviderType(req.ProviderType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	res, err := a.db.Exec(`
		INSERT INTO providers (user_id, provider_name, api_key, provider_type)
		VALUES (?, ?, ?, ?)
	`,
		user.ID,
		req.ModelName,
		req.APIKey,
		providerType,
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
		"id":            id,
		"model_name":    req.ModelName,
		"provider_type": providerType,
		"message":       "Provider created successfully",
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
	var modelType string
	user, err := a.GetUserByEmail(user.Email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = a.db.QueryRow(`
		SELECT api_key, COALESCE(provider_type, 'GEMINI')
		FROM providers
		WHERE user_id = ?
		AND provider_name = ?
	`,
		user.ID,
		req.ModelName,
	).Scan(
		&apiKey,
		&modelType,
	)

	if err == sql.ErrNoRows {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	provider, err := cloud.New(modelType, apiKey, req.ModelName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response, err := provider.GenerateText(req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"response": response,
	})
}
