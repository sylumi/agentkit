package openaimodel

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
)

// Config selects a model and supplies its connection settings.
// Model and provider IDs are supplied by the caller; Generate rejects an empty
// model ID before I/O. Connection metadata in ModelInfo is not used.
type Config struct {
	Model model.ModelInfo

	// APIKey overrides SDK defaults and option.WithAPIKey, including when empty.
	// A nonempty key replaces an Authorization header supplied through Options;
	// an empty key leaves that header intact. Read credentials from the environment
	// in the caller; Provider.APIKeyEnv is not consulted.
	APIKey string
	// BaseURL is required and selects an API root, not a protocol.
	// It must be an HTTP or HTTPS URL without userinfo, a query, or a fragment.
	BaseURL string
	// HTTPClient overrides option.WithHTTPClient when non-nil.
	HTTPClient *http.Client

	// Options supply additional SDK settings, such as headers, organization, and
	// project. APIKey, a non-nil HTTPClient, BaseURL, and WithMaxRetries(0) are
	// applied afterward and take precedence over the corresponding SDK options.
	Options []option.RequestOption
}

func normalizeConfig(cfg Config) (Config, error) {
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
