package openaimodel

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestConfig(t *testing.T) {
	cfg := Config{Model: model.ModelInfo{ID: "test-model"}, APIKey: "test-key", BaseURL: "https://example.com", HTTPClient: &http.Client{}}
	normalized, err := normalizeConfig(cfg)
	if err != nil || normalized.BaseURL != "https://example.com/" || normalized.HTTPClient != cfg.HTTPClient || cfg.BaseURL != "https://example.com" {
		t.Fatalf("normalize: %+v, %v", normalized, err)
	}
	cfg.Model.ID = ""
	if _, err := NewModel(cfg); err == nil || !strings.Contains(err.Error(), "openai: config.model:") {
		t.Fatalf("model error = %v", err)
	}
	cfg.Model.ID = "test-model"
	if _, err := NewModel(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "default-test-key")
	cfg, err := normalizeConfig(Config{Model: model.ModelInfo{ID: "test"}})
	if err != nil || cfg.HTTPClient != nil || cfg.BaseURL != "https://api.openai.com/v1/" || cfg.Model.Provider.ID != "openai" {
		t.Fatalf("default config: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"model whitespace", func(c *Config) { c.Model.ID = " test" }, "config.model:"},
		{"provider without ID", func(c *Config) { c.Model.Provider = model.ProviderInfo{Name: "test"} }, "provider.id:"},
		{"invalid env", func(c *Config) { c.Model.Provider.APIKeyEnv = []string{"KEY=value"} }, "api_key_env:"},
		{"invalid key", func(c *Config) { c.APIKey = "synthetic invalid value" }, "config.api_key:"},
		{"invalid URL", func(c *Config) { c.BaseURL = "https://user:password@example.com" }, "config.base_url:"},
		{"model query", func(c *Config) { c.Model.BaseURL = "https://example.com?token=not-real" }, "model.base_url:"},
		{"provider URL", func(c *Config) { c.Model.Provider.BaseURL = "relative" }, "provider.base_url:"},
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
	t.Setenv("AGENTKIT_TEST_INVALID_KEY", "synthetic invalid value")
	cfg.Model.Provider.APIKeyEnv = []string{"AGENTKIT_TEST_INVALID_KEY", "OPENAI_API_KEY"}
	cfg.APIKey = ""
	if _, err := NewModel(cfg); err == nil {
		t.Fatal("invalid first credential silently fell back")
	}
	cfg.APIKey = "explicit-test-key"
	if _, err := NewModel(cfg); err != nil {
		t.Fatal("explicit key did not override invalid environment value")
	}
}

func TestOpenAIConnectionDefaultsAndOverrides(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("AGENTKIT_TEST_CUSTOM_KEY", "custom-test-key")
	t.Setenv("AGENTKIT_TEST_EMPTY_KEY", "")
	for _, tc := range []struct {
		name             string
		provider         model.ProviderInfo
		wantURL, wantKey string
	}{
		{"catalog without defaults", model.ProviderInfo{ID: "openai"}, "https://api.openai.com/v1/", "openai-test-key"},
		{"provider overrides", model.ProviderInfo{ID: "openai", BaseURL: "https://gateway.example/v1", APIKeyEnv: []string{"AGENTKIT_TEST_CUSTOM_KEY"}}, "https://gateway.example/v1/", "custom-test-key"},
		{"empty list disables lookup", model.ProviderInfo{ID: "openai", APIKeyEnv: []string{}}, "https://api.openai.com/v1/", ""},
		{"explicit empty variable does not fall back", model.ProviderInfo{ID: "openai", APIKeyEnv: []string{"AGENTKIT_TEST_EMPTY_KEY"}}, "https://api.openai.com/v1/", ""},
		{"other provider gets no defaults", model.ProviderInfo{ID: "custom"}, "", ""},
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
