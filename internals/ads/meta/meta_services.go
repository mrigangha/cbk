package meta

type Campaign struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func getAllCampaigns(adsAccountID string, accessToken string) ([]Campaign, error) {

}
