package llm

import (
	"github.com/openai/openai-go/option"
)

const copilotBaseURL = "https://api.githubcopilot.com"

// NewCopilotEnterprise returns a ToolCapableProvider backed by the GitHub
// Copilot Enterprise chat completions API (OpenAI-compatible).
// The caller must supply a valid OAuth access token as CallOptions.APIKey.
func NewCopilotEnterprise() ToolCapableProvider {
	return newCopilotEnterpriseWithBase(copilotBaseURL)
}

func newCopilotEnterpriseWithBase(baseURL string) ToolCapableProvider {
	return &openAIProvider{
		baseURL: baseURL,
		extraOpts: []option.RequestOption{
			option.WithHeader("Editor-Version", "cronfoundry/1.0"),
			option.WithHeader("Copilot-Integration-Id", "cronfoundry"),
		},
	}
}
