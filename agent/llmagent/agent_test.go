package llmagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
)

type modelFunc func(context.Context, model.Request, bool) iter.Seq2[model.Event, error]

func (f modelFunc) Generate(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
	return f(ctx, req, stream)
}

type stubTool struct {
	t               *testing.T
	definition      model.ToolDefinition
	definitionCalls int
	handler         func(context.Context, json.RawMessage) (string, error)
}

func (s *stubTool) Definition() model.ToolDefinition {
	s.definitionCalls++
	return s.definition
}

func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if s.handler == nil {
		s.t.Fatal("unexpected tool execution")
	}
	return s.handler(ctx, args)
}

func resultStream(result model.Result) iter.Seq2[model.Event, error] {
	return func(yield func(model.Event, error) bool) { yield(model.ResultEvent{Result: result}, nil) }
}

func toolCallResult(calls ...model.ToolCallPart) model.Result {
	message := &model.Message{Role: model.RoleAssistant}
	for i := range calls {
		message.Parts = append(message.Parts, model.Part{Kind: model.PartToolCall, ToolCall: &calls[i]})
	}
	return model.Result{Message: message, StopReason: model.StopReasonToolCalls}
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
		{Name: "chat", Model: llm, MaxModelCalls: -1},
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
	for _, limit := range []int{0, 1, 20} {
		if _, err := llmagent.New(llmagent.Config{Name: "chat", Model: llm, MaxModelCalls: limit}); err != nil {
			t.Fatalf("rejected model call limit %d: %v", limit, err)
		}
	}
}

func TestRunToolsAndGenerateConfig(t *testing.T) {
	maxTokens, reasoning := int64(256), false
	for _, tc := range []struct {
		name   string
		config *model.GenerateConfig
	}{
		{name: "defaults"},
		{name: "named tool", config: &model.GenerateConfig{
			MaxOutputTokens: &maxTokens,
			ToolChoice:      &model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "weather"},
			Reasoning:       &model.ReasoningConfig{Enabled: &reasoning},
		}},
		{name: "required tool", config: &model.GenerateConfig{
			ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceRequired},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, invocation := newInvocation(t)
			weather := &stubTool{t: t, definition: model.ToolDefinition{
				Name: "weather", Description: "Get the weather for a city.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
			}}
			clock := &stubTool{t: t, definition: model.ToolDefinition{
				Name: "clock", Description: "Get the current time.", InputSchema: json.RawMessage(`{"type":"object"}`),
			}}
			configuredTools := []tool.Tool{weather, clock}
			wantRequest := model.Request{
				Instructions: "Use the available tools.",
				Tools:        []model.ToolDefinition{weather.definition, clock.definition},
				Config:       tc.config,
			}
			before, err := json.Marshal(wantRequest)
			if err != nil {
				t.Fatal(err)
			}
			modelCalls := 0
			a, err := llmagent.New(llmagent.Config{
				Name: "chat", Instruction: wantRequest.Instructions,
				Tools: configuredTools, GenerateConfig: tc.config,
				Model: modelFunc(func(_ context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
					modelCalls++
					if stream || !reflect.DeepEqual(req, wantRequest) {
						t.Fatalf("model request: %+v, stream=%t", req, stream)
					}
					if weather.definitionCalls != modelCalls || clock.definitionCalls != modelCalls {
						t.Fatalf("definitions were not read once per Run: weather=%d, clock=%d, calls=%d",
							weather.definitionCalls, clock.definitionCalls, modelCalls)
					}
					return func(yield func(model.Event, error) bool) {
						yield(model.ResultEvent{Result: model.Result{StopReason: model.StopReasonStop}}, nil)
					}
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			configuredTools[0] = clock
			for run := range 2 {
				outputs := a.Run(t.Context(), invocation)
				if weather.definitionCalls != run || clock.definitionCalls != run || modelCalls != run {
					t.Fatal("tools or model accessed before consumption")
				}
				count := 0
				for _, err := range outputs {
					if err != nil {
						t.Fatal(err)
					}
					count++
				}
				if count != 1 || modelCalls != run+1 {
					t.Fatalf("got %d events and %d total model calls", count, modelCalls)
				}
			}
			after, err := json.Marshal(wantRequest)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("Run changed caller data: %s, %v", after, err)
			}
			if configuredTools[0] != clock || configuredTools[1] != clock {
				t.Fatal("Run changed the caller's tool slice")
			}
		})
	}
}

func TestRunRejectsInvalidTools(t *testing.T) {
	for _, name := range []string{"nil tool", "duplicate name"} {
		t.Run(name, func(t *testing.T) {
			_, invocation := newInvocation(t)
			first := &stubTool{t: t, definition: model.ToolDefinition{
				Name: "weather", InputSchema: json.RawMessage(`{"type":"object"}`),
			}}
			configuredTools := []tool.Tool{first, nil}
			wantError := "tools[1] must not be nil"
			if name == "duplicate name" {
				configuredTools[1] = &stubTool{t: t, definition: model.ToolDefinition{
					Name: "weather", Description: "A different weather tool.", InputSchema: json.RawMessage(`{"type":"object"}`),
				}}
				wantError = `duplicate tool name "weather"`
			}
			a, err := llmagent.New(llmagent.Config{
				Name: "chat", Tools: configuredTools,
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					t.Fatal("invalid tools reached the model")
					return nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(t.Context(), invocation) {
				count++
				if event != nil || err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("registration failure: event=%+v, error=%v", event, err)
				}
			}
			if count != 1 || invocation.Session.Events().Len() != 0 {
				t.Fatalf("registration failure yielded %d events and saved %d", count, invocation.Session.Events().Len())
			}
		})
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
		{StopReason: model.StopReasonBlocked, Message: toolMessage},
	} {
		t.Run(string(result.StopReason), func(t *testing.T) {
			_, invocation := newInvocation(t)
			calls := 0
			lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}}
			a, err := llmagent.New(llmagent.Config{Name: "chat", Instruction: "Answer briefly.", Tools: []tool.Tool{lookup}, MaxModelCalls: 1, Model: modelFunc(
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
	configuredTool := &stubTool{t: t, definition: model.ToolDefinition{Name: "weather"}}
	a, err := llmagent.New(llmagent.Config{Name: "chat", Tools: []tool.Tool{configuredTool}, Model: modelFunc(
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
	if configuredTool.definitionCalls != 0 {
		t.Fatal("canceled invocation registered tools")
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

func TestRunToolLoop(t *testing.T) {
	service, invocation := newInvocation(t)
	question := session.NewEvent(invocation.InvocationID)
	question.Author = "user"
	question.Message = &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("What is the weather in Beijing?")}}
	if err := service.AppendEvent(t.Context(), invocation.Session, question); err != nil {
		t.Fatal(err)
	}
	call := toolCallResult(model.ToolCallPart{ID: "weather-1", Name: "weather", Arguments: json.RawMessage(`{ "city": "Beijing" }`)})
	call.Message.Parts = append([]model.Part{model.NewTextPart("Let me check.")}, call.Message.Parts...)
	answer := model.Result{
		Message:    &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("It is 25 C in Beijing.")}},
		StopReason: model.StopReasonStop,
	}
	toolMessage := model.Message{Role: model.RoleTool, Parts: []model.Part{{
		Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "weather-1", Content: `{"temperature":25}`},
	}}}
	toolCalls := 0
	modelOpen := false
	weather, err := functiontool.New(functiontool.Config{Name: "weather"}, func(ctx context.Context, args struct {
		City string `json:"city"`
	}) (map[string]int, error) {
		toolCalls++
		if ctx != t.Context() || args.City != "Beijing" {
			t.Fatalf("tool input: context=%v, city=%q", ctx, args.City)
		}
		if modelOpen || invocation.Session.Events().Len() != 2 {
			t.Fatal("tool ran before model cleanup and call persistence")
		}
		return map[string]int{"temperature": 25}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	maxTokens, reasoning := int64(256), false
	config := &model.GenerateConfig{
		MaxOutputTokens: &maxTokens,
		ToolChoice:      &model.ToolChoice{Mode: model.ToolChoiceAuto},
		Reasoning:       &model.ReasoningConfig{Enabled: &reasoning},
	}
	wantRequests := []model.Request{
		{Instructions: "Answer briefly.", Messages: []model.Message{*question.Message}, Tools: []model.ToolDefinition{weather.Definition()}, Config: config},
		{Instructions: "Answer briefly.", Messages: []model.Message{*question.Message, *call.Message, toolMessage}, Tools: []model.ToolDefinition{weather.Definition()}, Config: config},
	}
	before, err := json.Marshal(wantRequests)
	if err != nil {
		t.Fatal(err)
	}
	var requests []model.Request
	a, err := llmagent.New(llmagent.Config{
		Name: "weather-agent", Instruction: "Answer briefly.", Tools: []tool.Tool{weather}, GenerateConfig: config, MaxModelCalls: 2,
		Model: modelFunc(func(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
			index := len(requests)
			if index >= len(wantRequests) || stream || ctx != t.Context() || !reflect.DeepEqual(req, wantRequests[index]) {
				t.Fatalf("unexpected model request %d: %+v", index, req)
			}
			requests = append(requests, req)
			result := call
			if index == 1 {
				result = answer
			}
			return func(yield func(model.Event, error) bool) {
				modelOpen = true
				defer func() { modelOpen = false }()
				yield(model.ResultEvent{Result: result}, nil)
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMessages := []*model.Message{call.Message, &toolMessage, answer.Message}
	seenIDs := map[string]bool{question.ID: true}
	count := 0
	for event, err := range a.Run(t.Context(), invocation) {
		if err != nil {
			t.Fatal(err)
		}
		if count >= len(wantMessages) || !reflect.DeepEqual(event.Message, wantMessages[count]) {
			t.Fatalf("unexpected event %d: %+v", count, event)
		}
		if modelOpen || event.ID == "" || seenIDs[event.ID] || event.Timestamp.IsZero() || event.InvocationID != invocation.InvocationID || event.Author != a.Name() {
			t.Fatalf("event identity or model cleanup: %+v, modelOpen=%t", event, modelOpen)
		}
		seenIDs[event.ID] = true
		if count == 1 {
			if event.StopReason != "" || event.Usage != nil || event.Metadata != nil {
				t.Fatalf("tool event contains generation fields: %+v", event)
			}
		} else if event.Usage == nil || event.StopReason != []model.StopReason{model.StopReasonToolCalls, "", model.StopReasonStop}[count] {
			t.Fatalf("model event lost generation fields: %+v", event)
		}
		if invocation.Session.Events().Len() != count+1 {
			t.Fatal("agent persisted its own output")
		}
		if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 3 || toolCalls != 1 || len(requests) != 2 || invocation.Session.Events().Len() != 4 {
		t.Fatalf("events=%d, tools=%d, models=%d, history=%d", count, toolCalls, len(requests), invocation.Session.Events().Len())
	}
	after, err := json.Marshal(requests)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("requests or caller data changed: %s, %v", after, err)
	}
}

func TestRunMultipleToolsAndErrors(t *testing.T) {
	service, invocation := newInvocation(t)
	var steps []string
	first := &stubTool{t: t, definition: model.ToolDefinition{Name: "first"}, handler: func(_ context.Context, args json.RawMessage) (string, error) {
		steps = append(steps, "first:"+string(args))
		if string(args) == `{ "fail": true }` {
			return "partial data", errors.New("service unavailable")
		}
		return "ready", nil
	}}
	second := &stubTool{t: t, definition: model.ToolDefinition{Name: "second"}, handler: func(context.Context, json.RawMessage) (string, error) {
		steps = append(steps, "second")
		return "", nil
	}}
	call := toolCallResult(
		model.ToolCallPart{ID: "call-1", Name: "first", Arguments: json.RawMessage(`{}`)},
		model.ToolCallPart{ID: "call-2", Name: "missing", Arguments: json.RawMessage(`{}`)},
		model.ToolCallPart{ID: "call-3", Name: "first", Arguments: json.RawMessage(`{ "fail": true }`)},
		model.ToolCallPart{ID: "call-4", Name: "second", Arguments: json.RawMessage(`{}`)},
	)
	wantResults := []model.ToolResultPart{
		{CallID: "call-1", Content: "ready"},
		{CallID: "call-2", Content: `llmagent: unknown tool "missing"`, IsError: true},
		{CallID: "call-3", Content: "service unavailable", IsError: true},
		{CallID: "call-4", Content: ""},
	}
	modelCalls := 0
	a, err := llmagent.New(llmagent.Config{
		Name: "chat", Tools: []tool.Tool{second, first},
		Model: modelFunc(func(_ context.Context, req model.Request, _ bool) iter.Seq2[model.Event, error] {
			modelCalls++
			steps = append(steps, fmt.Sprintf("model-%d", modelCalls))
			if modelCalls == 1 {
				return resultStream(call)
			}
			if modelCalls != 2 || len(req.Messages) != 5 || !reflect.DeepEqual(req.Messages[0], *call.Message) {
				t.Fatalf("unexpected follow-up request: %+v", req)
			}
			for i, want := range wantResults {
				message := req.Messages[i+1]
				if message.Role != model.RoleTool || len(message.Parts) != 1 || !reflect.DeepEqual(message.Parts[0].ToolResult, &want) {
					t.Fatalf("tool result %d: %+v", i, message)
				}
			}
			return resultStream(model.Result{StopReason: model.StopReasonStop})
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for event, err := range a.Run(t.Context(), invocation) {
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
			t.Fatal(err)
		}
		step := "save-model"
		if event.Message != nil && event.Message.Role == model.RoleTool {
			step = "save-" + event.Message.Parts[0].ToolResult.CallID
		}
		steps = append(steps, step)
	}
	wantSteps := []string{"model-1", "save-model", "first:{}", "save-call-1", "save-call-2", `first:{ "fail": true }`, "save-call-3", "second", "save-call-4", "model-2", "save-model"}
	if !reflect.DeepEqual(steps, wantSteps) || first.definitionCalls != 1 || second.definitionCalls != 1 {
		t.Fatalf("execution order=%v, registrations=%d/%d", steps, first.definitionCalls, second.definitionCalls)
	}
}

func TestRunModelCallLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{name: "default", want: 10},
		{name: "one call", limit: 1, want: 1},
		{name: "two calls", limit: 2, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelCalls, toolCalls := 0, 0
			lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}, handler: func(context.Context, json.RawMessage) (string, error) {
				toolCalls++
				return "try again", nil
			}}
			config := &model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceRequired}}
			a, err := llmagent.New(llmagent.Config{
				Name: "chat", Tools: []tool.Tool{lookup}, GenerateConfig: config, MaxModelCalls: tc.limit,
				Model: modelFunc(func(_ context.Context, req model.Request, _ bool) iter.Seq2[model.Event, error] {
					modelCalls++
					if req.Config != config || req.Config.ToolChoice.Mode != model.ToolChoiceRequired {
						t.Fatal("tool choice changed between calls")
					}
					if modelCalls > 2*tc.want {
						t.Fatal("model call limit was not enforced")
					}
					return resultStream(toolCallResult(model.ToolCallPart{
						ID: fmt.Sprintf("call-%d", modelCalls), Name: "lookup", Arguments: json.RawMessage(`{}`),
					}))
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			for run := range 2 {
				service, invocation := newInvocation(t)
				invocation.InvocationID = fmt.Sprintf("run-%d", run)
				errorCount := 0
				for event, err := range a.Run(t.Context(), invocation) {
					if err != nil {
						errorCount++
						if event != nil || !strings.Contains(err.Error(), fmt.Sprintf("model call limit reached (%d)", tc.want)) {
							t.Fatalf("limit error: %+v, %v", event, err)
						}
						continue
					}
					if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
						t.Fatal(err)
					}
				}
				if errorCount != 1 || modelCalls != (run+1)*tc.want || toolCalls != modelCalls || lookup.definitionCalls != run+1 {
					t.Fatalf("run=%d, errors=%d, models=%d, tools=%d, registrations=%d", run, errorCount, modelCalls, toolCalls, lookup.definitionCalls)
				}
				history := invocation.Session.Events()
				if history.Len() != 2*tc.want {
					t.Fatalf("history has %d events, want %d", history.Len(), 2*tc.want)
				}
				for i := 0; i < history.Len(); i += 2 {
					callID := history.At(i).Message.Parts[0].ToolCall.ID
					if result := history.At(i + 1).Message.Parts[0].ToolResult; result.CallID != callID || result.IsError {
						t.Fatalf("unmatched tool call at the limit: %q, %+v", callID, result)
					}
				}
			}
		})
	}
}

func TestRunStopsBeforeFurtherWork(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		for _, afterEvents := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("cancel=%t/after=%d", cancelRun, afterEvents), func(t *testing.T) {
				service, invocation := newInvocation(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				toolCalls, modelCalls := 0, 0
				lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}, handler: func(context.Context, json.RawMessage) (string, error) {
					toolCalls++
					return "done", nil
				}}
				a, err := llmagent.New(llmagent.Config{
					Name: "chat", Tools: []tool.Tool{lookup},
					Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
						modelCalls++
						if modelCalls != 1 {
							t.Fatal("model called after consumption stopped")
						}
						return resultStream(toolCallResult(
							model.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)},
							model.ToolCallPart{ID: "call-2", Name: "lookup", Arguments: json.RawMessage(`{}`)},
						))
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				count, errorCount := 0, 0
				for event, err := range a.Run(ctx, invocation) {
					if err != nil {
						errorCount++
						if !cancelRun || event != nil || !errors.Is(err, context.Canceled) {
							t.Fatalf("unexpected run error: %+v, %v", event, err)
						}
						continue
					}
					count++
					if err := service.AppendEvent(ctx, invocation.Session, event); err != nil {
						t.Fatal(err)
					}
					if count == afterEvents {
						if !cancelRun {
							break
						}
						cancel()
					}
				}
				wantErrors := 0
				if cancelRun {
					wantErrors = 1
				}
				if count != afterEvents || toolCalls != afterEvents-1 || modelCalls != 1 || errorCount != wantErrors {
					t.Fatalf("events=%d, tools=%d, models=%d, errors=%d", count, toolCalls, modelCalls, errorCount)
				}
			})
		}
	}
}

func TestRunToolCancellation(t *testing.T) {
	for _, name := range []string{"canceled error", "deadline error", "context canceled"} {
		t.Run(name, func(t *testing.T) {
			service, invocation := newInvocation(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := context.Canceled
			if name == "deadline error" {
				cause = context.DeadlineExceeded
			}
			toolErr := fmt.Errorf("lookup: %w", cause)
			toolCalls := 0
			lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}, handler: func(toolCtx context.Context, _ json.RawMessage) (string, error) {
				toolCalls++
				if toolCtx != ctx {
					t.Fatal("tool received a different context")
				}
				if name == "context canceled" {
					cancel()
					return "finished during cancellation", nil
				}
				return "", toolErr
			}}
			modelCalls := 0
			a, err := llmagent.New(llmagent.Config{
				Name: "chat", Tools: []tool.Tool{lookup},
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					modelCalls++
					if modelCalls != 1 {
						t.Fatal("model called after tool cancellation")
					}
					return resultStream(toolCallResult(
						model.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)},
						model.ToolCallPart{ID: "call-2", Name: "lookup", Arguments: json.RawMessage(`{}`)},
					))
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			errorCount := 0
			for event, err := range a.Run(ctx, invocation) {
				if err != nil {
					errorCount++
					if event != nil || !errors.Is(err, cause) || (name != "context canceled" && err != toolErr) {
						t.Fatalf("cancellation changed: %+v, %v", event, err)
					}
					continue
				}
				if err := service.AppendEvent(ctx, invocation.Session, event); err != nil {
					t.Fatal(err)
				}
			}
			if toolCalls != 1 || errorCount != 1 || invocation.Session.Events().Len() != 1 {
				t.Fatalf("tools=%d, errors=%d, history=%d", toolCalls, errorCount, invocation.Session.Events().Len())
			}
		})
	}
}

func TestRunPersistenceFailureStops(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("event-%d", failAt), func(t *testing.T) {
			service, invocation := newInvocation(t)
			toolCalls, modelCalls := 0, 0
			lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}, handler: func(context.Context, json.RawMessage) (string, error) {
				toolCalls++
				return "done", nil
			}}
			a, err := llmagent.New(llmagent.Config{
				Name: "chat", Tools: []tool.Tool{lookup},
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					modelCalls++
					if modelCalls != 1 {
						t.Fatal("model called after persistence failed")
					}
					return resultStream(toolCallResult(
						model.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)},
						model.ToolCallPart{ID: "call-2", Name: "lookup", Arguments: json.RawMessage(`{}`)},
					))
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(t.Context(), invocation) {
				if err != nil {
					t.Fatal(err)
				}
				count++
				if count == failAt {
					if err := service.Delete(t.Context(), &session.DeleteRequest{
						AppName: invocation.Session.AppName(), UserID: invocation.Session.UserID(), SessionID: invocation.Session.ID(),
					}); err != nil {
						t.Fatal(err)
					}
				}
				if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
					if !errors.Is(err, session.ErrNotFound) {
						t.Fatal(err)
					}
					break
				}
			}
			if count != failAt || toolCalls != failAt-1 || invocation.Session.Events().Len() != failAt-1 {
				t.Fatalf("events=%d, tools=%d, history=%d", count, toolCalls, invocation.Session.Events().Len())
			}
		})
	}
}

func TestRunModelErrorAfterTool(t *testing.T) {
	service, invocation := newInvocation(t)
	callErr := &model.CallError{Cause: errors.New("provider unavailable"), Partial: model.PartialOutput{Parts: []model.PartialPart{
		{Kind: model.PartText, Text: new(string)},
	}}}
	modelCalls, toolCalls := 0, 0
	lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}, handler: func(context.Context, json.RawMessage) (string, error) {
		toolCalls++
		return "done", nil
	}}
	closed := false
	a, err := llmagent.New(llmagent.Config{
		Name: "chat", Tools: []tool.Tool{lookup},
		Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
			modelCalls++
			if modelCalls == 1 {
				return resultStream(toolCallResult(model.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{}`)}))
			}
			if modelCalls != 2 {
				t.Fatal("model retried after failure")
			}
			return func(yield func(model.Event, error) bool) {
				defer func() { closed = true }()
				yield(nil, callErr)
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	errorCount := 0
	for event, err := range a.Run(t.Context(), invocation) {
		if err != nil {
			errorCount++
			if event != nil || err != callErr || !closed {
				t.Fatalf("model failure changed or iterator left open: %+v, %v, closed=%t", event, err, closed)
			}
			continue
		}
		if err := service.AppendEvent(t.Context(), invocation.Session, event); err != nil {
			t.Fatal(err)
		}
	}
	if modelCalls != 2 || toolCalls != 1 || errorCount != 1 || invocation.Session.Events().Len() != 2 {
		t.Fatalf("models=%d, tools=%d, errors=%d, history=%d", modelCalls, toolCalls, errorCount, invocation.Session.Events().Len())
	}
}
