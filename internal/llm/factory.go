package llm

import "fmt"

// NewProvider returns a default-configured provider by its config name.
// baseURL overrides the default API endpoint; pass "" for production defaults.
// For openai and anthropic, baseURL redirects all API calls (useful in tests).
// For azure-foundry, the endpoint must be "" here — it is supplied per-call via
// CallOptions.Endpoint; passing a non-empty baseURL returns an error.
func NewProvider(name, baseURL string) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(baseURL), nil
	case "anthropic":
		return NewAnthropic(baseURL), nil
	case "azure-foundry":
		if baseURL != "" {
			return nil, fmt.Errorf("azure-foundry endpoint is set per-call via CallOptions; pass baseURL=\"\"")
		}
		return NewAzureFoundry(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, anthropic, azure-foundry)", name)
	}
}
