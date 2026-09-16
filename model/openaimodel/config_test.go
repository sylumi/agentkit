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
	cfg := Config{Model: model.ModelInfo{ID: "test"}, BaseURL: "https://example.com/v1/"}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"missing URL", func(c *Config) { c.BaseURL = "" }, "config.base_url: required"},
		{"metadata and SDK options cannot supply URL", func(c *Config) {
			c.BaseURL = ""
			c.Model.BaseURL = "https://model.invalid/"
			c.Model.Provider.BaseURL = "https://provider.invalid/"
			c.Options = []option.RequestOption{option.WithBaseURL("https://options.invalid/")}
		}, "config.base_url: required"},
		{"invalid URL", func(c *Config) { c.BaseURL = "https://example.com/%zz" }, "config.base_url:"},
		{"unsupported scheme", func(c *Config) { c.BaseURL = "ftp://example.com" }, "config.base_url:"},
		{"missing host", func(c *Config) { c.BaseURL = "https:///v1" }, "config.base_url:"},
		{"URL username and password", func(c *Config) { c.BaseURL = "https://synthetic-user:password@example.com/v1" }, "userinfo is not supported"},
		{"URL username", func(c *Config) { c.BaseURL = "https://synthetic-user@example.com/v1" }, "userinfo is not supported"},
		{"empty URL userinfo", func(c *Config) { c.BaseURL = "https://@example.com/v1" }, "userinfo is not supported"},
		{"query", func(c *Config) { c.BaseURL = "https://example.com/v1?api-version=not-real" }, "query and fragment are not supported"},
		{"empty query", func(c *Config) { c.BaseURL = "https://example.com/v1?" }, "query and fragment are not supported"},
		{"fragment", func(c *Config) { c.BaseURL = "https://example.com/v1#section" }, "query and fragment are not supported"},
		{"empty fragment", func(c *Config) { c.BaseURL = "https://example.com/v1#" }, "query and fragment are not supported"},
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
}

func TestConfigURLNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL, modelURL, providerURL, want string
	}{
		{"config override", "https://config.invalid/v1", "invalid-model-url", "invalid-provider-url", "https://config.invalid/v1/"},
		{"root URL", "https://example.com", "", "", "https://example.com/"},
		{"existing trailing slash", "https://example.com/v1/", "", "", "https://example.com/v1/"},
		{"override query and fragment", "https://config.invalid/v1", "https://model.invalid/v2?region=test", "https://provider.invalid/v3#section", "https://config.invalid/v1/"},
		{"encoded path", "https://example.com/a%2Fb", "", "", "https://example.com/a%2Fb/"},
		{"encoded delimiters", "https://example.com/a%3Fb%23c", "", "", "https://example.com/a%3Fb%23c/"},
		{"override URL userinfo", "https://config.invalid/v1", "https://user:password@model.invalid/v2", "https://user:password@provider.invalid/v3", "https://config.invalid/v1/"},
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
