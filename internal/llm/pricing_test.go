package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCostCents(t *testing.T) {
	cases := []struct {
		name                string
		provider, model     string
		inputTok, outputTok int
		wantCents           int
	}{
		{
			name:     "openai gpt-4o-mini cheap",
			provider: "openai", model: "gpt-4o-mini",
			inputTok: 1_000_000, outputTok: 1_000_000,
			// $0.15 in + $0.60 out per 1M = $0.75 = 75 cents.
			wantCents: 75,
		},
		{
			name:     "openai gpt-4o",
			provider: "openai", model: "gpt-4o",
			inputTok: 1_000_000, outputTok: 1_000_000,
			// $2.50 in + $10.00 out per 1M = $12.50 = 1250 cents.
			wantCents: 1250,
		},
		{
			name:     "anthropic claude sonnet 4.5",
			provider: "anthropic", model: "claude-sonnet-4-5",
			inputTok: 1_000_000, outputTok: 1_000_000,
			// $3.00 in + $15.00 out per 1M = $18.00 = 1800 cents.
			wantCents: 1800,
		},
		{
			name:     "azure foundry returns 0 (BYOK)",
			provider: "azure-foundry", model: "gpt-4o",
			inputTok: 1_000_000, outputTok: 1_000_000,
			wantCents: 0,
		},
		{
			name:     "unknown model returns 0",
			provider: "openai", model: "gpt-fictional-9",
			inputTok: 1_000_000, outputTok: 1_000_000,
			wantCents: 0,
		},
		{
			name:     "sub-penny rounds down",
			provider: "openai", model: "gpt-4o-mini",
			inputTok: 1000, outputTok: 1000,
			// $0.00015 + $0.0006 = $0.00075 → 0 cents (sub-penny floors).
			wantCents: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CostCents(tc.provider, tc.model, Usage{
				InputTokens: tc.inputTok, OutputTokens: tc.outputTok,
			})
			assert.Equal(t, tc.wantCents, got)
		})
	}
}
