package console

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/session/inmemory"
)

type testAgent struct {
	run func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error]
}

func (testAgent) Name() string        { return "chat" }
func (testAgent) Description() string { return "Test chat" }
func (a testAgent) Run(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return a.run(ctx, inv)
}

func TestConsoleKeepsHistoryAndCancelsWhileWaitingForInput(t *testing.T) {
	var turns []int
	a := testAgent{run: func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			turns = append(turns, inv.Session.Events().Len())
			e := session.NewEvent(inv.InvocationID)
			e.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello")}}
			yield(e, nil)
		}
	}}
	store := inmemory.New()
	cfg := launcher.Config{Agent: a, SessionService: store, AppName: "custom-app", UserID: "alice"}
	var output bytes.Buffer
	if err := run(t.Context(), cfg, strings.NewReader("hi\n\nagain\n"), &output); err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0] != 1 || turns[1] != 3 || strings.Count(output.String(), "Assistant: Hello") != 2 {
		t.Fatal("conversation history was lost", turns, output.String())
	}
	listed, err := store.List(t.Context(), &session.ListRequest{AppName: cfg.AppName, UserID: cfg.UserID})
	if err != nil || len(listed.Sessions) != 1 {
		t.Fatalf("configured service was not used: %v", err)
	}
	loaded, err := store.Get(t.Context(), &session.GetRequest{AppName: cfg.AppName, UserID: cfg.UserID, SessionID: listed.Sessions[0].ID()})
	if err != nil || loaded.Session.Events().Len() != 4 {
		t.Fatalf("configured service did not retain the conversation: %v", err)
	}
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- run(ctx, launcher.Config{Agent: a}, input, io.Discard) }()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for terminal input")
	}
}

func TestConsoleCancelsActiveRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := testAgent{run: func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			cancel()
			<-ctx.Done()
			yield(nil, ctx.Err())
		}
	}}
	err := run(ctx, launcher.Config{Agent: a}, strings.NewReader("wait\n"), io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("turn did not use its caller's cancellation: %v", err)
	}
}
