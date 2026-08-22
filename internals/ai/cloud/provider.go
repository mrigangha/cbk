package cloud

import (
	"fmt"
	"strings"

	"github.com/mrigangha/cbk/internals/tools"
)

// Provider is a chat-capable AI backend. Implementations translate
// between the internal Gemini-style request/response types and their
// own wire format.
type Provider interface {
	Chat(req tools.GenerateContentRequest) (*tools.GenerateContentResponse, error)
	GenerateText(prompt string) (string, error)
}

const (
	TypeGemini     = "GEMINI"
	TypeOpenRouter = "OPENROUTER"
)

const (
	geminiBaseURL     = "https://generativelanguage.googleapis.com/v1beta"
	openRouterBaseURL = "https://openrouter.ai/api/v1"
)

// New returns a provider implementation for the given type.
func New(providerType, apiKey, model string) (Provider, error) {

	switch strings.ToUpper(strings.TrimSpace(providerType)) {
	case "", TypeGemini:
		return newGeminiProvider(apiKey, model, geminiBaseURL), nil

	case TypeOpenRouter:
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("openrouter requires a model, e.g. openai/gpt-4o-mini")
		}
		return newOpenRouterProvider(apiKey, model, openRouterBaseURL), nil

	default:
		return nil, fmt.Errorf("unsupported provider type %q", providerType)
	}
}
