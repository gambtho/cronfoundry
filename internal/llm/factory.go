package llm

import "fmt"

// NewProvider returns a default-configured provider by its config name.
// For testing with mock endpoints, call the concrete constructors directly.
func NewProvider(name string) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(""), nil
	case "anthropic":
		return NewAnthropic(""), nil
	case "azure-foundry":
		return NewAzureFoundry(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, anthropic, azure-foundry)", name)
	}
}
