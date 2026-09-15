package model

// ProviderInfo describes a service's connection defaults, without credentials.
// APIKeyEnv lists alternative environment variables for the same API key, in
// preference order. It is not a list of all variables required by the service.
type ProviderInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	BaseURL   string   `json:"base_url,omitempty"`
	APIKeyEnv []string `json:"api_key_env,omitempty"`
}
