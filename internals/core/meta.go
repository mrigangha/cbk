package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/mrigangha/cbk/internals/tools"
)

type AdSetResponse struct {
	Data []AdSet `json:"data"`
}

type AdSet struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Status           string      `json:"status"`
	EffectiveStatus  string      `json:"effective_status"`
	DailyBudget      string      `json:"daily_budget"`
	LifetimeBudget   string      `json:"lifetime_budget"`
	OptimizationGoal string      `json:"optimization_goal"`
	BillingEvent     string      `json:"billing_event"`
	BidStrategy      string      `json:"bid_strategy"`
	StartTime        string      `json:"start_time"`
	EndTime          string      `json:"end_time"`
	Targeting        interface{} `json:"targeting"`
}

func (a *Api) GetAdSets(w http.ResponseWriter, r *http.Request) {
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

	campaignID := r.URL.Query().Get("ad_campaign_id")
	if campaignID == "" {
		http.Error(w, "missing campaign_id", http.StatusBadRequest)
		return
	}

	var accessToken string

	err := a.db.QueryRow(`
		SELECT access_token
		FROM meta_ads_accounts
		WHERE user_id = ?
		AND ad_account_id = ?
	`,
		user.ID,
		adAccountID,
	).Scan(&accessToken)

	if err != nil {
		http.Error(w, "access token not found", http.StatusInternalServerError)
		return
	}

	graphURL := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/%s/adsets?fields=id,name,status,effective_status,daily_budget,lifetime_budget,optimization_goal,billing_event,bid_strategy,targeting,start_time,end_time",
		campaignID,
	)

	req, err := http.NewRequest(http.MethodGet, graphURL, nil)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to fetch ad sets from Meta",
		})
		return
	}

	var result AdSetResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

type CampaignInsightsResponse struct {
	Data []CampaignInsight `json:"data"`
}

type CampaignInsight struct {
	Spend       string `json:"spend"`
	Impressions string `json:"impressions"`
	Reach       string `json:"reach"`
	Clicks      string `json:"clicks"`
	CTR         string `json:"ctr"`
	CPC         string `json:"cpc"`
	CPM         string `json:"cpm"`
	Frequency   string `json:"frequency"`

	Actions           []Action `json:"actions"`
	CostPerActionType []Action `json:"cost_per_action_type"`

	PurchaseROAS []ROAS `json:"purchase_roas,omitempty"`
}

type Action struct {
	ActionType string `json:"action_type"`
	Value      string `json:"value"`
}

type ROAS struct {
	ActionType string `json:"action_type"`
	Value      string `json:"value"`
}

func (a *Api) GetCampaignInsights(w http.ResponseWriter, r *http.Request) {
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

	campaignID := r.URL.Query().Get("ad_campaign_id")
	if campaignID == "" {
		http.Error(w, "missing campaign_id", http.StatusBadRequest)
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
		http.Error(w, "access token not found", http.StatusInternalServerError)
		return
	}

	graphURL := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/%s/insights?fields=spend,impressions,reach,clicks,ctr,cpc,cpm,frequency,actions,cost_per_action_type,purchase_roas",
		campaignID,
	)

	req, err := http.NewRequest(http.MethodGet, graphURL, nil)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

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

	var campaigns tools.CampaignResponse

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

type AdsResponse struct {
	Data []Ad `json:"data"`
}

type Ad struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	EffectiveStatus  string   `json:"effective_status"`
	ConfiguredStatus string   `json:"configured_status"`
	Creative         Creative `json:"creative"`
}

type Creative struct {
	ID string `json:"id"`
}

func (a *Api) GetAds(w http.ResponseWriter, r *http.Request) {
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

	adSetID := r.URL.Query().Get("ad_set_id")
	if adSetID == "" {
		http.Error(w, "missing ad_set_id", http.StatusBadRequest)
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
		http.Error(w, "access token not found", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/%s/ads?fields=id,name,status,effective_status,configured_status,creative",
		adSetID,
	)

	req, err := http.NewRequest("GET", url, nil)
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

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}
