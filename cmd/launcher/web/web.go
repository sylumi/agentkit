// Package web starts the Agent HTTP API with an optional UI handler.
// It does not import or embed frontend resources.
package web

import (
	"context"
	"net/http"

	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/server"
)

// Run serves the API and optional UI at addr; an empty address defaults to
// 127.0.0.1:8080. A nil UI serves only the API. Interrupts and context
// cancellation shut down the server and cancel active runs.
func Run(ctx context.Context, cfg launcher.Config, addr string, ui http.Handler) error {
	handler, err := NewHandler(cfg, ui)
	if err != nil {
		return err
	}
	return listenAndServe(ctx, addr, handler)
}

// NewHandler mounts the API at /api and an optional UI at / without listening.
// Omitted config values use launcher defaults; supplied services are reused.
func NewHandler(cfg launcher.Config, ui http.Handler) (http.Handler, error) {
	cfg, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	api, err := server.New(
		server.Config{
			AppName:        cfg.AppName,
			Agent:          cfg.Agent,
			UserID:         cfg.UserID,
			SessionService: cfg.SessionService,
		})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api", api)
	mux.Handle("/api/", api)
	if ui != nil {
		mux.Handle("/", ui)
	}
	return mux, nil
}
