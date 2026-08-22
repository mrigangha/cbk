package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mrigangha/cbk/internals/tools"
)

type InteractionRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type InteractionResponse struct {
	Steps []struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
			Type string `json:"type"`
		} `json:"content,omitempty"`
	} `json:"steps"`
}

type geminiProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func newGeminiProvider(apiKey, model, baseURL string) *geminiProvider {
	return &geminiProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
	}
}

func (p *geminiProvider) GenerateText(prompt string) (string, error) {
	reqBody := InteractionRequest{
		Model: p.model,
		Input: prompt,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		p.baseURL+"/interactions",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var result InteractionResponse

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	for _, step := range result.Steps {
		if step.Type == "model_output" {
			var text string
			for _, part := range step.Content {
				text += part.Text
			}
			if text != "" {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("no model output found in response")
}

func (p *geminiProvider) Chat(
	req tools.GenerateContentRequest,
) (*tools.GenerateContentResponse, error) {

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf(
		"%s/models/%s:generateContent?key=%s",
		p.baseURL,
		p.model,
		p.apiKey,
	)

	httpReq, err := http.NewRequest(
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"gemini error (%d): %s",
			httpResp.StatusCode,
			string(respBody),
		)
	}

	var response tools.GenerateContentResponse

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}

	return &response, nil
}
