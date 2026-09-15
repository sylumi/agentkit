package model

// ModelInfo describes a model; an LLM executes requests. Catalog declarations
// do not imply adapter support or account access, and are not request defaults.
type ModelInfo struct {
	Provider         ProviderInfo     `json:"provider"`
	ID               string           `json:"id"`
	Name             string           `json:"name,omitempty"`
	BaseURL          string           `json:"base_url,omitempty"` // Optional model-specific API root.
	Capabilities     Capabilities     `json:"capabilities"`
	Modalities       Modalities       `json:"modalities"`
	Limits           Limits           `json:"limits"`
	ReasoningOptions ReasoningOptions `json:"reasoning_options"`
	Pricing          *Pricing         `json:"pricing,omitempty"`
	Status           string           `json:"status,omitempty"`
}

// Capabilities preserves unknown (nil) separately from explicitly unsupported.
type Capabilities struct {
	ToolCall         *bool `json:"tool_call,omitempty"`
	StructuredOutput *bool `json:"structured_output,omitempty"`
	Temperature      *bool `json:"temperature,omitempty"`
	Reasoning        *bool `json:"reasoning,omitempty"`
}

// Modalities lists declared input and output formats, including formats an
// adapter may not yet support.
type Modalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

// Limits contains known positive token limits. Nil means unknown. These are
// model limits, not the output-token budget requested for a generation.
type Limits struct {
	ContextTokens   *int64 `json:"context_tokens,omitempty"`
	MaxInputTokens  *int64 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens *int64 `json:"max_output_tokens,omitempty"`
}

// ReasoningOptions describes available controls, not the selection for a call.
type ReasoningOptions struct {
	Toggle  *bool       `json:"toggle,omitempty"`
	Efforts []string    `json:"efforts,omitempty"`
	Budget  *TokenRange `json:"budget,omitempty"`
}

// TokenRange preserves unknown boundaries. A known boundary may be zero.
type TokenRange struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

// Rates contains reference USD prices per million tokens. Nil means unknown;
// an explicit zero is a known zero price. Rates are not a bill or cost estimate.
type Rates struct {
	Input       *float64 `json:"input,omitempty"`
	Output      *float64 `json:"output,omitempty"`
	CacheRead   *float64 `json:"cache_read,omitempty"`
	CacheWrite  *float64 `json:"cache_write,omitempty"`
	Reasoning   *float64 `json:"reasoning,omitempty"`
	InputAudio  *float64 `json:"input_audio,omitempty"`
	OutputAudio *float64 `json:"output_audio,omitempty"`
}

// Pricing preserves base rates and source-specific tiers without evaluating them.
type Pricing struct {
	Rates Rates       `json:"rates"`
	Tiers []PriceTier `json:"tiers,omitempty"`
}

// PriceTier retains the source's tier kind and threshold. Unknown kinds can be
// displayed or stored, but must not be interpreted as a supported billing rule.
type PriceTier struct {
	Kind      string `json:"kind"`
	Threshold int64  `json:"threshold"`
	Rates     Rates  `json:"rates"`
}
