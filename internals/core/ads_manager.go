package core

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type CampaignDetailsResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Objective string `json:"objective"`

	DailyBudget    string `json:"daily_budget,omitempty"`
	LifetimeBudget string `json:"lifetime_budget,omitempty"`

	CreatedTime string `json:"created_time,omitempty"`
	UpdatedTime string `json:"updated_time,omitempty"`

	SpecialAdCategories []string `json:"special_ad_categories,omitempty"`
}

type MetaErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func (a *Api) GetCampaignDetails(w http.ResponseWriter, r *http.Request) {
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

	url := fmt.Sprintf(
		"https://graph.facebook.com/v23.0/%s?fields=id,name,status,objective,daily_budget,lifetime_budget,created_time,updated_time,special_ad_categories",
		campaignID,
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
		var metaErr MetaErrorResponse
		json.NewDecoder(resp.Body).Decode(&metaErr)

		http.Error(w, metaErr.Error.Message, resp.StatusCode)
		return
	}

	var campaign CampaignDetailsResponse

	if err := json.NewDecoder(resp.Body).Decode(&campaign); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(campaign)
}
