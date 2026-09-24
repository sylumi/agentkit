// Package launcher defines shared configuration for local Agent launchers.
package launcher

import (
	"fmt"
	"strings"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/session/inmemory"
)

// Config supplies an Agent and optional services to console and web launchers.
// Callers retain ownership of supplied services; launchers do not close them.
type Config struct {
	Agent          agent.Agent
	SessionService session.Service // Defaults to a new in-memory service.
	AppName        string          // Defaults to Agent.Name().
	UserID         string          // Defaults to "user".
}

// Resolve validates Config and returns a copy with omitted defaults filled in.
// Supplied services are reused. Resolve once and share the result to reuse an
// automatically created session service across multiple launchers.
func (c Config) Resolve() (Config, error) {
	if c.Agent == nil || strings.TrimSpace(c.Agent.Name()) == "" {
		return Config{}, fmt.Errorf("launcher: a named Agent is required")
	}
	if c.AppName == "" {
		c.AppName = c.Agent.Name()
	}
	if c.UserID == "" {
		c.UserID = "user"
	}
	if strings.TrimSpace(c.AppName) == "" || strings.TrimSpace(c.UserID) == "" {
		return Config{}, fmt.Errorf("launcher: app name and user ID must not be blank")
	}
	if c.SessionService == nil {
		c.SessionService = inmemory.New()
	}
	return c, nil
}
