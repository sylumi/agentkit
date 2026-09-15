package openaimodel

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
)

// Config selects a model and optionally overrides its connection defaults.
// An omitted Provider selects OpenAI. OpenAI fills missing connection defaults;
// other providers are used as supplied. BaseURL selects an API root, not a protocol.
type Config struct {
	Model model.ModelInfo

	// Empty strings leave these overrides unspecified. APIKey otherwise takes
	// precedence over Model.Provider.APIKeyEnv, which is read at construction.
	// For OpenAI, a nil APIKeyEnv uses OPENAI_API_KEY; an empty slice disables lookup.
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client

	// Options are applied after SDK environment defaults, model/provider defaults,
	// and the fields above. They may override values or supply additional settings,
	// such as organization and project. WithMaxRetries(0) is applied last.
	Options []option.RequestOption
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Model.ID == "" || strings.TrimSpace(cfg.Model.ID) != cfg.Model.ID {
		return cfg, fmt.Errorf("openai: config.model: required without surrounding whitespace")
	}
	p := cfg.Model.Provider
	if p.ID == "" && p.Name == "" && p.BaseURL == "" && p.APIKeyEnv == nil {
		p.ID = "openai"
	}
	if p.ID == "openai" {
		if p.Name == "" {
			p.Name = "OpenAI"
		}
		if p.BaseURL == "" {
			p.BaseURL = "https://api.openai.com/v1/"
		}
		if p.APIKeyEnv == nil {
			p.APIKeyEnv = []string{"OPENAI_API_KEY"}
		}
	}
	if p.ID == "" || strings.TrimSpace(p.ID) != p.ID {
		return cfg, fmt.Errorf("openai: config.model.provider.id: required without surrounding whitespace")
	}
	for _, name := range p.APIKeyEnv {
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "=\x00") {
			return cfg, fmt.Errorf("openai: config.model.provider.api_key_env: invalid variable name")
		}
	}
	cfg.Model.Provider = p
	if cfg.APIKey == "" {
		for _, name := range p.APIKeyEnv {
			if value := os.Getenv(name); value != "" {
				cfg.APIKey = value
				break
			}
		}
	}
	for _, c := range cfg.APIKey {
		if c < 33 || c > 126 {
			return cfg, fmt.Errorf("openai: config.api_key: expected visible ASCII without spaces")
		}
	}
	for _, field := range []struct{ name, value string }{
		{"model.provider.base_url", p.BaseURL}, {"model.base_url", cfg.Model.BaseURL}, {"base_url", cfg.BaseURL},
	} {
		if field.value == "" {
			continue
		}
		u, err := url.Parse(field.value)
		if err != nil || strings.TrimSpace(field.value) != field.value {
			return cfg, fmt.Errorf("openai: config.%s: invalid URL", field.name)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" ||
			u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(field.value, "#") {
			return cfg, fmt.Errorf("openai: config.%s: expected an HTTP API root without credentials, query, or fragment", field.name)
		}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = cfg.Model.BaseURL
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = p.BaseURL
	}
	if cfg.BaseURL != "" && !strings.HasSuffix(cfg.BaseURL, "/") {
		cfg.BaseURL += "/"
	}
	return cfg, nil
}
