package llmagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

type modelFunc func(context.Context, model.Request, bool) iter.Seq2[model.Event, error]

func (f modelFunc) Generate(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
	return f(ctx, req, stream)
}

func newInvocation(t *testing.T) (session.Service, *agent.InvocationContext) {
	t.Helper()
	service := session.InMemoryService()
	created, err := service.Create(t.Context(), &session.CreateRequest{AppName: "test", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	return service, &agent.InvocationContext{InvocationID: "run-1", Session: created.Session}
}

func TestNew(t *testing.T) {
	llm := modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
		t.Fatal("constructor called the model")
		return nil
	})
	for _, cfg := range []llmagent.Config{
		{Model: llm}, {Name: " \t", Model: llm}, {Name: "chat"},
	} {
		if _, err := llmagent.New(cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	for _, description := range []string{"", "Answers questions."} {
		a, err := llmagent.New(llmagent.Config{Name: "chat", Description: description, Model: llm})
		if err != nil {
			t.Fatal(err)
		}
		if a.Name() != "chat" || a.Description() != description {
			t.Fatalf("agent identity: %q, %q", a.Name(), a.Description())
		}
	}
}

func TestRunHistoryAndPersistence(t *testing.T) {
	service, invocation := newInvocation(t)
	history := []model.Message{
		{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("What is the weather?")}},
		{Role: model.RoleAssistant, Parts: []model.Part{
			{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingSummary, Text: "Check the weather."}},
			{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{ID: "call-1", Name: "weather", Arguments: json.RawMessage(`{"city":"Beijing"}`)}},
		}},
		{Role: model.RoleTool, Parts: []model.Part{
			{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call-1", Content: "25 C"}},
		}},
	}
	for i := range history {
		event := session.NewEvent("previous-run")
		event.Message = &history[i]
		if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
			t.Fatal(err)
		}
	}
	metadataOnly := session.NewEvent("previous-run")
	metadataOnly.StopReason = model.StopReasonBlocked
	if err := service.AppendEvent(t.Context(), invocation.Session, metadataOnly); err != nil {
		t.Fatal(err)
	}

	inputTokens, outputTokens, cachedTokens := int64(14), int64(3), int64(0)
	result := model.Result{
		Message:    &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("It is 25 C.")}},
		StopReason: model.StopReasonStop,
		Usage:      model.Usage{InputTokens: &inputTokens, OutputTokens: &outputTokens, CachedInputTokens: &cachedTokens},
		Metadata:   &model.ResponseMetadata{Provider: "test", Model: "test-model", ResponseID: "response-1"},
	}
	var requests []model.Request
	llm := modelFunc(func(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
		if ctx != t.Context() || stream {
			t.Fatal("model received a different context or streaming mode")
		}
		requests = append(requests, req)
		return func(yield func(model.Event, error) bool) {
			yield(model.ResultEvent{Result: result}, nil)
		}
	})
	a, err := llmagent.New(llmagent.Config{
		Name: "chat", Description: "Not a prompt.", Model: llm, Instruction: "Keep {placeholders} literal.",
	})
	if err != nil {
		t.Fatal(err)
	}
	outputs := a.Run(t.Context(), invocation)
	if len(requests) != 0 {
		t.Fatal("Run called the model before consumption")
	}
	question := session.NewEvent(invocation.InvocationID)
	question.Author = "user"
	question.Message = &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("Summarize the result.")}}
	if err := service.AppendEvent(t.Context(), invocation.Session, question); err != nil {
		t.Fatal(err)
	}
	wantHistory := append(slices.Clone(history), *question.Message)
	before, err := json.Marshal(slices.Collect(invocation.Session.Events().All()))
	if err != nil {
		t.Fatal(err)
	}
	var events []*session.Event
	for event, err := range outputs {
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(requests) != 1 || len(events) != 1 {
		t.Fatalf("got %d calls and %d events", len(requests), len(events))
	}
	wantRequest := model.Request{Instructions: "Keep {placeholders} literal.", Messages: wantHistory}
	if !reflect.DeepEqual(requests[0], wantRequest) {
		t.Fatalf("request: got %+v, want %+v", requests[0], wantRequest)
	}
	event := events[0]
	if event.ID == "" || event.Timestamp.IsZero() || event.InvocationID != invocation.InvocationID || event.Author != "chat" {
		t.Fatalf("event identity: %+v", event)
	}
	if !reflect.DeepEqual(event.Message, result.Message) || event.StopReason != result.StopReason ||
		!reflect.DeepEqual(event.Usage, &result.Usage) || !reflect.DeepEqual(event.Metadata, result.Metadata) {
		t.Fatalf("result changed: %+v", event)
	}
	after, err := json.Marshal(slices.Collect(invocation.Session.Events().All()))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Run changed session history: %s, %v", after, err)
	}

	if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
		t.Fatal(err)
	}
	next := &agent.InvocationContext{InvocationID: "run-2", Session: invocation.Session}
	for reply, err := range a.Run(t.Context(), next) {
		if err != nil {
			t.Fatal(err)
		}
		if reply.InvocationID != next.InvocationID || reply.ID == event.ID {
			t.Fatalf("next invocation reused event identity: %+v", reply)
		}
	}
	wantHistory = append(wantHistory, *event.Message)
	if len(requests) != 2 || !reflect.DeepEqual(requests[1].Messages, wantHistory) {
		t.Fatalf("next invocation did not read saved output: %+v", requests)
	}
	if !reflect.DeepEqual(requests[0], wantRequest) {
		t.Fatal("next invocation changed an earlier request")
	}
}

func TestRunPreservesStopReasons(t *testing.T) {
	toolMessage := &model.Message{Role: model.RoleAssistant, Parts: []model.Part{{
		Kind:     model.PartToolCall,
		ToolCall: &model.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)},
	}}}
	for _, result := range []model.Result{
		{StopReason: model.StopReasonStop},
		{StopReason: model.StopReasonBlocked},
		{StopReason: model.StopReasonLength, Message: toolMessage},
		{StopReason: model.StopReasonToolCalls, Message: toolMessage},
	} {
		t.Run(string(result.StopReason), func(t *testing.T) {
			_, invocation := newInvocation(t)
			calls := 0
			a, err := llmagent.New(llmagent.Config{Name: "chat", Instruction: "Answer briefly.", Model: modelFunc(
				func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					calls++
					return func(yield func(model.Event, error) bool) { yield(model.ResultEvent{Result: result}, nil) }
				},
			)})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(t.Context(), invocation) {
				if err != nil {
					t.Fatal(err)
				}
				count++
				if event.StopReason != result.StopReason || !reflect.DeepEqual(event.Message, result.Message) || event.Usage == nil {
					t.Fatalf("generation outcome changed: %+v", event)
				}
			}
			if count != 1 || calls != 1 {
				t.Fatalf("got %d events from %d model calls", count, calls)
			}
		})
	}
}

func TestRunModelErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "provider failure"
		if canceled {
			name = "canceled during generation"
		}
		t.Run(name, func(t *testing.T) {
			_, invocation := newInvocation(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("provider unavailable")
			partialText := "partial answer"
			callErr := &model.CallError{
				Cause:   cause,
				Partial: model.PartialOutput{Parts: []model.PartialPart{{Kind: model.PartText, Text: &partialText}}},
			}
			closed := false
			a, err := llmagent.New(llmagent.Config{Name: "chat", Model: modelFunc(
				func(modelCtx context.Context, _ model.Request, _ bool) iter.Seq2[model.Event, error] {
					return func(yield func(model.Event, error) bool) {
						defer func() { closed = true }()
						if canceled {
							cancel()
							cause = modelCtx.Err()
							callErr.Cause = cause
						}
						yield(nil, callErr)
					}
				},
			)})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(ctx, invocation) {
				count++
				var got *model.CallError
				if event != nil || !errors.Is(err, cause) || !errors.As(err, &got) || got != callErr {
					t.Fatalf("model error changed: event=%+v, error=%v", event, err)
				}
			}
			if count != 1 || !closed || invocation.Session.Events().Len() != 0 {
				t.Fatalf("failure cleanup: events=%d, closed=%t, history=%d", count, closed, invocation.Session.Events().Len())
			}
		})
	}
}

func TestRunCanceledBeforeConsumption(t *testing.T) {
	_, invocation := newInvocation(t)
	a, err := llmagent.New(llmagent.Config{Name: "chat", Model: modelFunc(
		func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
			t.Fatal("canceled invocation called the model")
			return nil
		},
	)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	outputs := a.Run(ctx, invocation)
	cancel()
	count := 0
	for event, err := range outputs {
		count++
		if event != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled output: %+v, %v", event, err)
		}
	}
	if count != 1 {
		t.Fatalf("got %d cancellation errors", count)
	}
}

func TestRunEarlyExit(t *testing.T) {
	_, invocation := newInvocation(t)
	closed, stopped := false, false
	a, err := llmagent.New(llmagent.Config{Name: "chat", Model: modelFunc(
		func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
			return func(yield func(model.Event, error) bool) {
				defer func() { closed = true }()
				stopped = !yield(model.ResultEvent{Result: model.Result{StopReason: model.StopReasonStop}}, nil)
			}
		},
	)})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range a.Run(t.Context(), invocation) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if !closed || !stopped {
		t.Fatalf("model iterator was not stopped and released: stopped=%t, closed=%t", stopped, closed)
	}
}
