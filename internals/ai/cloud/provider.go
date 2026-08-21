package cloud

type Provider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewProvider(apiKey, model, baseURL string) *Provider {
	return &Provider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
	}
}
