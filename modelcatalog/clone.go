package modelcatalog

import (
	"slices"

	"github.com/sylumi/agentkit/model"
)

// cloneModelInfo keeps callers from mutating the catalog through query results.
func cloneModelInfo(m model.ModelInfo) model.ModelInfo {
	m.Provider.APIKeyEnv = slices.Clone(m.Provider.APIKeyEnv)
	m.Capabilities = model.Capabilities{
		ToolCall: clone(m.Capabilities.ToolCall), StructuredOutput: clone(m.Capabilities.StructuredOutput),
		Temperature: clone(m.Capabilities.Temperature), Reasoning: clone(m.Capabilities.Reasoning),
	}
	m.Modalities.Input = slices.Clone(m.Modalities.Input)
	m.Modalities.Output = slices.Clone(m.Modalities.Output)
	m.Limits = model.Limits{
		ContextTokens: clone(m.Limits.ContextTokens), MaxInputTokens: clone(m.Limits.MaxInputTokens),
		MaxOutputTokens: clone(m.Limits.MaxOutputTokens),
	}
	m.ReasoningOptions.Toggle = clone(m.ReasoningOptions.Toggle)
	m.ReasoningOptions.Efforts = slices.Clone(m.ReasoningOptions.Efforts)
	if b := m.ReasoningOptions.Budget; b != nil {
		m.ReasoningOptions.Budget = &model.TokenRange{Min: clone(b.Min), Max: clone(b.Max)}
	}
	if p := m.Pricing; p != nil {
		m.Pricing = &model.Pricing{Rates: cloneRates(p.Rates), Tiers: slices.Clone(p.Tiers)}
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

func cloneRates(r model.Rates) model.Rates {
	return model.Rates{
		Input: clone(r.Input), Output: clone(r.Output), CacheRead: clone(r.CacheRead), CacheWrite: clone(r.CacheWrite),
		Reasoning: clone(r.Reasoning), InputAudio: clone(r.InputAudio), OutputAudio: clone(r.OutputAudio),
	}
}
