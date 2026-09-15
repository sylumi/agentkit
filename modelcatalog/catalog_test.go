package modelcatalog_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sylumi/agentkit/modelcatalog"
)

func fixtureCatalog(t *testing.T) *modelcatalog.Catalog {
	t.Helper()
	const snapshot = `{
  "version": 2,
  "source": "https://models.dev/api.json",
  "fetched_at": "2026-09-12T00:00:00Z",
  "models": [
    {
      "provider": {"id": "openai", "name": "OpenAI", "base_url": "https://api.openai.com/v1/", "api_key_env": ["OPENAI_API_KEY"]},
      "id": "gpt-4.1",
      "name": "GPT-4.1",
      "base_url": "https://deployment.example/v1/",
      "status": "preview",
      "capabilities": {"tool_call": true, "structured_output": false, "temperature": true, "reasoning": true},
      "modalities": {"input": ["text", "image"], "output": ["text"]},
      "limits": {"context_tokens": 100000, "max_input_tokens": 90000, "max_output_tokens": 10000},
      "reasoning_options": {"toggle": true, "efforts": ["low", "high"], "budget": {"min": 0, "max": 8192}},
      "pricing": {
        "rates": {"input": 2, "output": 8, "cache_read": 0, "cache_write": 1, "reasoning": 9, "input_audio": 3, "output_audio": 4},
        "tiers": [{"kind": "context", "threshold": 200000, "rates": {"input": 4, "output": 16}}]
      }
    },
    {"provider": {"id": "openai", "name": "OpenAI", "base_url": "https://api.openai.com/v1/", "api_key_env": ["OPENAI_API_KEY"]}, "id": "family/model"},
    {"provider": {"id": "azure"}, "id": "gpt-4.1", "capabilities": {"tool_call": false}}
  ]
}`
	c, err := modelcatalog.Load(strings.NewReader(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogQueriesAndIndependentResults(t *testing.T) {
	c := fixtureCatalog(t)
	original, ok := c.Lookup("openai", "gpt-4.1")
	if !ok {
		t.Fatal("model missing")
	}
	if original.Name != "GPT-4.1" || original.Status != "preview" || original.BaseURL != "https://deployment.example/v1/" || original.Provider.Name != "OpenAI" || original.Provider.BaseURL != "https://api.openai.com/v1/" {
		t.Fatal("lost model or provider details")
	}
	azure, ok := c.Lookup("azure", "gpt-4.1")
	if !ok || azure.Capabilities.ToolCall == nil || *azure.Capabilities.ToolCall {
		t.Fatal("provider identity was lost")
	}
	if _, ok := c.Lookup("missing", "gpt-4.1"); ok {
		t.Fatal("invented provider")
	}
	if _, ok := c.Lookup("openai", "family/model"); !ok {
		t.Fatal("split a model ID containing a slash")
	}
	list := c.List("")
	if len(list) != 3 || list[0].Provider.ID != "azure" || list[1].ID != "family/model" || len(c.List("absent")) != 0 {
		t.Fatal("incorrect list order/filter")
	}

	// Each concurrent caller edits its own returned description.
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			m, _ := c.Lookup("openai", "gpt-4.1")
			m.Provider.APIKeyEnv[0] = "CHANGED"
			*m.Capabilities.ToolCall = false
			*m.Capabilities.StructuredOutput = true
			*m.Capabilities.Temperature = false
			*m.Capabilities.Reasoning = false
			m.Modalities.Input[0], m.Modalities.Output[0] = "changed", "changed"
			*m.Limits.ContextTokens, *m.Limits.MaxInputTokens, *m.Limits.MaxOutputTokens = 1, 1, 1
			*m.ReasoningOptions.Toggle = false
			m.ReasoningOptions.Efforts[0] = "changed"
			*m.ReasoningOptions.Budget.Min, *m.ReasoningOptions.Budget.Max = 1, 1
			*m.Pricing.Rates.Input, *m.Pricing.Rates.Output, *m.Pricing.Rates.CacheRead = 1, 1, 1
			*m.Pricing.Rates.CacheWrite = 99
			*m.Pricing.Rates.Reasoning, *m.Pricing.Rates.InputAudio, *m.Pricing.Rates.OutputAudio = 1, 1, 1
			*m.Pricing.Tiers[0].Rates.Input = 1
			m.Pricing.Tiers[0].Kind = "changed"
			listed := c.List("openai")
			listed[1].Provider.APIKeyEnv[0] = "CHANGED"
			*listed[1].Pricing.Tiers[0].Rates.Output = 1
		})
	}
	wg.Wait()
	again, _ := c.Lookup("openai", "gpt-4.1")
	if !reflect.DeepEqual(original, again) {
		t.Fatal("caller edits mutated shared catalog data")
	}
}

func TestBuiltinLoadsWithoutValidCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "invalid value with spaces")
	c, err := modelcatalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	info, ok := c.Lookup("openai", "gpt-4.1")
	if !ok || info.Provider.ID != "openai" || info.Limits.ContextTokens == nil {
		t.Fatal("missing embedded model data")
	}
	if info.Provider.BaseURL != "" || len(info.Provider.APIKeyEnv) != 0 {
		t.Fatal("catalog contains injected OpenAI connection defaults")
	}
	for _, id := range []string{"anthropic", "google", "deepseek", "azure"} {
		list := c.List(id)
		if len(list) == 0 {
			t.Fatalf("missing embedded provider %q", id)
		}
		for _, m := range list {
			found, ok := c.Lookup(id, m.ID)
			if !ok || found.Provider.ID != id {
				t.Fatalf("lookup failed for %q/%q", id, m.ID)
			}
			if len(found.Provider.APIKeyEnv) != 0 {
				t.Fatalf("unconfirmed authentication defaults for %q", id)
			}
		}
	}
}
