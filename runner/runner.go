// Package runner connects agents to session storage.
package runner

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/google/uuid"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

type Config struct {
	AppName        string
	Agent          agent.Agent
	SessionService session.Service
}

// Runner executes an agent and persists its events.
type Runner struct {
	appName        string
	agent          agent.Agent
	sessionService session.Service
}

type runOptions struct {
	streaming bool
}

// RunOption configures one invocation without changing the shared Runner.
type RunOption func(*runOptions)

// WithStreaming requests partial events alongside complete events.
// Only complete events are saved. Streaming is disabled by default.
func WithStreaming(enabled bool) RunOption {
	return func(options *runOptions) { options.streaming = enabled }
}

func New(cfg Config) (*Runner, error) {
	if strings.TrimSpace(cfg.AppName) == "" {
		return nil, fmt.Errorf("runner: app name is required")
	}
	if cfg.Agent == nil {
		return nil, fmt.Errorf("runner: agent is required")
	}
	if cfg.SessionService == nil {
		return nil, fmt.Errorf("runner: session service is required")
	}
	return &Runner{appName: cfg.AppName, agent: cfg.Agent, sessionService: cfg.SessionService}, nil
}

// Run lazily saves user input and yields agent events from an existing session.
// Complete events are saved before yielding; partial events are only forwarded.
// Keep the message and returned events read-only. Serialize runs on the same session.
func (r *Runner) Run(ctx context.Context, userID, sessionID string, message *model.Message, options ...RunOption) iter.Seq2[*session.Event, error] {
	var cfg runOptions
	for _, option := range options {
		option(&cfg)
	}
	return func(yield func(*session.Event, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if message == nil || message.Role != model.RoleUser {
			yield(nil, fmt.Errorf("runner: a user message is required"))
			return
		}
		if err := message.Validate(); err != nil {
			yield(nil, fmt.Errorf("runner: message: %w", err))
			return
		}
		response, err := r.sessionService.Get(ctx, &session.GetRequest{
			AppName: r.appName, UserID: userID, SessionID: sessionID,
		})
		if err != nil {
			yield(nil, fmt.Errorf("runner: get session: %w", err))
			return
		}
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		invocation := &agent.InvocationContext{InvocationID: uuid.NewString(), Session: response.Session, Streaming: cfg.streaming}
		input := session.NewEvent(invocation.InvocationID)
		input.Author = "user"
		input.Message = message
		if err := r.sessionService.AppendEvent(ctx, invocation.Session, input); err != nil {
			yield(nil, fmt.Errorf("runner: save user input: %w", err))
			return
		}
		for event, err := range r.agent.Run(ctx, invocation) {
			if err != nil {
				yield(nil, err)
				return
			}
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if event == nil {
				yield(nil, fmt.Errorf("runner: agent returned a nil event"))
				return
			}
			if !event.Partial {
				if err := r.sessionService.AppendEvent(ctx, invocation.Session, event); err != nil {
					yield(nil, fmt.Errorf("runner: save agent event: %w", err))
					return
				}
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}
