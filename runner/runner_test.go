package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
)

type agentFunc func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error]

func (f agentFunc) Name() string        { return "test-agent" }
func (f agentFunc) Description() string { return "" }
func (f agentFunc) Run(ctx context.Context, invocation *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return f(ctx, invocation)
}

type modelFunc func(context.Context, model.Request, bool) iter.Seq2[model.Event, error]

func (f modelFunc) Generate(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
	return f(ctx, req, stream)
}

type serviceStub struct {
	session.Service
	get    func(context.Context, *session.GetRequest) (*session.GetResponse, error)
	append func(context.Context, session.Session, *session.Event) error
}

func (s serviceStub) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if s.get != nil {
		return s.get(ctx, req)
	}
	return s.Service.Get(ctx, req)
}

func (s serviceStub) AppendEvent(ctx context.Context, current session.Session, event *session.Event) error {
	if s.append != nil {
		return s.append(ctx, current, event)
	}
	return s.Service.AppendEvent(ctx, current, event)
}

func createSession(t *testing.T) session.Service {
	t.Helper()
	s := session.InMemoryService()
	if _, err := s.Create(t.Context(), &session.CreateRequest{AppName: "app", UserID: "user", SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func history(t *testing.T, s session.Service) session.Events {
	t.Helper()
	response, err := s.Get(t.Context(), &session.GetRequest{AppName: "app", UserID: "user", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	return response.Session.Events()
}

func userMessage(text string) *model.Message {
	return &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(text)}}
}

func reply(invocation *agent.InvocationContext) *session.Event {
	e := session.NewEvent(invocation.InvocationID)
	e.Author = "test-agent"
	e.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello.")}}
	e.StopReason = model.StopReasonStop
	return e
}

func TestNew(t *testing.T) {
	a := agentFunc(func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		t.Fatal("constructor ran the agent")
		return nil
	})
	s := createSession(t)
	for _, cfg := range []runner.Config{
		{Agent: a, SessionService: s},
		{AppName: " \t", Agent: a, SessionService: s},
		{AppName: "app", SessionService: s},
		{AppName: "app", Agent: a},
	} {
		if _, err := runner.New(cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	if _, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: s}); err != nil {
		t.Fatal(err)
	}
}

func TestRunToolLoopAndNextTurn(t *testing.T) {
	s := createSession(t)
	getCalls, modelCalls, toolCalls := 0, 0, 0
	service := serviceStub{Service: s, get: func(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
		getCalls++
		if ctx != t.Context() || *req != (session.GetRequest{AppName: "app", UserID: "user", SessionID: "session"}) {
			t.Fatalf("session lookup: %+v", req)
		}
		return s.Get(ctx, req)
	}}
	weather, err := functiontool.New(functiontool.Config{Name: "weather"}, func(ctx context.Context, args struct {
		City string `json:"city"`
	}) (map[string]int, error) {
		toolCalls++
		if ctx != t.Context() || args.City != "Beijing" || history(t, s).Len() != 2 {
			t.Fatal("tool ran before its call was saved or received the wrong input")
		}
		return map[string]int{"temperature": 25}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests []model.Request
	a, err := llmagent.New(llmagent.Config{
		Name: "weather-agent", Tools: []tool.Tool{weather},
		Model: modelFunc(func(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
			modelCalls++
			if ctx != t.Context() || stream || modelCalls > 3 {
				t.Fatal("unexpected model call")
			}
			requests = append(requests, req)
			if err := req.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(req.Messages) != 2*modelCalls-1 {
				t.Fatalf("call %d received %d messages", modelCalls, len(req.Messages))
			}
			if modelCalls == 2 {
				call := req.Messages[1].Parts[0].ToolCall
				result := req.Messages[2].Parts[0].ToolResult
				if call == nil || result == nil || call.ID != result.CallID || result.Content != `{"temperature":25}` || result.IsError {
					t.Fatalf("tool round trip: call=%+v, result=%+v", call, result)
				}
			}
			result := model.Result{
				Message:    &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("It is 25 C.")}},
				StopReason: model.StopReasonStop,
			}
			if modelCalls == 1 {
				result.StopReason = model.StopReasonToolCalls
				result.Message.Parts = []model.Part{{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
					ID: "weather-1", Name: "weather", Arguments: json.RawMessage(`{"city":"Beijing"}`),
				}}}
			}
			return func(yield func(model.Event, error) bool) { yield(model.ResultEvent{Result: result}, nil) }
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: service})
	if err != nil {
		t.Fatal(err)
	}
	var previousID string
	for turn, text := range []string{"What is the weather in Beijing?", "Thanks."} {
		input := userMessage(text)
		outputs := r.Run(t.Context(), "user", "session", input)
		before := history(t, s).Len()
		if getCalls != turn || before != 4*turn {
			t.Fatal("Run accessed the session before consumption")
		}
		count := 0
		for event, err := range outputs {
			if err != nil {
				t.Fatal(err)
			}
			count++
			stored := history(t, s)
			if stored.Len() != before+1+count || stored.At(stored.Len()-1).ID != event.ID || event.Author != a.Name() {
				t.Fatal("event yielded before persistence or user input was yielded")
			}
			userEvent := stored.At(before)
			if userEvent.Author != "user" || !reflect.DeepEqual(userEvent.Message, input) || event.InvocationID != userEvent.InvocationID {
				t.Fatalf("invocation identity: user=%+v, output=%+v", userEvent, event)
			}
			if _, err := uuid.Parse(event.InvocationID); err != nil || event.InvocationID == previousID {
				t.Fatalf("invalid or reused invocation ID %q", event.InvocationID)
			}
		}
		wantCount := 3
		if turn == 1 {
			wantCount = 1
		}
		if count != wantCount || getCalls != turn+1 || !reflect.DeepEqual(input, userMessage(text)) {
			t.Fatalf("outputs=%d, lookups=%d, input=%+v", count, getCalls, input)
		}
		previousID = history(t, s).At(before).InvocationID
	}
	if modelCalls != 3 || toolCalls != 1 || history(t, s).Len() != 6 || len(requests[0].Messages) != 1 || len(requests[1].Messages) != 3 {
		t.Fatalf("models=%d, tools=%d, history=%d", modelCalls, toolCalls, history(t, s).Len())
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	for i, message := range []*model.Message{
		nil,
		{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello.")}},
		{Role: model.RoleUser},
		{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartToolCall}}},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := serviceStub{get: func(context.Context, *session.GetRequest) (*session.GetResponse, error) {
				t.Fatal("invalid input accessed storage")
				return nil, nil
			}}
			a := agentFunc(func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error] {
				t.Fatal("invalid input ran the agent")
				return nil
			})
			r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: s})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range r.Run(t.Context(), "user", "session", message) {
				count++
				if event != nil || err == nil {
					t.Fatalf("invalid input yielded %+v, %v", event, err)
				}
			}
			if count != 1 {
				t.Fatalf("got %d errors", count)
			}
		})
	}
}

func TestRunFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failGet    bool
		failAppend int
		agentError bool
		wantSaved  int
		wantRuns   int
	}{
		{name: "missing session", failGet: true},
		{name: "input save", failAppend: 1},
		{name: "output save", failAppend: 2, wantSaved: 1, wantRuns: 1},
		{name: "agent error", agentError: true, wantSaved: 1, wantRuns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := createSession(t)
			cause := errors.New("storage unavailable")
			if tc.failGet {
				cause = session.ErrNotFound
			}
			appends, runs, advanced := 0, 0, 0
			closed := false
			service := serviceStub{Service: s,
				get: func(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
					if tc.failGet {
						req.SessionID = "missing"
					}
					return s.Get(ctx, req)
				},
				append: func(ctx context.Context, current session.Session, e *session.Event) error {
					appends++
					if appends == tc.failAppend {
						return cause
					}
					return s.AppendEvent(ctx, current, e)
				},
			}
			callErr := &model.CallError{Cause: cause}
			a := agentFunc(func(ctx context.Context, invocation *agent.InvocationContext) iter.Seq2[*session.Event, error] {
				runs++
				if ctx != t.Context() || invocation.Session.Events().Len() != 1 {
					t.Fatal("agent started before input persistence")
				}
				return func(yield func(*session.Event, error) bool) {
					defer func() { closed = true }()
					if tc.agentError {
						if yield(nil, callErr) {
							advanced++
						}
					} else if yield(reply(invocation), nil) {
						advanced++
					}
				}
			})
			r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: service})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range r.Run(t.Context(), "user", "session", userMessage("Hello.")) {
				count++
				if event != nil || !errors.Is(err, cause) || (tc.agentError && err != callErr) {
					t.Fatalf("failure changed: %+v, %v", event, err)
				}
			}
			if count != 1 || runs != tc.wantRuns || advanced != 0 || closed != (tc.wantRuns > 0) || history(t, s).Len() != tc.wantSaved {
				t.Fatalf("errors=%d, runs=%d, advanced=%d, closed=%t, saved=%d", count, runs, advanced, closed, history(t, s).Len())
			}
		})
	}
}

func TestRunCancellationAndEarlyExit(t *testing.T) {
	for _, stage := range []string{"before consumption", "after get", "after input", "agent output", "after output", "after final output", "early exit"} {
		t.Run(stage, func(t *testing.T) {
			s := createSession(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			gets, runs, advanced := 0, 0, 0
			closed := false
			service := serviceStub{Service: s,
				get: func(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
					gets++
					response, err := s.Get(ctx, req)
					if stage == "after get" {
						cancel()
					}
					return response, err
				},
				append: func(ctx context.Context, current session.Session, e *session.Event) error {
					err := s.AppendEvent(ctx, current, e)
					if stage == "after input" {
						cancel()
					}
					return err
				},
			}
			a := agentFunc(func(ctx context.Context, invocation *agent.InvocationContext) iter.Seq2[*session.Event, error] {
				runs++
				return func(yield func(*session.Event, error) bool) {
					defer func() { closed = true }()
					if err := ctx.Err(); err != nil {
						yield(nil, err)
						return
					}
					if stage == "agent output" {
						cancel()
					}
					if !yield(reply(invocation), nil) || stage == "after final output" {
						return
					}
					if err := ctx.Err(); err != nil {
						yield(nil, err)
						return
					}
					advanced++
				}
			})
			r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: service})
			if err != nil {
				t.Fatal(err)
			}
			outputs := r.Run(ctx, "user", "session", userMessage("Hello."))
			if stage == "before consumption" {
				cancel()
			}
			count, errorCount := 0, 0
			for event, err := range outputs {
				if err != nil {
					errorCount++
					if event != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation changed: %+v, %v", event, err)
					}
					continue
				}
				count++
				if stage == "early exit" {
					break
				}
				cancel()
			}
			wantGets, wantRuns, wantSaved, wantOutputs, wantErrors := 1, 0, 0, 0, 1
			switch stage {
			case "before consumption":
				wantGets = 0
			case "after input":
				wantRuns, wantSaved = 1, 1
			case "agent output":
				wantRuns, wantSaved = 1, 1
			case "after output", "after final output", "early exit":
				wantRuns, wantSaved, wantOutputs = 1, 2, 1
				if stage == "after final output" || stage == "early exit" {
					wantErrors = 0
				}
			}
			if gets != wantGets || runs != wantRuns || history(t, s).Len() != wantSaved || count != wantOutputs || errorCount != wantErrors || advanced != 0 || closed != (wantRuns > 0) {
				t.Fatalf("gets=%d, runs=%d, saved=%d, outputs=%d, errors=%d, advanced=%d, closed=%t", gets, runs, history(t, s).Len(), count, errorCount, advanced, closed)
			}
		})
	}
}
