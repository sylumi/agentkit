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
// Model and provider IDs are supplied by the caller; Generate rejects an empty
// model ID before I/O.
// BaseURL is selected from Config, Model, then Model.Provider, and selects an
// API root without userinfo, a query, or a fragment, not a protocol.
type Config struct {
	Model model.ModelInfo

	// Empty strings leave these overrides unspecified. APIKey otherwise takes
	// precedence over Model.Provider.APIKeyEnv, which is read at construction.
	// A nil or empty APIKeyEnv disables environment lookup for every provider.
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client

	// Options may override credentials and HTTPClient or supply additional settings,
	// such as organization and project. The resolved BaseURL and WithMaxRetries(0)
	// are applied last and cannot be overridden by Options.
	Options []option.RequestOption
}

func normalizeConfig(cfg Config) (Config, error) {
	p := cfg.Model.Provider
	if cfg.APIKey == "" {
		for _, name := range p.APIKeyEnv {
			if value := os.Getenv(name); value != "" {
				cfg.APIKey = value
				break
			}
		}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = cfg.Model.BaseURL
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = p.BaseURL
	}
	if cfg.BaseURL == "" {
		return cfg, fmt.Errorf("openai: config.base_url: required")
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return cfg, fmt.Errorf("openai: config.base_url: invalid URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return cfg, fmt.Errorf("openai: config.base_url: expected an HTTP or HTTPS URL")
	}
	if u.User != nil {
		return cfg, fmt.Errorf("openai: config.base_url: userinfo is not supported")
	}
	if u.RawQuery != "" || u.ForceQuery || strings.Contains(cfg.BaseURL, "#") {
		return cfg, fmt.Errorf("openai: config.base_url: query and fragment are not supported")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
		if u.RawPath != "" {
			u.RawPath += "/"
		}
	}
	cfg.BaseURL = u.String()
	return cfg, nil
}
