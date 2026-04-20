package llm

import (
	"fmt"
	"os"
)

// NewProvider returns a default-configured provider by its config name.
// For testing with mock endpoints, call the concrete constructors directly.
// CRONFOUNDRY_OPENAI_BASE_URL overrides the OpenAI API endpoint (useful in tests).
func NewProvider(name string) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(os.Getenv("CRONFOUNDRY_OPENAI_BASE_URL")), nil
	case "anthropic":
		return NewAnthropic(""), nil
	case "azure-foundry":
		return NewAzureFoundry(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, anthropic, azure-foundry)", name)
	}
}
