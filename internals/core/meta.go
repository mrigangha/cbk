package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

func (a *Api) MetaLogin(w http.ResponseWriter, r *http.Request) {

	var (
		appID       = os.Getenv("META_APP_ID")
		callbackURL = os.Getenv("META_CALLBACK_URL")
	)
	authURL := fmt.Sprintf(
		"https://www.facebook.com/v23.0/dialog/oauth?client_id=%s&redirect_uri=%s&response_type=code&scope=ads_management,ads_read,business_management",
		appID,
		url.QueryEscape(callbackURL),
	)

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}
type MetaCredential struct {
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	CallbackURL string `json:"callback_url"`
}

func (a *Api) GetMetaCredential(w http.ResponseWriter, r *http.Request) {
	var (
		appID       = os.Getenv("META_APP_ID")
		appSecret   = os.Getenv("META_APP_SECRET")
		callbackURL = os.Getenv("META_CALLBACK_URL")
	)
	cred := MetaCredential{
		AppID:       appID,
		AppSecret:   appSecret,
		CallbackURL: callbackURL,
	}
	json.NewEncoder(w).Encode(cred)
}

type CallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

func (a *Api) MetaCallback(w http.ResponseWriter, r *http.Request) {
	var req CallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	if req.State == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}

	refreshToken := r.Header.Get("X-Refresh-Token")
	if refreshToken == "" {
		http.Error(w, "missing refresh token", http.StatusUnauthorized)
		return
	}

	claims, err := DecodeJWT(refreshToken)
	if err != nil {
		http.Error(w, "invalid refresh token", http.StatusUnauthorized)
		return
	}

	email, ok := claims["email"].(string)
	if !ok {
		http.Error(w, "invalid email", http.StatusUnauthorized)
		return
	}
	fmt.Println(email)
	var (
		appID       = os.Getenv("META_APP_ID")
		appSecret   = os.Getenv("META_APP_SECRET")
		callbackURL = os.Getenv("META_CALLBACK_URL")
	)

	// Exchange code for access token
	tokenURL := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/oauth/access_token?client_id=%s&redirect_uri=%s&client_secret=%s&code=%s",
		url.QueryEscape(appID),
		url.QueryEscape(callbackURL),
		url.QueryEscape(appSecret),
		url.QueryEscape(req.Code),
	)

	resp, err := http.Get(tokenURL)
	if err != nil {
		http.Error(w, "failed to contact Meta", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "failed to exchange code", http.StatusBadRequest)
		return
	}

	var token TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		http.Error(w, "invalid token response", http.StatusInternalServerError)
		return
	}

	// TODO:
	// Save token.AccessToken to your database
	// Associate it with the currently logged-in user

	user, err := a.GetUserByEmail(email)
	if err != nil {
		http.Error(w, "failed to get user", http.StatusInternalServerError)
		return
	}

	//a.db.Exec(`INSERT INTO meta_ads_accounts()`)
	err = SaveAdsAccounts(token.AccessToken, user.ID, a.db)
	if err != nil {
		http.Error(w, "failed to get ads accounts", http.StatusInternalServerError)
		return
	}

	// Redirect back to frontend
	http.Redirect(
		w,
		r,
		"http://localhost:5173/integrations?connected=true",
		http.StatusTemporaryRedirect,
	)
}

type ConnectedMetaAccount struct {
	ID             int    `json:"id"`
	AdAccountID    string `json:"ad_account_id"`
	BusinessID     string `json:"business_id,omitempty"`
	AccountName    string `json:"account_name"`
	Currency       string `json:"currency"`
	TimezoneName   string `json:"timezone_name"`
	TokenExpiresAt string `json:"token_expires_at,omitempty"`
	AccessToken    string `json:"access_token"`
}

type CampaignResponse struct {
	Data []Campaign `json:"data"`
}

type Campaign struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (a *Api) GetCampaigns(w http.ResponseWriter, r *http.Request) {
	user := a.GetUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	adAccountID := r.URL.Query().Get("ad_account_id")
	if adAccountID == "" {
		http.Error(w, "missing ad_account_id", http.StatusBadRequest)
		return
	}

	var accessToken string

	err := a.db.QueryRow(`
		SELECT access_token
		FROM meta_ads_accounts
		WHERE user_id = ? AND ad_account_id = ?
	`,
		user.ID,
		adAccountID,
	).Scan(&accessToken)

	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	url := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/act_%s/campaigns?fields=id,name,status,objective",
		adAccountID,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, string(body), resp.StatusCode)
		return
	}

	var campaigns CampaignResponse

	if err := json.NewDecoder(resp.Body).Decode(&campaigns); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(campaigns)
}

func (a *Api) GetConnectedMetaAccounts(w http.ResponseWriter, r *http.Request) {
	// Authenticate user
	user := a.GetUser(r)

	// Get user id
	var userID int64
	err := a.db.QueryRow(
		`SELECT id FROM users WHERE email = ?`,
		user.Email,
	).Scan(&userID)

	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	rows, err := a.db.Query(`
		SELECT
			id,
			ad_account_id,
			business_id,
			account_name,
			currency,
			timezone_name,
			COALESCE(token_expires_at, ''),
			access_token
		FROM meta_ads_accounts
		WHERE user_id = ?
		ORDER BY account_name
	`, userID)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var accounts []ConnectedMetaAccount

	for rows.Next() {
		var account ConnectedMetaAccount

		err := rows.Scan(
			&account.ID,
			&account.AdAccountID,
			&account.BusinessID,
			&account.AccountName,
			&account.Currency,
			&account.TimezoneName,
			&account.TokenExpiresAt,
			&account.AccessToken,
		)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		accounts = append(accounts, account)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(accounts)
}

func (a *Api) DeleteConnectedMetaAccounts(w http.ResponseWriter, r *http.Request) {

	// Authenticate user
	user := a.GetUser(r)

	// Get user ID
	var userID int64
	err := a.db.QueryRow(
		`SELECT id FROM users WHERE email = ?`,
		user.Email,
	).Scan(&userID)

	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Delete all Meta accounts for the user
	_, err = a.db.Exec(
		`DELETE FROM meta_ads_accounts WHERE user_id = ?`,
		userID,
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "All connected Meta ad accounts deleted.",
	})
}
