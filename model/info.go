package model

import "slices"

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

// Clone returns an independent copy, including all nested pointers and slices.
// Nil pointers and nil versus empty slices are preserved.
func (m ModelInfo) Clone() ModelInfo {
	m.Provider.APIKeyEnv = slices.Clone(m.Provider.APIKeyEnv)
	m.Capabilities = Capabilities{
		ToolCall: clone(m.Capabilities.ToolCall), StructuredOutput: clone(m.Capabilities.StructuredOutput),
		Temperature: clone(m.Capabilities.Temperature), Reasoning: clone(m.Capabilities.Reasoning),
	}
	m.Modalities.Input = slices.Clone(m.Modalities.Input)
	m.Modalities.Output = slices.Clone(m.Modalities.Output)
	m.Limits = Limits{
		ContextTokens: clone(m.Limits.ContextTokens), MaxInputTokens: clone(m.Limits.MaxInputTokens),
		MaxOutputTokens: clone(m.Limits.MaxOutputTokens),
	}
	m.ReasoningOptions.Toggle = clone(m.ReasoningOptions.Toggle)
	m.ReasoningOptions.Efforts = slices.Clone(m.ReasoningOptions.Efforts)
	if b := m.ReasoningOptions.Budget; b != nil {
		m.ReasoningOptions.Budget = &TokenRange{Min: clone(b.Min), Max: clone(b.Max)}
	}
	if p := m.Pricing; p != nil {
		m.Pricing = &Pricing{Rates: cloneRates(p.Rates), Tiers: slices.Clone(p.Tiers)}
		for i := range m.Pricing.Tiers {
			m.Pricing.Tiers[i].Rates = cloneRates(p.Tiers[i].Rates)
		}
	}
	return m
}

func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneRates(r Rates) Rates {
	return Rates{
		Input: clone(r.Input), Output: clone(r.Output), CacheRead: clone(r.CacheRead), CacheWrite: clone(r.CacheWrite),
		Reasoning: clone(r.Reasoning), InputAudio: clone(r.InputAudio), OutputAudio: clone(r.OutputAudio),
	}
}
