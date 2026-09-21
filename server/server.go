// Package server exposes one Agent and its sessions through HTTP for local development.
package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/session"
)

// Config supplies the Agent and storage. Shared dependencies must support
// concurrent calls from different sessions. Run serialization is per Server.
type Config struct {
	AppName        string
	Agent          agent.Agent
	SessionService session.Service
	UserID         string // Defaults to "user"; this is not authentication.
}

// Server implements http.Handler. It does not listen or serve static files.
// A host application must provide its own authentication before exposing it
// beyond local development. All clients use the configured AppName and UserID.
type Server struct {
	handler http.Handler
	agent   agent.Agent
	store   session.Service
	runner  *runner.Runner
	appName string
	userID  string
	mu      sync.Mutex
	active  map[string]bool
}

func New(cfg Config) (*Server, error) {
	if cfg.UserID == "" {
		cfg.UserID = "user"
	}
	if strings.TrimSpace(cfg.UserID) == "" {
		return nil, fmt.Errorf("server: user ID must not be blank")
	}
	r, err := runner.New(runner.Config{
		AppName:        cfg.AppName,
		Agent:          cfg.Agent,
		SessionService: cfg.SessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	s := &Server{
		agent:   cfg.Agent,
		store:   cfg.SessionService,
		runner:  r,
		appName: cfg.AppName,
		userID:  cfg.UserID,
		active:  make(map[string]bool),
	}
	s.handler = http.NewCrossOriginProtection().Handler(s.routes())
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
