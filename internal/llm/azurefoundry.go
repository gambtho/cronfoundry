package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

// defaultAzureAPIVersion is used when CallOptions does not specify one.
// Azure requires an api-version query parameter; we pin a recent, widely
// supported preview version. Callers can override via the endpoint if needed.
const defaultAzureAPIVersion = "2024-08-01-preview"

type azureFoundryProvider struct{}

// NewAzureFoundry returns a Provider backed by Azure AI Foundry (Azure OpenAI).
// It uses the openai-go/v3 client together with its `azure` subpackage, which
// handles Azure's URL shape (/openai/deployments/{deployment}/chat/completions)
// and `api-key` header authentication.
//
// Endpoint and Deployment must be supplied via CallOptions.
func NewAzureFoundry() Provider {
	return &azureFoundryProvider{}
}

func (p *azureFoundryProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	if opts.Endpoint == "" {
		return Usage{}, fmt.Errorf("azure-foundry: endpoint required")
	}
	if opts.Deployment == "" {
		return Usage{}, fmt.Errorf("azure-foundry: deployment required")
	}

	client := openai.NewClient(
		azure.WithEndpoint(opts.Endpoint, defaultAzureAPIVersion),
		azure.WithAPIKey(opts.APIKey),
		// Force the Authorization header unset: NewClient loads OPENAI_API_KEY
		// from the environment by default, which would otherwise attach a
		// bogus `Authorization: Bearer ...` header to every Azure request.
		option.WithHeaderDel("Authorization"),
	)

	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(m.Content),
					},
				},
			})
		case RoleUser:
			msgs = append(msgs, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(m.Content),
					},
				},
			})
		}
	}

	// The Azure middleware derives the deployment name from the JSON `model`
	// field and rewrites the URL accordingly. We therefore pass Deployment
	// (not Model) here; opts.Model is intentionally unused for Azure.
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(opts.Deployment),
		Messages: msgs,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(opts.MaxTokens))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		if len(evt.Choices) > 0 && evt.Choices[0].Delta.Content != "" {
			onChunk(StreamChunk{Delta: evt.Choices[0].Delta.Content})
		}
		if evt.Usage.PromptTokens > 0 || evt.Usage.CompletionTokens > 0 {
			usage.InputTokens = int(evt.Usage.PromptTokens)
			usage.OutputTokens = int(evt.Usage.CompletionTokens)
		}
	}
	if err := stream.Err(); err != nil {
		return usage, fmt.Errorf("azure-foundry chat: %w", err)
	}
	return usage, nil
}
