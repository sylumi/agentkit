package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"testing"

	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
)

func TestRunContinuesAfterToolInterruption(t *testing.T) {
	writeFailure := errors.New("result storage unavailable")
	for _, tc := range []struct {
		name        string
		stopAfter   model.Role
		cancelAt    string
		writeError  string
		wantCalls   int
		wantEffects int
		wantSaved   int
		wantErr     error
	}{
		{name: "stop after calls before execution", stopAfter: model.RoleAssistant},
		{name: "cancel during first tool", cancelAt: "before effect", wantCalls: 1, wantErr: context.Canceled},
		{name: "cancel after effect before result", cancelAt: "after effect", wantCalls: 1, wantEffects: 1, wantErr: context.Canceled},
		{name: "stop after first saved result", stopAfter: model.RoleTool, wantCalls: 1, wantEffects: 1, wantSaved: 1},
		{name: "result write fails before commit", writeError: "before commit", wantCalls: 1, wantEffects: 1, wantErr: writeFailure},
		{name: "result write fails after commit", writeError: "after commit", wantCalls: 1, wantEffects: 1, wantSaved: 1, wantErr: writeFailure},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/streaming=%t", tc.name, streaming), func(t *testing.T) {
				store := createSession(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				callA := model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
					ID: "call-a", Name: "work", Arguments: json.RawMessage(`{"task":"a"}`),
				}}
				callB := model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
					ID: "call-b", Name: "work", Arguments: json.RawMessage(`{"task":"b"}`),
				}}
				thinking := model.Part{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingSummary, Text: "Two operations are needed."}}
				text := model.NewTextPart("Starting the requested work.")
				calls := model.Message{Role: model.RoleAssistant, Parts: []model.Part{thinking, text, callA, callB}}
				answer := model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Ready to continue.")}}
				toolCalls, effects, modelCalls, writeAttempts := 0, 0, 0, 0
				work, err := functiontool.New(functiontool.Config{Name: "work"}, func(toolCtx context.Context, args struct {
					Task string `json:"task"`
				}) (map[string]string, error) {
					toolCalls++
					if toolCalls != 1 || args.Task != "a" || !reflect.DeepEqual(history(t, store).At(1).Message, &calls) {
						t.Fatal("unexpected execution or call was not saved before execution")
					}
					if tc.cancelAt == "before effect" {
						cancel()
						return nil, toolCtx.Err()
					}
					effects++ // Stand-in for an operation whose side effect cannot be rolled back.
					if tc.cancelAt == "after effect" {
						cancel()
					}
					return map[string]string{"completed": args.Task}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				service := serviceStub{Service: store, append: func(ctx context.Context, view session.Session, event *session.Event) error {
					if event.Partial {
						t.Fatal("Runner attempted to persist a partial event")
					}
					if event.Message != nil && event.Message.Role == model.RoleTool {
						writeAttempts++
						if tc.writeError == "before commit" {
							return writeFailure
						}
					}
					if err := store.AppendEvent(ctx, view, event); err != nil {
						return err
					}
					if event.Message != nil && event.Message.Role == model.RoleTool && tc.writeError == "after commit" {
						return writeFailure
					}
					return nil
				}}
				firstInput, nextInput := userMessage("Do both operations."), userMessage("Continue the conversation.")
				kept := model.Message{Role: model.RoleAssistant, Parts: []model.Part{thinking, text}}
				wantNext := []model.Message{*firstInput, kept}
				if tc.wantSaved == 1 {
					wantNext[1].Parts = append(wantNext[1].Parts, callA)
					wantNext = append(wantNext, model.Message{Role: model.RoleTool, Parts: []model.Part{{
						Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call-a", Content: `{"completed":"a"}`},
					}}})
				}
				wantNext = append(wantNext, *nextInput)
				a, err := llmagent.New(llmagent.Config{Name: "worker", MaxModelCalls: 3, Tools: []tool.Tool{work},
					Model: modelFunc(func(_ context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
						modelCalls++
						if stream != streaming {
							t.Fatal("streaming mode did not reach the model")
						}
						if err := req.Validate(); err != nil {
							t.Fatal(err)
						}
						result := model.Result{Message: &calls, StopReason: model.StopReasonToolCalls}
						if modelCalls == 1 {
							if !reflect.DeepEqual(req.Messages, []model.Message{*firstInput}) {
								t.Fatalf("first request: %+v", req.Messages)
							}
						} else {
							if modelCalls != 2 || !reflect.DeepEqual(req.Messages, wantNext) {
								got, _ := json.Marshal(req.Messages)
								want, _ := json.Marshal(wantNext)
								t.Fatalf("next request = %s, want %s", got, want)
							}
							result = model.Result{Message: &answer, StopReason: model.StopReasonStop}
						}
						return func(yield func(model.Event, error) bool) {
							if stream {
								for _, delta := range []model.Event{model.PartStart{Kind: model.PartText}, model.TextDelta{Delta: "Working."}, model.PartEnd{}} {
									if !yield(delta, nil) {
										return
									}
								}
							}
							yield(model.ResultEvent{Result: result}, nil)
						}
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: service})
				if err != nil {
					t.Fatal(err)
				}
				var runErr error
				for event, err := range r.Run(ctx, "user", "session", firstInput, runner.WithStreaming(streaming)) {
					if err != nil {
						runErr = err
						break
					}
					if !event.Partial && event.Message.Role == tc.stopAfter {
						break
					}
				}
				if !errors.Is(runErr, tc.wantErr) || modelCalls != 1 || toolCalls != tc.wantCalls || effects != tc.wantEffects {
					t.Fatalf("interruption: error=%v, model=%d, tool=%d, effects=%d", runErr, modelCalls, toolCalls, effects)
				}
				wantWrites := tc.wantSaved
				if tc.writeError != "" {
					wantWrites = 1
				}
				if writeAttempts != wantWrites {
					t.Fatalf("result writes=%d, want %d", writeAttempts, wantWrites)
				}
				saved := slices.Collect(history(t, store).All())
				if len(saved) != 2+tc.wantSaved || !reflect.DeepEqual(saved[1].Message, &calls) {
					t.Fatalf("saved history after interruption: %+v", saved)
				}
				before, err := json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
				// Use a fresh context and let Runner reload the actual committed history.
				completed := 0
				for event, err := range r.Run(t.Context(), "user", "session", nextInput, runner.WithStreaming(streaming)) {
					if err != nil {
						t.Fatal(err)
					}
					if !event.Partial {
						completed++
						if !reflect.DeepEqual(event.Message, &answer) {
							t.Fatalf("unexpected continuation: %+v", event)
						}
					}
				}
				if completed != 1 || modelCalls != 2 || toolCalls != tc.wantCalls || effects != tc.wantEffects || writeAttempts != wantWrites {
					t.Fatalf("continuation repeated work: completed=%d, model=%d, tool=%d, effects=%d, writes=%d", completed, modelCalls, toolCalls, effects, writeAttempts)
				}
				after := slices.Collect(history(t, store).All())
				if len(after) != len(saved)+2 {
					t.Fatalf("continuation appended %d events, want user input and answer only", len(after)-len(saved))
				}
				prefix, err := json.Marshal(after[:len(saved)])
				if err != nil || !bytes.Equal(before, prefix) {
					t.Fatalf("continuation modified original history: %s, %v", prefix, err)
				}
				if after[len(saved)].InvocationID == saved[0].InvocationID {
					t.Fatal("continuation did not use a new invocation")
				}
			})
		}
	}
}
