package openaimodel

import (
	"net/http"
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	cfg := Config{Model: "test-model", APIKey: "test-key", BaseURL: "https://example.com", HTTPClient: &http.Client{}}
	normalized, err := normalizeConfig(cfg)
	if err != nil || normalized.BaseURL != "https://example.com/" || normalized.HTTPClient != cfg.HTTPClient || cfg.BaseURL != "https://example.com" {
		t.Fatalf("normalize: %+v, %v", normalized, err)
	}
	cfg.Model = ""
	if _, err := NewModel(cfg); err == nil || !strings.Contains(err.Error(), "openai: config.model:") {
		t.Fatalf("model error = %v", err)
	}
	cfg.Model = "test-model"
	if _, err := NewModel(cfg); err != nil {
		t.Fatal(err)
	}
}
