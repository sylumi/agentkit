package llmagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool"
)

type toolsetFunc struct {
	name  string
	tools func(context.Context) ([]tool.Tool, error)
}

func (s toolsetFunc) Name() string                                   { return s.name }
func (s toolsetFunc) Tools(ctx context.Context) ([]tool.Tool, error) { return s.tools(ctx) }

type processingToolset struct {
	toolsetFunc
	process func(context.Context, *model.Request) error
}

func (s processingToolset) ProcessRequest(ctx context.Context, req *model.Request) error {
	return s.process(ctx, req)
}

func TestToolsetsLifecycle(t *testing.T) {
	for _, empty := range [][]tool.Tool{nil, {}} {
		t.Run(fmt.Sprintf("nil=%t", empty == nil), func(t *testing.T) {
			firstDiscoveries, plainDiscoveries, emptyDiscoveries, processed := 0, 0, 0, 0
			newTool := func(name string) tool.Tool {
				return &stubTool{t: t, definition: model.ToolDefinition{Name: name},
					handler: func(context.Context, json.RawMessage) (string, error) { return name, nil }}
			}
			// Spare capacity detects writes beyond the returned slice.
			backing := []tool.Tool{newTool("plain"), newTool("sentinel")}
			first := processingToolset{
				toolsetFunc: toolsetFunc{"first", func(context.Context) ([]tool.Tool, error) {
					firstDiscoveries++
					return []tool.Tool{newTool(fmt.Sprintf("dynamic_%d", firstDiscoveries))}, nil
				}},
				process: func(_ context.Context, req *model.Request) error {
					if req.Instructions != "base" || len(req.Tools) != 3 || req.Config == nil {
						t.Fatalf("request not assembled before preprocessing: %+v", req)
					}
					req.Instructions += "|first"
					return nil
				},
			}
			plain := toolsetFunc{"plain", func(context.Context) ([]tool.Tool, error) {
				plainDiscoveries++
				return backing[:1], nil
			}}
			emptySet := processingToolset{
				toolsetFunc: toolsetFunc{"empty", func(context.Context) ([]tool.Tool, error) {
					emptyDiscoveries++
					return empty, nil
				}},
				process: func(_ context.Context, req *model.Request) error {
					processed++
					if req.Instructions != "base|first" {
						t.Fatalf("processor order or accumulated instructions: %q", req.Instructions)
					}
					req.Instructions += "|empty"
					return nil
				},
			}
			sets := []tool.Toolset{first, plain, emptySet}
			modelCalls := 0
			a, err := llmagent.New(llmagent.Config{
				MaxModelCalls: 10,
				Name:          "chat", Instruction: "base", Tools: []tool.Tool{newTool("static")},
				Toolsets: sets, GenerateConfig: &model.GenerateConfig{},
				Model: modelFunc(func(_ context.Context, req model.Request, _ bool) iter.Seq2[model.Event, error] {
					modelCalls++
					run := (modelCalls + 1) / 2
					if firstDiscoveries != run || plainDiscoveries != run || emptyDiscoveries != run || processed != modelCalls {
						t.Fatal("incorrect discovery or preprocessing frequency")
					}
					if req.Instructions != "base|first|empty" {
						t.Fatalf("instructions: %q", req.Instructions)
					}
					want := []string{"static", fmt.Sprintf("dynamic_%d", run), "plain"}
					var names []string
					for _, def := range req.Tools {
						names = append(names, def.Name)
					}
					if !reflect.DeepEqual(names, want) {
						t.Fatalf("tools: %v, want %v", names, want)
					}
					if modelCalls%2 == 1 {
						if len(req.Messages) != 0 {
							t.Fatal("history leaked across sessions")
						}
						var calls []model.ToolCallPart
						for _, name := range want {
							calls = append(calls, model.ToolCallPart{ID: name, Name: name, Arguments: json.RawMessage(`{}`)})
						}
						return resultStream(toolCallResult(calls...))
					}
					if len(req.Messages) != 4 {
						t.Fatalf("history: %+v", req.Messages)
					}
					for i, name := range want {
						result := req.Messages[i+1].Parts[0].ToolResult
						if result == nil || result.IsError || result.Content != name || result.CallID != name {
							t.Fatalf("wrong tool executed: %+v", result)
						}
					}
					return resultStream(model.Result{StopReason: model.StopReasonStop})
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			sets[0] = nil // Agent must own its configuration slice.
			for run := range 2 {
				store, invocation := newInvocation(t)
				outputs := a.Run(t.Context(), invocation)
				if firstDiscoveries != run || processed != run*2 {
					t.Fatal("work before iterator consumption")
				}
				count := 0
				for event, err := range outputs {
					if err != nil {
						t.Fatal(err)
					}
					if err := store.AppendEvent(t.Context(), invocation.Session, event); err != nil {
						t.Fatal(err)
					}
					count++
				}
				if count != 5 {
					t.Fatalf("events: %d", count)
				}
			}
			if backing[1].Definition().Name != "sentinel" {
				t.Fatal("toolset slice overwritten")
			}
		})
	}
}

func TestEmptyToolsetDiscoveredOnce(t *testing.T) {
	for _, empty := range [][]tool.Tool{nil, {}} {
		t.Run(fmt.Sprintf("nil=%t", empty == nil), func(t *testing.T) {
			store, invocation := newInvocation(t)
			discoveries, modelCalls := 0, 0
			a, err := llmagent.New(llmagent.Config{
				MaxModelCalls: 10,
				Name:          "empty", Toolsets: []tool.Toolset{toolsetFunc{"empty", func(context.Context) ([]tool.Tool, error) {
					discoveries++
					return empty, nil
				}}},
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					modelCalls++
					if modelCalls == 1 {
						// Unknown tools return errors to the model, which can continue.
						return resultStream(toolCallResult(model.ToolCallPart{ID: "missing", Name: "missing", Arguments: json.RawMessage(`{}`)}))
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
				if err := store.AppendEvent(t.Context(), invocation.Session, event); err != nil {
					t.Fatal(err)
				}
			}
			if discoveries != 1 || modelCalls != 2 {
				t.Fatalf("discoveries=%d, model calls=%d", discoveries, modelCalls)
			}
		})
	}
}

func TestToolsetRegistrationFailures(t *testing.T) {
	lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"}}
	set := func(name string, tools ...tool.Tool) tool.Toolset {
		return toolsetFunc{name, func(context.Context) ([]tool.Tool, error) { return tools, nil }}
	}
	for _, tc := range []struct {
		name  string
		tools []tool.Tool
		sets  []tool.Toolset
		want  string
	}{
		{name: "nil set", sets: []tool.Toolset{nil}, want: "toolsets[0] must not be nil"},
		{name: "nil tool", sets: []tool.Toolset{set("broken", nil)}, want: `toolset "broken": tools[0] must not be nil`},
		{name: "static collision", tools: []tool.Tool{lookup}, sets: []tool.Toolset{set("one", lookup)}, want: `duplicate tool name "lookup"`},
		{name: "set collision", sets: []tool.Toolset{set("one", lookup), set("two", lookup)}, want: `duplicate tool name "lookup"`},
		{name: "within set", sets: []tool.Toolset{set("one", lookup, lookup)}, want: `duplicate tool name "lookup"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, invocation := newInvocation(t)
			a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "chat", Tools: tc.tools, Toolsets: tc.sets,
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					t.Fatal("invalid registration reached model")
					return nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(t.Context(), invocation) {
				count++
				if event != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("event=%v error=%v", event, err)
				}
			}
			if count != 1 {
				t.Fatalf("outputs: %d", count)
			}
		})
	}
}

func TestToolsetFailureAndCancellation(t *testing.T) {
	for _, stage := range []string{"before run", "discover", "process"} {
		for _, cancelRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%t", stage, cancelRun), func(t *testing.T) {
				_, invocation := newInvocation(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cause := errors.New("source unavailable")
				if cancelRun || stage == "before run" {
					cause = context.Canceled
				}
				fail := func() error {
					if cancelRun {
						cancel()
						return nil
					}
					return cause
				}
				discovered, processed, laterDiscoveries := 0, 0, 0
				first := processingToolset{
					toolsetFunc: toolsetFunc{"first", func(context.Context) ([]tool.Tool, error) {
						discovered++
						if stage == "discover" {
							return nil, fail()
						}
						return nil, nil
					}},
					process: func(context.Context, *model.Request) error {
						processed++
						return fail()
					},
				}
				later := processingToolset{
					toolsetFunc: toolsetFunc{"later", func(context.Context) ([]tool.Tool, error) { laterDiscoveries++; return nil, nil }},
					process:     func(context.Context, *model.Request) error { t.Fatal("processing continued after failure"); return nil },
				}
				a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "chat", Toolsets: []tool.Toolset{first, later},
					Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
						t.Fatal("model called after failure")
						return nil
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				outputs := a.Run(ctx, invocation)
				if stage == "before run" {
					cancel()
				}
				count := 0
				for event, err := range outputs {
					count++
					if event != nil || !errors.Is(err, cause) {
						t.Fatalf("event=%v error=%v", event, err)
					}
					if !cancelRun && stage != "before run" && !strings.Contains(err.Error(), `toolset "first"`) {
						t.Fatalf("missing source: %v", err)
					}
				}
				wantDiscovered, wantProcessed, wantLater := 1, 0, 0
				if stage == "before run" {
					wantDiscovered = 0
				}
				if stage == "process" {
					wantProcessed, wantLater = 1, 1
				}
				if count != 1 || discovered != wantDiscovered || processed != wantProcessed || laterDiscoveries != wantLater {
					t.Fatalf("outputs=%d discoveries=%d processing=%d later=%d", count, discovered, processed, laterDiscoveries)
				}
			})
		}
	}
}

func TestToolsetsConcurrentRuns(t *testing.T) {
	type runKey struct{}
	set := processingToolset{
		toolsetFunc: toolsetFunc{"scoped", func(ctx context.Context) ([]tool.Tool, error) {
			id := ctx.Value(runKey{}).(string)
			return []tool.Tool{&stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"},
				handler: func(context.Context, json.RawMessage) (string, error) { return id, nil },
			}}, nil
		}},
		process: func(ctx context.Context, req *model.Request) error {
			if req.Instructions != "base" {
				t.Errorf("shared request instructions: %q", req.Instructions)
			}
			req.Instructions += "|" + ctx.Value(runKey{}).(string)
			return nil
		},
	}
	a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "chat", Instruction: "base", Toolsets: []tool.Toolset{set},
		Model: modelFunc(func(ctx context.Context, req model.Request, _ bool) iter.Seq2[model.Event, error] {
			id := ctx.Value(runKey{}).(string)
			if req.Instructions != "base|"+id {
				t.Errorf("request leaked: %q", req.Instructions)
			}
			if len(req.Messages) == 0 {
				return resultStream(toolCallResult(model.ToolCallPart{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{}`)}))
			}
			if len(req.Messages) != 2 || req.Messages[1].Parts[0].ToolResult.Content != id {
				t.Errorf("tool mapping leaked across runs: %+v", req.Messages)
			}
			return resultStream(model.Result{StopReason: model.StopReasonStop})
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			store, invocation := newInvocation(t)
			ctx := context.WithValue(t.Context(), runKey{}, fmt.Sprint(i))
			for event, err := range a.Run(ctx, invocation) {
				if err != nil {
					t.Fatal(err)
				}
				if err := store.AppendEvent(ctx, invocation.Session, event); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestToolsetProcessingStopsWithRun(t *testing.T) {
	for _, mode := range []string{"before tool", "after tool", "processor failure", "processor cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store, invocation := newInvocation(t)
			processed, modelCalls, executed := 0, 0, 0
			cause := errors.New("preprocessing failed")
			if mode == "processor cancellation" {
				cause = context.Canceled
			}
			lookup := &stubTool{t: t, definition: model.ToolDefinition{Name: "lookup"},
				handler: func(context.Context, json.RawMessage) (string, error) { executed++; return "done", nil },
			}
			set := processingToolset{
				toolsetFunc: toolsetFunc{"set", func(context.Context) ([]tool.Tool, error) { return []tool.Tool{lookup}, nil }},
				process: func(_ context.Context, req *model.Request) error {
					processed++
					if processed == 1 {
						return nil
					}
					if len(req.Messages) != 2 || req.Messages[1].Parts[0].ToolResult.Content != "done" {
						t.Fatal("preprocessor did not receive saved tool history")
					}
					if mode == "processor cancellation" {
						cancel()
						return nil
					}
					return cause
				},
			}
			a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "chat", Toolsets: []tool.Toolset{set},
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					modelCalls++
					return resultStream(toolCallResult(model.ToolCallPart{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{}`)}))
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			var runErr error
			for event, err := range a.Run(ctx, invocation) {
				if err != nil {
					runErr = err
					break
				}
				if err := store.AppendEvent(ctx, invocation.Session, event); err != nil {
					t.Fatal(err)
				}
				if mode == "before tool" || (mode == "after tool" && event.Message.Role == model.RoleTool) {
					break
				}
			}
			wantSaved, wantExecuted, wantProcessed := 2, 1, 2
			switch mode {
			case "before tool":
				wantSaved, wantExecuted, wantProcessed = 1, 0, 1
				cause = nil
			case "after tool":
				wantProcessed = 1
				cause = nil
			}
			if !errors.Is(runErr, cause) || modelCalls != 1 || processed != wantProcessed || executed != wantExecuted || invocation.Session.Events().Len() != wantSaved {
				t.Fatalf("err=%v model=%d processors=%d tools=%d saved=%d", runErr, modelCalls, processed, executed, invocation.Session.Events().Len())
			}
		})
	}
}
