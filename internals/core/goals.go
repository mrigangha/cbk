package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// getUserID resolves the authenticated user's ID.
func (a *Api) getUserID(r *http.Request) int64 {
	user := a.GetUser(r)
	if user == nil {
		return 0
	}
	return user.ID
}

// MarketingGoal is a first-class optimization objective. The description
// is for humans and LLMs; the structured target fields are what a future
// automation engine optimizes against.
type MarketingGoal struct {
	ID          int64    `json:"id"`
	UserID      int64    `json:"user_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Objective   string   `json:"objective"`
	Status      string   `json:"status"`
	TargetCPL   *float64 `json:"target_cpl,omitempty"`
	TargetCPA   *float64 `json:"target_cpa,omitempty"`
	TargetROAS  *float64 `json:"target_roas,omitempty"`

	TargetConversions *int64 `json:"target_conversions,omitempty"`

	Budget      *float64 `json:"budget,omitempty"`
	DailyBudget *float64 `json:"daily_budget,omitempty"`

	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type GoalRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Objective   string `json:"objective"`

	TargetCPL   *float64 `json:"target_cpl"`
	TargetCPA   *float64 `json:"target_cpa"`
	TargetROAS  *float64 `json:"target_roas"`
	TargetConversions *int64 `json:"target_conversions"`

	Budget      *float64 `json:"budget"`
	DailyBudget *float64 `json:"daily_budget"`

	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func goalFromRequest(req GoalRequest) (*MarketingGoal, error) {

	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}

	objective := strings.ToUpper(strings.TrimSpace(req.Objective))
	if objective == "" {
		return nil, fmt.Errorf("objective is required")
	}

	goal := &MarketingGoal{
		Name:              strings.TrimSpace(req.Name),
		Description:       req.Description,
		Objective:         objective,
		Status:            "ACTIVE",
		TargetCPL:         req.TargetCPL,
		TargetCPA:         req.TargetCPA,
		TargetROAS:        req.TargetROAS,
		TargetConversions: req.TargetConversions,
		Budget:            req.Budget,
		DailyBudget:       req.DailyBudget,
		StartDate:         req.StartDate,
		EndDate:           req.EndDate,
	}

	if err := validateGoalNumbers(goal); err != nil {
		return nil, err
	}

	return goal, nil
}

func validateGoalNumbers(goal *MarketingGoal) error {

	negatives := []string{}

	checkFloat := func(v *float64, name string) {
		if v != nil && *v < 0 {
			negatives = append(negatives, name)
		}
	}
	checkInt := func(v *int64, name string) {
		if v != nil && *v < 0 {
			negatives = append(negatives, name)
		}
	}

	checkFloat(goal.TargetCPL, "target_cpl")
	checkFloat(goal.TargetCPA, "target_cpa")
	checkFloat(goal.TargetROAS, "target_roas")
	checkInt(goal.TargetConversions, "target_conversions")
	checkFloat(goal.Budget, "budget")
	checkFloat(goal.DailyBudget, "daily_budget")

	if len(negatives) > 0 {
		return fmt.Errorf("negative values not allowed for: %s", strings.Join(negatives, ", "))
	}

	return nil
}

const goalColumns = `
	id, user_id, name, COALESCE(description, ''), objective, status,
	target_cpl, target_cpa, target_roas, target_conversions,
	budget, daily_budget,
	COALESCE(start_date, ''), COALESCE(end_date, ''),
	created_at, updated_at
`

func scanGoal(row interface{ Scan(...any) error }) (*MarketingGoal, error) {

	var g MarketingGoal

	err := row.Scan(
		&g.ID,
		&g.UserID,
		&g.Name,
		&g.Description,
		&g.Objective,
		&g.Status,
		&g.TargetCPL,
		&g.TargetCPA,
		&g.TargetROAS,
		&g.TargetConversions,
		&g.Budget,
		&g.DailyBudget,
		&g.StartDate,
		&g.EndDate,
		&g.CreatedAt,
		&g.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &g, nil
}

func (a *Api) CreateGoal(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req GoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	goal, err := goalFromRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	res, err := a.db.Exec(`
		INSERT INTO marketing_goals (
			user_id, name, description, objective, status,
			target_cpl, target_cpa, target_roas, target_conversions,
			budget, daily_budget, start_date, end_date
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		user.ID,
		goal.Name,
		goal.Description,
		goal.Objective,
		goal.Status,
		goal.TargetCPL,
		goal.TargetCPA,
		goal.TargetROAS,
		goal.TargetConversions,
		goal.Budget,
		goal.DailyBudget,
		goal.StartDate,
		goal.EndDate,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, _ := res.LastInsertId()

	saved, err := scanGoal(a.db.QueryRow(
		`SELECT `+goalColumns+` FROM marketing_goals WHERE id = ?`, id,
	))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(saved)
}

func (a *Api) ListGoals(w http.ResponseWriter, r *http.Request) {

	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	statusFilter := r.URL.Query().Get("status")

	query := `SELECT ` + goalColumns + ` FROM marketing_goals WHERE user_id = ?`
	args := []any{user.ID}

	if statusFilter != "" {
		query += ` AND status = ?`
		args = append(args, strings.ToUpper(statusFilter))
	}

	query += ` ORDER BY created_at DESC`

	rows, err := a.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	goals := make([]MarketingGoal, 0)

	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		goals = append(goals, *g)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"goals": goals})
}

// getOwnedGoal loads a goal and verifies it belongs to the authenticated user.
func (a *Api) getOwnedGoal(r *http.Request, goalID int64) (*MarketingGoal, error) {

	user := a.GetUser(r)
	if user == nil {
		return nil, fmt.Errorf("unauthorized")
	}

	goal, err := scanGoal(a.db.QueryRow(
		`SELECT `+goalColumns+` FROM marketing_goals WHERE id = ?`, goalID,
	))

	if err == sql.ErrNoRows {
		return nil, nil // not found (or not owned — indistinguishable)
	}
	if err != nil {
		return nil, err
	}

	if goal.UserID != user.ID {
		return nil, nil
	}

	return goal, nil
}

func parseGoalID(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "goalID"), 10, 64)
}

func (a *Api) GetGoal(w http.ResponseWriter, r *http.Request) {

	id, err := parseGoalID(r)
	if err != nil || id <= 0 {
		http.Error(w, "invalid goal id", http.StatusBadRequest)
		return
	}

	goal, err := a.getOwnedGoal(r, id)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if goal == nil {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(goal)
}

func (a *Api) UpdateGoal(w http.ResponseWriter, r *http.Request) {

	id, err := parseGoalID(r)
	if err != nil || id <= 0 {
		http.Error(w, "invalid goal id", http.StatusBadRequest)
		return
	}

	existing, err := a.getOwnedGoal(r, id)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if existing == nil {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	var req GoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updated := &MarketingGoal{
		ID:          existing.ID,
		UserID:      existing.UserID,
		Name:        existing.Name,
		Description: existing.Description,
		Objective:   existing.Objective,
		Status:      existing.Status,
		TargetCPL:   existing.TargetCPL,
		TargetCPA:   existing.TargetCPA,
		TargetROAS:  existing.TargetROAS,

		TargetConversions: existing.TargetConversions,

		Budget:      existing.Budget,
		DailyBudget: existing.DailyBudget,

		StartDate: existing.StartDate,
		EndDate:   existing.EndDate,
	}

	// PATCH semantics: only provided fields change.
	if strings.TrimSpace(req.Name) != "" {
		updated.Name = strings.TrimSpace(req.Name)
	}
	if req.Description != "" {
		updated.Description = req.Description
	}
	if strings.TrimSpace(req.Objective) != "" {
		updated.Objective = strings.ToUpper(strings.TrimSpace(req.Objective))
	}

	if req.TargetCPL != nil {
		updated.TargetCPL = req.TargetCPL
	}
	if req.TargetCPA != nil {
		updated.TargetCPA = req.TargetCPA
	}
	if req.TargetROAS != nil {
		updated.TargetROAS = req.TargetROAS
	}
	if req.TargetConversions != nil {
		updated.TargetConversions = req.TargetConversions
	}
	if req.Budget != nil {
		updated.Budget = req.Budget
	}
	if req.DailyBudget != nil {
		updated.DailyBudget = req.DailyBudget
	}
	if req.StartDate != "" {
		updated.StartDate = req.StartDate
	}
	if req.EndDate != "" {
		updated.EndDate = req.EndDate
	}

	if err := validateGoalNumbers(updated); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err = a.db.Exec(`
		UPDATE marketing_goals SET
			name = ?, description = ?, objective = ?,
			target_cpl = ?, target_cpa = ?, target_roas = ?, target_conversions = ?,
			budget = ?, daily_budget = ?,
			start_date = ?, end_date = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		updated.Name,
		updated.Description,
		updated.Objective,
		updated.TargetCPL,
		updated.TargetCPA,
		updated.TargetROAS,
		updated.TargetConversions,
		updated.Budget,
		updated.DailyBudget,
		updated.StartDate,
		updated.EndDate,
		id,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	saved, err := scanGoal(a.db.QueryRow(
		`SELECT `+goalColumns+` FROM marketing_goals WHERE id = ?`, id,
	))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(saved)
}

func (a *Api) DeleteGoal(w http.ResponseWriter, r *http.Request) {

	id, err := parseGoalID(r)
	if err != nil || id <= 0 {
		http.Error(w, "invalid goal id", http.StatusBadRequest)
		return
	}

	res, err := a.db.Exec(`
		DELETE FROM marketing_goals
		WHERE id = ? AND user_id = ?
	`, id, a.getUserID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"message": "goal deleted"})
}

func (a *Api) setGoalStatus(w http.ResponseWriter, r *http.Request, status string) {

	id, err := parseGoalID(r)
	if err != nil || id <= 0 {
		http.Error(w, "invalid goal id", http.StatusBadRequest)
		return
	}

	res, err := a.db.Exec(`
		UPDATE marketing_goals
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ?
	`, status, id, a.getUserID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		http.Error(w, "goal not found", http.StatusNotFound)
		return
	}

	saved, err := scanGoal(a.db.QueryRow(
		`SELECT `+goalColumns+` FROM marketing_goals WHERE id = ?`, id,
	))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(saved)
}

func (a *Api) PauseGoal(w http.ResponseWriter, r *http.Request) {
	a.setGoalStatus(w, r, "PAUSED")
}

func (a *Api) ActivateGoal(w http.ResponseWriter, r *http.Request) {
	a.setGoalStatus(w, r, "ACTIVE")
}

// CompleteGoal marks a goal as achieved.
func (a *Api) CompleteGoal(w http.ResponseWriter, r *http.Request) {
	a.setGoalStatus(w, r, "COMPLETED")
}
