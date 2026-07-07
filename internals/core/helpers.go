package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
)

func (a *Api) GetUserByEmail(email string) (*User, error) {
	var user User

	err := a.db.QueryRow(`
	SELECT
		id,
		username,
		email,
		hashed_password
	FROM users
	WHERE email = ?
	`, email).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.HashedPassword,
	)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (a *Api) GetUserByID(id int64) (*User, error) {
	var user User

	err := a.db.QueryRow(`
	SELECT
		id,
		username,
		email,
		hashed_password
	FROM users
	WHERE id = ?
	`, id).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.HashedPassword,
	)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (a *Api) GetUser(r *http.Request) *User {
	data, ok := r.Context().Value(UserContextKey).(map[string]any)
	if !ok {
		return nil
	}

	email, ok := data["email"].(string)
	if !ok {
		return nil
	}
	user, err := a.GetUserByEmail(email)
	if err != nil {
		return nil
	}
	return user
}

type AdAccountsResponse struct {
	Data []MetaAdAccount `json:"data"`
}

type MetaAdAccount struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	Name         string `json:"name"`
	Currency     string `json:"currency"`
	TimezoneName string `json:"timezone_name"`
	Business     *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"business,omitempty"`
}

func SaveAdsAccounts(accessToken string, userID int64, db *sql.DB) error {
	req, err := http.NewRequest(
		"GET",
		"https://graph.facebook.com/v23.0/me/adaccounts?fields=id,name,account_id,currency,timezone_name,business",
		nil,
	)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var accounts AdAccountsResponse
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return err
	}

	for _, acc := range accounts.Data {

		var businessID string
		if acc.Business != nil {
			businessID = acc.Business.ID
		}

		_, err := db.Exec(`
			INSERT INTO meta_ads_accounts (
				user_id,
				ad_account_id,
				business_id,
				account_name,
				access_token,
				token_expires_at,
				currency,
				timezone_name
			)
			VALUES (?, ?, ?, ?, ?, NULL, ?, ?)
			ON CONFLICT(user_id, ad_account_id)
			DO UPDATE SET
				business_id = excluded.business_id,
				account_name = excluded.account_name,
				access_token = excluded.access_token,
				currency = excluded.currency,
				timezone_name = excluded.timezone_name,
				updated_at = CURRENT_TIMESTAMP;
		`,
			userID,
			acc.AccountID,
			businessID,
			acc.Name,
			accessToken,
			acc.Currency,
			acc.TimezoneName,
		)

		if err != nil {
			return err
		}

		fmt.Printf("Saved account: %s (%s)\n", acc.Name, acc.AccountID)
	}

	return nil
}
