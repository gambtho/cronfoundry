package llm

import (
	"fmt"
	"os"
)

// NewProvider returns a default-configured provider by its config name.
// For testing with mock endpoints, call NewOpenAI/NewAnthropic directly.
// CRONFOUNDRY_OPENAI_BASE_URL and CRONFOUNDRY_ANTHROPIC_BASE_URL override
// the respective API endpoints (useful in tests and local development).
func NewProvider(name string) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(os.Getenv("CRONFOUNDRY_OPENAI_BASE_URL")), nil
	case "anthropic":
		return NewAnthropic(os.Getenv("CRONFOUNDRY_ANTHROPIC_BASE_URL")), nil
	case "azure-foundry":
		return NewAzureFoundry(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, anthropic, azure-foundry)", name)
	}
}
