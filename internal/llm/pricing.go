package llm

// pricePer1M is USD cents per 1,000,000 tokens, as fixed-point integers.
// 75 means $0.75. Azure AI Foundry is omitted — that provider is BYOK and
// the customer is billed by Azure directly, so we do not compute cost.
type pricePer1M struct{ in, out int }

// priceTable is a minimal lookup for public-list pricing as of 2026-Q1.
// Unknown (provider, model) combinations return 0 rather than erroring —
// a run must not fail because we haven't catalogued a new model. Operators
// can PR new rows; stale rows are harmless (they just under/over-report).
var priceTable = map[string]map[string]pricePer1M{
	"openai": {
		"gpt-4o-mini": {in: 15, out: 60},
		"gpt-4o":      {in: 250, out: 1000},
		"gpt-5.1":     {in: 300, out: 600},
	},
	// OpenRouter passes through provider pricing; rows here are approximate
	// public-list prices as of 2026-Q1 for cost estimation only.
	"openrouter": {
		"openai/gpt-4o-mini":                     {in: 15, out: 60},
		"openai/gpt-4o":                          {in: 250, out: 1000},
		"anthropic/claude-haiku-4.5":             {in: 100, out: 500},
		"anthropic/claude-sonnet-4.5":            {in: 300, out: 1500},
		"meta-llama/llama-3.3-70b-instruct":      {in: 59, out: 79},
		"google/gemini-2.0-flash-001":            {in: 10, out: 40},
	},
	"anthropic": {
		"claude-haiku-4-5":  {in: 100, out: 500},
		"claude-sonnet-4-5": {in: 300, out: 1500},
		"claude-opus-4-6":   {in: 1500, out: 7500},
	},
}

// CostCents returns the run cost in whole cents (floored) for the given
// provider, model, and token usage. Returns 0 for unknown entries so the
// finalize path never emits a negative or arbitrary value.
func CostCents(provider, model string, u Usage) int {
	models, ok := priceTable[provider]
	if !ok {
		return 0
	}
	p, ok := models[model]
	if !ok {
		return 0
	}
	// Math: (tokens * cents_per_1M) / 1_000_000. Use int64 to avoid overflow
	// at token counts in the hundreds of millions.
	inCents := int64(u.InputTokens) * int64(p.in) / 1_000_000
	outCents := int64(u.OutputTokens) * int64(p.out) / 1_000_000
	return int(inCents + outCents)
}
