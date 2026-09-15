package openaimodel

import (
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
)

func TestConfig(t *testing.T) {
	cfg := Config{Model: model.ModelInfo{ID: "test-model", Provider: model.ProviderInfo{ID: "openai"}}, APIKey: "test-key", BaseURL: "https://example.com", HTTPClient: &http.Client{}}
	normalized, err := normalizeConfig(cfg)
	if err != nil || normalized.BaseURL != "https://example.com/" || normalized.HTTPClient != cfg.HTTPClient || cfg.BaseURL != "https://example.com" {
		t.Fatalf("normalize: %+v, %v", normalized, err)
	}
	if _, err := NewModel(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "default-test-key")
	cfg := Config{Model: model.ModelInfo{ID: "test", Provider: model.ProviderInfo{ID: "openai", BaseURL: "https://api.openai.com/v1/"}}}
	normalized, err := normalizeConfig(cfg)
	if err != nil || normalized.APIKey != "" || normalized.Model.Provider.Name != "" {
		t.Fatalf("explicit config: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"missing URL", func(c *Config) { c.Model.Provider.BaseURL = "" }, "config.base_url:"},
		{"SDK option cannot supply URL", func(c *Config) {
			c.Model.Provider.BaseURL = ""
			c.Options = []option.RequestOption{option.WithBaseURL("https://options.invalid/")}
		}, "config.base_url:"},
		{"invalid URL", func(c *Config) { c.BaseURL = "https://example.com/%zz" }, "config.base_url:"},
		{"unsupported scheme", func(c *Config) { c.BaseURL = "ftp://example.com" }, "config.base_url:"},
		{"missing host", func(c *Config) { c.BaseURL = "https:///v1" }, "config.base_url:"},
		{"query", func(c *Config) { c.BaseURL = "https://example.com/v1?api-version=not-real" }, "query and fragment are not supported"},
		{"empty query", func(c *Config) { c.BaseURL = "https://example.com/v1?" }, "query and fragment are not supported"},
		{"fragment", func(c *Config) { c.BaseURL = "https://example.com/v1#section" }, "query and fragment are not supported"},
		{"empty fragment", func(c *Config) { c.BaseURL = "https://example.com/v1#" }, "query and fragment are not supported"},
		{"model query", func(c *Config) { c.Model.BaseURL = "https://example.com/v1?region=test" }, "query and fragment are not supported"},
		{"provider query", func(c *Config) { c.Model.Provider.BaseURL = "https://example.com/v1?region=test" }, "query and fragment are not supported"},
		{"provider URL", func(c *Config) { c.Model.Provider.BaseURL = "relative" }, "config.base_url:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := cfg
			tc.change(&next)
			_, err := NewModel(next)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "not-real") {
				t.Fatal("configuration error disclosed a value")
			}
		})
	}
	t.Setenv("AGENTKIT_TEST_FIRST_KEY", "first-test-key")
	cfg.Model.Provider.APIKeyEnv = []string{"AGENTKIT_TEST_FIRST_KEY", "OPENAI_API_KEY"}
	cfg.APIKey = ""
	if got, err := normalizeConfig(cfg); err != nil || got.APIKey != "first-test-key" {
		t.Fatal("first nonempty environment value was not selected")
	}
	cfg.APIKey = "explicit-test-key"
	if got, err := normalizeConfig(cfg); err != nil || got.APIKey != "explicit-test-key" {
		t.Fatal("explicit key did not override environment value")
	}
}

func TestCredentialLookup(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("AGENTKIT_TEST_CUSTOM_KEY", "custom-test-key")
	t.Setenv("AGENTKIT_TEST_EMPTY_KEY", "")
	for _, tc := range []struct {
		name             string
		provider         model.ProviderInfo
		wantURL, wantKey string
	}{
		{"OpenAI gets no key default", model.ProviderInfo{ID: "openai", BaseURL: "https://api.openai.com/v1/"}, "https://api.openai.com/v1/", ""},
		{"explicit OpenAI key variable", model.ProviderInfo{ID: "openai", BaseURL: "https://api.openai.com/v1/", APIKeyEnv: []string{"OPENAI_API_KEY"}}, "https://api.openai.com/v1/", "openai-test-key"},
		{"provider overrides", model.ProviderInfo{ID: "openai", BaseURL: "https://gateway.example/v1", APIKeyEnv: []string{"AGENTKIT_TEST_CUSTOM_KEY"}}, "https://gateway.example/v1/", "custom-test-key"},
		{"empty list disables lookup", model.ProviderInfo{ID: "openai", BaseURL: "https://api.openai.com/v1/", APIKeyEnv: []string{}}, "https://api.openai.com/v1/", ""},
		{"explicit empty variable does not fall back", model.ProviderInfo{ID: "openai", BaseURL: "https://api.openai.com/v1/", APIKeyEnv: []string{"AGENTKIT_TEST_EMPTY_KEY"}}, "https://api.openai.com/v1/", ""},
		{"other provider gets no key default", model.ProviderInfo{ID: "custom", BaseURL: "https://custom.invalid"}, "https://custom.invalid/", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := normalizeConfig(Config{Model: model.ModelInfo{ID: "test", Provider: tc.provider}})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BaseURL != tc.wantURL || cfg.APIKey != tc.wantKey {
				t.Fatal("connection defaults or explicit overrides were not respected")
			}
		})
	}
}

func TestOnlySelectedURLIsValidated(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL, modelURL, providerURL, want string
	}{
		{"config override", "https://config.invalid/v1", "invalid-model-url", "invalid-provider-url", "https://config.invalid/v1/"},
		{"model override", "", "https://model.invalid/v2", "invalid-provider-url", "https://model.invalid/v2/"},
		{"provider fallback", "", "", "https://provider.invalid/v3", "https://provider.invalid/v3/"},
		{"override query and fragment", "https://config.invalid/v1", "https://model.invalid/v2?region=test", "https://provider.invalid/v3#section", "https://config.invalid/v1/"},
		{"encoded path", "https://example.com/a%2Fb", "", "", "https://example.com/a%2Fb/"},
		{"encoded delimiters", "https://example.com/a%3Fb%23c", "", "", "https://example.com/a%3Fb%23c/"},
		{"URL user info", "https://user:password@example.com/v1", "", "", "https://user:password@example.com/v1/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := normalizeConfig(Config{
				Model:   model.ModelInfo{ID: "test", BaseURL: tc.modelURL, Provider: model.ProviderInfo{ID: "custom", BaseURL: tc.providerURL}},
				BaseURL: tc.baseURL,
			})
			if err != nil || cfg.BaseURL != tc.want {
				t.Fatalf("selected URL = %q, error = %v; want %q", cfg.BaseURL, err, tc.want)
			}
		})
	}
}
