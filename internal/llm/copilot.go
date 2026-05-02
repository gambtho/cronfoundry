package llm

import (
	"github.com/openai/openai-go/option"
)

const copilotBaseURL = "https://api.githubcopilot.com"

// NewCopilotEnterprise returns a ToolCapableProvider backed by the GitHub
// Copilot Enterprise chat completions API (OpenAI-compatible).
//
// The caller must supply a Copilot **IDE token** (NOT the OAuth access
// token) as CallOptions.APIKey. IDE tokens are minted by
// webapi.ResolveCopilotToken via api.github.com/copilot_internal/v2/token
// and live ~30 min; the runner fetches one per run from
// /internal/runs/{id}/copilot-token.
//
// Copilot-Integration-Id must be one of GitHub's accepted values
// ("vscode-chat", "jetbrains", "vim", etc.) — the endpoint rejects
// arbitrary identifiers. We use "vscode-chat" because it's the canonical
// chat-completion path; arbitrary user-agents like "cronfoundry" produce
// 401s from the model picker.
func NewCopilotEnterprise() ToolCapableProvider {
	return newCopilotEnterpriseWithBase(copilotBaseURL)
}

func newCopilotEnterpriseWithBase(baseURL string) ToolCapableProvider {
	return &openAIProvider{
		baseURL: baseURL,
		extraOpts: []option.RequestOption{
			option.WithHeader("Editor-Version", "vscode/1.85.1"),
			option.WithHeader("Editor-Plugin-Version", "copilot-chat/0.12.0"),
			option.WithHeader("Copilot-Integration-Id", "vscode-chat"),
		},
	}
}
