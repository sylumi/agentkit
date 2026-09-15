package modeldata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/sylumi/agentkit/model"
)

type sourceProvider struct {
	Name   string    `json:"name"`
	API    string    `json:"api"`
	Models rawObject `json:"models"`
}

type sourceModel struct {
	Name             string           `json:"name"`
	ToolCall         *bool            `json:"tool_call"`
	StructuredOutput *bool            `json:"structured_output"`
	Temperature      *bool            `json:"temperature"`
	Reasoning        *bool            `json:"reasoning"`
	Modalities       model.Modalities `json:"modalities"`
	Limit            struct {
		Context *int64 `json:"context"`
		Input   *int64 `json:"input"`
		Output  *int64 `json:"output"`
	} `json:"limit"`
	Provider struct {
		API string `json:"api"`
	} `json:"provider"`
	ReasoningOptions []json.RawMessage `json:"reasoning_options"`
	Cost             *sourceCost       `json:"cost"`
	Status           string            `json:"status"`
}

type sourceCost struct {
	model.Rates
	Tiers []struct {
		model.Rates
		Tier struct {
			Type string `json:"type"`
			Size *int64 `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
	ContextOver200K *model.Rates `json:"context_over_200k"`
}

// DecodeModelsDev converts every provider and model in a models.dev download.
// It reads no environment variables and performs no I/O beyond the supplied JSON.
func DecodeModelsDev(r io.Reader, fetchedAt time.Time) (*Snapshot, error) {
	var root rawObject
	if err := decodeJSON(r, &root); err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}

	s := &Snapshot{
		Version:   Version,
		Source:    ModelsDevURL,
		FetchedAt: fetchedAt.UTC()}

	for _, providerKey := range slices.Sorted(maps.Keys(root)) {
		if err := validID(providerKey); err != nil {
			return nil, fmt.Errorf("models.dev.provider: %w", err)
		}
		var p sourceProvider
		if err := json.Unmarshal(root[providerKey], &p); err != nil {
			return nil, fmt.Errorf("models.dev.provider[%q]: %w", providerKey, err)
		}
		provider := model.ProviderInfo{ID: providerKey, Name: p.Name, BaseURL: p.API}
		for modelID, raw := range p.Models {
			m, err := convertModel(provider, modelID, raw)
			if err != nil {
				return nil, fmt.Errorf("models.dev.provider[%q].models[%q]: %w", providerKey, modelID, err)
			}
			s.Models = append(s.Models, m)
		}
	}
	s.Sort()
	return s, nil
}

func convertModel(provider model.ProviderInfo, id string, raw json.RawMessage) (model.ModelInfo, error) {
	var src *sourceModel
	if err := json.Unmarshal(raw, &src); err != nil {
		return model.ModelInfo{}, err
	}
	if src == nil {
		return model.ModelInfo{}, fmt.Errorf("expected a model object")
	}
	m := model.ModelInfo{
		Provider: provider,
		ID:       id,
		Name:     src.Name,
		BaseURL:  src.Provider.API,
		Capabilities: model.Capabilities{
			ToolCall:         src.ToolCall,
			StructuredOutput: src.StructuredOutput,
			Temperature:      src.Temperature,
			Reasoning:        src.Reasoning,
		},
		Modalities: src.Modalities,
		Limits: model.Limits{
			ContextTokens:   knownLimit(src.Limit.Context),
			MaxInputTokens:  knownLimit(src.Limit.Input),
			MaxOutputTokens: knownLimit(src.Limit.Output),
		},
		Status: src.Status,
	}
	var err error
	m.ReasoningOptions, err = convertReasoning(src.ReasoningOptions)
	if err != nil {
		return model.ModelInfo{}, err
	}
	if p := src.Cost; p != nil {
		m.Pricing = &model.Pricing{Rates: p.Rates}
		for i, tier := range p.Tiers {
			if tier.Tier.Size == nil {
				return model.ModelInfo{}, fmt.Errorf("cost.tiers[%d].tier.size: required", i)
			}
			m.Pricing.Tiers = append(m.Pricing.Tiers, model.PriceTier{Kind: tier.Tier.Type, Threshold: *tier.Tier.Size, Rates: tier.Rates})
		}
		if len(p.Tiers) == 0 && p.ContextOver200K != nil {
			m.Pricing.Tiers = append(m.Pricing.Tiers, model.PriceTier{Kind: "context", Threshold: 200000, Rates: *p.ContextOver200K})
		}
	}
	return m, nil
}

// models.dev uses zero for some unspecified limits. Prices and usage have
// different zero semantics and must not use this conversion.
func knownLimit(n *int64) *int64 {
	if n != nil && *n == 0 {
		return nil
	}
	return n
}

func convertReasoning(raw []json.RawMessage) (model.ReasoningOptions, error) {
	var result model.ReasoningOptions
	seen := make(map[string]bool)
	for i, data := range raw {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &kind); err != nil || kind.Type == "" {
			return result, fmt.Errorf("reasoning_options[%d].type: expected a non-empty string", i)
		}
		if kind.Type != "toggle" && kind.Type != "effort" && kind.Type != "budget_tokens" {
			continue
		}
		if seen[kind.Type] {
			return result, fmt.Errorf("reasoning_options[%d]: duplicate %s option", i, kind.Type)
		}
		seen[kind.Type] = true
		switch kind.Type {
		case "toggle":
			yes := true
			result.Toggle = &yes
		case "effort":
			var effort struct {
				Values []*string `json:"values"`
			}
			if err := json.Unmarshal(data, &effort); err != nil {
				return result, fmt.Errorf("reasoning_options[%d]: %w", i, err)
			}
			for _, value := range effort.Values {
				if value != nil && *value != "default" && !slices.Contains(result.Efforts, *value) {
					result.Efforts = append(result.Efforts, *value)
				}
			}
		case "budget_tokens":
			var budget model.TokenRange
			if err := json.Unmarshal(data, &budget); err != nil {
				return result, fmt.Errorf("reasoning_options[%d]: %w", i, err)
			}
			// Some providers declare -1 as a special budget value. It does not
			// establish a token boundary; retain any other known bound.
			if budget.Min != nil && *budget.Min == -1 {
				budget.Min = nil
			}
			if budget.Max != nil && *budget.Max == -1 {
				budget.Max = nil
			}
			result.Budget = &budget
		}
	}
	return result, nil
}

// rawObject defers record decoding and rejects duplicate IDs before a map
// could silently overwrite one of them.
type rawObject map[string]json.RawMessage

func (o *rawObject) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("expected an object")
	}
	result := make(rawObject)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key := token.(string)
		if _, exists := result[key]; exists {
			return fmt.Errorf("duplicate object key %q", key)
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return err
		}
		result[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	*o = result
	return nil
}
