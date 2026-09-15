package modeldata

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *Snapshot {
	t.Helper()
	const raw = `{
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "env": ["OPENAI_API_KEY"],
    "models": {
      "gpt-4.1": {
        "id": "gpt-4.1",
        "name": "GPT-4.1",
        "tool_call": true,
        "structured_output": false,
        "reasoning": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 100000, "input": 90000, "output": 10000},
        "reasoning_options": [
          {"type": "toggle"},
          {"type": "effort", "values": ["default", null, "low", "high", "low"]},
          {"type": "budget_tokens", "min": 0, "max": 8192},
          {"type": "future-control", "values": {"not": "an effort array"}}
        ],
        "cost": {
          "input": 2, "output": 8, "cache_read": 0, "reasoning": 9,
          "input_audio": 3, "output_audio": 4,
          "tiers": [{"input": 4, "output": 16, "tier": {"type": "context", "size": 200000}}],
          "context_over_200k": {"input": 4, "output": 16}
        },
        "status": "preview",
        "experimental": {"modes": {"fast": {"cost": {"input": 99}}}}
      },
      "family/model": {
        "limit": {"context": 0, "input": 0, "output": 4},
        "provider": {"api": "https://deployment.example/v1/"}
      }
    }
  },
  "azure": {
    "id": "azure",
    "env": ["AZURE_RESOURCE_NAME", "AZURE_API_KEY"],
    "models": {"gpt-4.1": {"tool_call": false}}
  }
}`
	s, err := DecodeModelsDev(strings.NewReader(raw), time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestModelsDevImportsAllProviders(t *testing.T) {
	raw := `{"openai":{"models":{"shared":{"tool_call":true}}},"custom":{"models":{"unknown":{},"shared":{"tool_call":false}}}}`
	s, err := DecodeModelsDev(strings.NewReader(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Models) != 3 || s.Models[0].Provider.ID != "custom" || s.Models[1].Provider.ID != "custom" || s.Models[2].Provider.ID != "openai" || s.Models[0].ID != "shared" || s.Models[1].ID != "unknown" || s.Models[2].ID != "shared" {
		t.Fatal("all-provider import lost records or ordering")
	}
	if s.Models[0].Capabilities.ToolCall == nil || *s.Models[0].Capabilities.ToolCall || s.Models[1].Capabilities.ToolCall != nil {
		t.Fatal("non-tool models were filtered or unknown capability was changed")
	}
}

func TestModelsDevPreservesSourceURLs(t *testing.T) {
	t.Setenv("DEPLOYMENT_HOST", "local.example")
	for _, api := range []string{
		"https://${DEPLOYMENT_HOST}/v1",
		"https://example.com/accounts/${ACCOUNT_ID}/v1",
		"${BASE_URL}/v1",
		"https://example.com/v1",
	} {
		t.Run(api, func(t *testing.T) {
			raw := `{"custom":{"api":"` + api + `","env":["DEPLOYMENT_HOST","CUSTOM_API_KEY"],"models":{"m":{"provider":{"api":"` + api + `"}}}}}`
			s, err := DecodeModelsDev(strings.NewReader(raw), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if s.Models[0].Provider.BaseURL != api || s.Models[0].BaseURL != api || len(s.Models[0].Provider.APIKeyEnv) != 0 {
				t.Fatal("changed a source URL or guessed deployment/auth configuration")
			}
		})
	}
}

func TestModelsDevSpecialBudgetIsNotATokenBoundary(t *testing.T) {
	for _, bounds := range []string{`"min":-1,"max":8192`, `"min":0,"max":-1`} {
		raw := `{"custom":{"models":{"m":{"reasoning_options":[{"type":"budget_tokens",` + bounds + `}]}}}}`
		s, err := DecodeModelsDev(strings.NewReader(raw), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		b := s.Models[0].ReasoningOptions.Budget
		if b == nil {
			t.Fatal("lost budget control")
		}
		if strings.Contains(bounds, `"min":-1`) {
			if b.Min != nil || b.Max == nil || *b.Max != 8192 {
				t.Fatal("special value became a lower bound or valid upper bound was lost")
			}
		} else if b.Min == nil || *b.Min != 0 || b.Max != nil {
			t.Fatal("zero lower bound was lost or special value became an upper bound")
		}
	}
}

func TestModelsDevPreservesMeaning(t *testing.T) {
	s := fixture(t)
	if len(s.Models) != 3 || s.Models[0].Provider.ID != "azure" || s.Models[1].Provider.ID != "openai" || s.Models[2].Provider.ID != "openai" {
		t.Fatal("models were not sorted by provider and model ID")
	}
	if len(s.Models[0].Provider.APIKeyEnv) != 0 || s.Models[0].Provider.BaseURL != "" {
		t.Fatal("guessed Azure credentials or routing")
	}
	p := s.Models[1].Provider
	if p.BaseURL != "" || len(p.APIKeyEnv) != 0 {
		t.Fatal("catalog import injected OpenAI connection defaults")
	}
	if p.Name != "OpenAI" || !reflect.DeepEqual(p, s.Models[2].Provider) || s.Models[0].Provider.Name != "" {
		t.Fatal("provider details were lost when attached to models")
	}
	unknown, m := s.Models[1], s.Models[2]
	if unknown.ID != "family/model" || unknown.Name != "" || unknown.Limits.ContextTokens != nil || unknown.Limits.MaxInputTokens != nil || *unknown.Limits.MaxOutputTokens != 4 {
		t.Fatal("zero limits were not normalized or a missing name was filled in")
	}
	if unknown.Capabilities.ToolCall != nil || unknown.Pricing != nil || unknown.BaseURL != "https://deployment.example/v1/" {
		t.Fatal("invented capabilities/prices or lost model-specific URL")
	}
	if m.BaseURL != "" || m.Capabilities.StructuredOutput == nil || *m.Capabilities.StructuredOutput || m.Capabilities.Temperature != nil {
		t.Fatal("provider URL was copied or unknown/false capabilities were merged")
	}
	if *m.Limits.ContextTokens != 100000 || *m.Limits.MaxInputTokens != 90000 || *m.Limits.MaxOutputTokens != 10000 {
		t.Fatal("token limits were conflated")
	}
	r := m.ReasoningOptions
	if r.Toggle == nil || !*r.Toggle || !reflect.DeepEqual(r.Efforts, []string{"low", "high"}) || r.Budget == nil || *r.Budget.Min != 0 || *r.Budget.Max != 8192 {
		t.Fatal("reasoning controls were lost or inferred")
	}
	if m.Pricing == nil || *m.Pricing.Rates.CacheRead != 0 || m.Pricing.Rates.CacheWrite != nil || *m.Pricing.Rates.Input != 2 || len(m.Pricing.Tiers) != 1 {
		t.Fatal("unknown/zero prices, experimental mode, or legacy tier handling changed base prices")
	}
	if *m.Pricing.Rates.Reasoning != 9 || *m.Pricing.Rates.InputAudio != 3 || *m.Pricing.Rates.OutputAudio != 4 || *m.Pricing.Tiers[0].Rates.Input != 4 {
		t.Fatal("lost optional prices")
	}
	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(&buf)
	if err != nil || !reflect.DeepEqual(s, restored) {
		t.Fatalf("snapshot round trip: %v", err)
	}
}

func TestModelsDevLegacyAndUnknownTiers(t *testing.T) {
	for _, cost := range []string{
		`{"context_over_200k":{"input":2}}`,
		`{"tiers":[{"input":2,"tier":{"type":"future-tier","size":42}}]}`,
	} {
		s, err := DecodeModelsDev(strings.NewReader(`{"custom":{"models":{"m":{"cost":`+cost+`}}}}`), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		p := s.Models[0].Pricing
		if p == nil || len(p.Tiers) != 1 || *p.Tiers[0].Rates.Input != 2 {
			t.Fatal("lost tier")
		}
		if strings.Contains(cost, "future-tier") {
			if p.Tiers[0].Kind != "future-tier" || p.Tiers[0].Threshold != 42 {
				t.Fatal("changed unknown tier")
			}
		} else if p.Tiers[0].Kind != "context" || p.Tiers[0].Threshold != 200000 {
			t.Fatal("wrong legacy threshold")
		}
	}
}

func TestModelsDevRejectsInvalidData(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"malformed provider", `{"openai":{"models":{"m":{}}},"custom":{"models":"unsupported format"}}`, `provider["custom"]`},
		{"duplicate provider", `{"openai":{},"openai":{}}`, "duplicate object key"},
		{"duplicate model", `{"openai":{"models":{"m":{},"m":{}}}}`, "duplicate object key"},
		{"wrong capability type", `{"openai":{"models":{"m":{"tool_call":"yes"}}}}`, "tool_call"},
		{"fractional limit", `{"openai":{"models":{"m":{"limit":{"output":1.5}}}}}`, "output"},
		{"missing tier threshold", `{"openai":{"models":{"m":{"cost":{"tiers":[{"tier":{"type":"context"}}]}}}}}`, "tier.size"},
		{"wrong efforts", `{"openai":{"models":{"m":{"reasoning_options":[{"type":"effort","values":[7]}]}}}}`, "reasoning_options"},
		{"null model", `{"openai":{"models":{"m":null}}}`, "model object"},
		{"trailing JSON", `{"openai":{"models":{"m":{}}}} {}`, "one JSON value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeModelsDev(strings.NewReader(tc.raw), time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want %q", err, tc.want)
			}
		})
	}
}
