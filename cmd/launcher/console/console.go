// Package console runs an Agent as an interactive terminal conversation.
package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/session"
)

// Run chats over stdin/stdout using the configured Agent and session service.
// Ctrl+C, SIGTERM, or context cancellation ends the conversation normally.
func Run(ctx context.Context, cfg launcher.Config) error {
	if ctx == nil {
		return fmt.Errorf("console: context is required")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, cfg, os.Stdin, os.Stdout)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func run(ctx context.Context, cfg launcher.Config, input io.Reader, output io.Writer) error {
	cfg, err := cfg.Resolve()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Runner saves user, assistant, and tool events in this shared session.
	created, err := cfg.SessionService.Create(ctx, &session.CreateRequest{AppName: cfg.AppName, UserID: cfg.UserID})
	if err != nil {
		return err
	}
	r, err := runner.New(runner.Config{AppName: cfg.AppName, Agent: cfg.Agent, SessionService: cfg.SessionService})
	if err != nil {
		return err
	}
	sessionID := created.Session.ID()
	scanner := bufio.NewScanner(input)
	lines := make(chan string)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	fmt.Fprintln(output, "Chat with the assistant. Press Ctrl+C or Ctrl+D to exit.")
	for {
		fmt.Fprint(output, "\nYou: ")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return scanner.Err()
			}
			prompt := strings.TrimSpace(line)
			if prompt == "" {
				continue
			}
			if err := runTurn(ctx, r, cfg, sessionID, prompt, output); err != nil {
				return err
			}
		}
	}
}

func runTurn(ctx context.Context, r *runner.Runner, cfg launcher.Config, sessionID, prompt string, output io.Writer) error {
	input := &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(prompt)}}
	for event, err := range r.Run(ctx, cfg.UserID, sessionID, input) {
		if err != nil {
			return err
		}
		if event.Message != nil {
			for _, part := range event.Message.Parts {
				switch part.Kind {
				case model.PartThinking:
					fmt.Fprintf(output, "Thinking (%s): %s\n", part.Thinking.Kind, part.Thinking.Text)
				case model.PartText:
					fmt.Fprintf(output, "Assistant: %s\n", *part.Text)
				case model.PartToolCall:
					fmt.Fprintf(output, "Tool call: %s(%s)\n", part.ToolCall.Name, part.ToolCall.Arguments)
				case model.PartToolResult:
					fmt.Fprintf(output, "Tool result (error=%t): %s\n", part.ToolResult.IsError, part.ToolResult.Content)
				}
			}
		}
		if event.StopReason != "" && event.StopReason != model.StopReasonStop && event.StopReason != model.StopReasonToolCalls {
			return fmt.Errorf("generation ended with %s", event.StopReason)
		}
	}
	return nil
}
