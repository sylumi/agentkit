package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func listenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	if ctx == nil {
		return fmt.Errorf("web: context is required")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("web: listen: %w", err)
	}
	log.Printf("Agentkit HTTP: http://%s", listener.Addr())
	err = serveWeb(ctx, listener, handler)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func serveWeb(ctx context.Context, listener net.Listener, handler http.Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, IdleTimeout: time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer srv.Close()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := srv.Shutdown(shutdown); err != nil {
			return fmt.Errorf("launcher: shutdown: %w", err)
		}
		return ctx.Err()
	}
}
