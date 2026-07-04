package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func (p *Provider) GenerateText(prompt string) (string, error) {
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
