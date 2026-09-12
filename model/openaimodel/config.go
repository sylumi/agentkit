package openaimodel

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3/option"
)

// Config configures an OpenAI Responses connection. Model, APIKey, and BaseURL
// are required. Response metadata identifies the provider as "openai".
type Config struct {
	Model      string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client

	// Options are applied in order after the SDK environment defaults and the
	// fields above. They may override those values or supply additional settings,
	// such as organization and project. WithMaxRetries(0) is applied last.
	Options []option.RequestOption
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Model == "" || strings.TrimSpace(cfg.Model) != cfg.Model {
		return cfg, fmt.Errorf("openai: config.model: required without surrounding whitespace")
	}
	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("openai: config.api_key: required")
	}
	for _, c := range cfg.APIKey {
		if c < 33 || c > 126 {
			return cfg, fmt.Errorf("openai: config.api_key: expected visible ASCII without spaces")
		}
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || strings.TrimSpace(cfg.BaseURL) != cfg.BaseURL {
		return cfg, fmt.Errorf("openai: config.base_url: invalid URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(cfg.BaseURL, "#") {
		return cfg, fmt.Errorf("openai: config.base_url: expected an HTTP API root without credentials, query, or fragment")
	}
	if !strings.HasSuffix(cfg.BaseURL, "/") {
		cfg.BaseURL += "/"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return cfg, nil
}
