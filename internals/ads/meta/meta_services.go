package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Campaign struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const graphBaseURL = "https://graph.facebook.com/v23.0"

type campaignResponse struct {
	Data []Campaign `json:"data"`
}

func getAllCampaigns(adsAccountID string, accessToken string) ([]Campaign, error) {
	url := fmt.Sprintf(
		"%s/act_%s/campaigns?fields=id,name",
		graphBaseURL,
		adsAccountID,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"meta error (%d): %s",
			resp.StatusCode,
			string(body),
		)
	}

	var result campaignResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Data, nil
}
