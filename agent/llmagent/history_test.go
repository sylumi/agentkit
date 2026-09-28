package llmagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/tool"
)

type historyEvents []*session.Event

func (e historyEvents) All() iter.Seq[*session.Event] { return slices.Values(e) }
func (e historyEvents) Len() int                      { return len(e) }
func (e historyEvents) At(i int) *session.Event {
	if i < 0 || i >= len(e) {
		return nil
	}
	return e[i]
}

type historySession struct {
	session.Session
	events historyEvents
}

func (s historySession) Events() session.Events { return s.events }

func historyCall(id string) model.Part {
	return model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
		ID: id, Name: "lookup", Arguments: json.RawMessage(` {"id":9007199254740993} `),
	}}
}

func historyResult(id, content string, isError bool) model.Part {
	return model.Part{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{
		CallID: id, Content: content, IsError: isError,
	}}
}

func historyMessage(role model.Role, parts ...model.Part) model.Message {
	// Include spare capacity to detect accidental appends to shared slices.
	backing := append(slices.Clone(parts), model.NewTextPart("unused capacity"))
	return model.Message{Role: role, Parts: backing[:len(parts)]}
}

func eventsForHistory(messages ...model.Message) historyEvents {
	var events historyEvents
	for _, message := range messages {
		// Event IDs and authors are deliberately absent. Association must not
		// depend on these fields or on matching InvocationIDs.
		events = append(events, &session.Event{Message: &message})
	}
	return events
}

func historySnapshot(t *testing.T, events historyEvents) []byte {
	t.Helper()
	var backing [][]model.Part
	var arguments []string
	for _, event := range events {
		if event != nil && event.Message != nil {
			backing = append(backing, event.Message.Parts[:cap(event.Message.Parts)])
			for _, part := range event.Message.Parts {
				if part.ToolCall != nil {
					// JSON marshaling normalizes RawMessage whitespace; also keep
					// its exact bytes to detect changes to the saved arguments.
					arguments = append(arguments, string(part.ToolCall.Arguments))
				}
			}
		}
	}
	data, err := json.Marshal(struct {
		Events    historyEvents
		Backing   [][]model.Part
		Arguments []string
	}{events, backing, arguments})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunNormalizesRequestHistory(t *testing.T) {
	msg := historyMessage
	callA, callB := historyCall("a"), historyCall("b")
	resultA, resultB := historyResult("a", "done", false), historyResult("b", "failed", true)
	newA := historyResult("a", "updated", false)
	text := model.NewTextPart("Checking the saved work.")
	thinking := model.Part{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingSummary, Text: "Need a lookup."}}
	user := msg(model.RoleUser, model.NewTextPart("Continue."))
	callMessage := msg(model.RoleAssistant, callA, callB)
	crossInvocation := eventsForHistory(msg(model.RoleAssistant, callA), user, msg(model.RoleTool, resultA))
	crossInvocation[0].InvocationID = "run-1"
	crossInvocation[2].InvocationID = "another-run"
	for _, tc := range []struct {
		name   string
		events historyEvents
		want   []model.Message
	}{
		{name: "empty"},
		{name: "ordinary dialogue", events: eventsForHistory(user, msg(model.RoleAssistant, thinking, text)),
			want: []model.Message{user, msg(model.RoleAssistant, thinking, text)}},
		{name: "unmatched parts preserve text and thinking", events: eventsForHistory(
			msg(model.RoleAssistant, thinking, callA, text), msg(model.RoleTool, resultB), user),
			want: []model.Message{msg(model.RoleAssistant, thinking, text), user}},
		{name: "orphan call and result messages disappear", events: eventsForHistory(
			msg(model.RoleAssistant, callA), msg(model.RoleTool, resultB), user), want: []model.Message{user}},
		{name: "partial batch retains real error", events: eventsForHistory(callMessage, msg(model.RoleTool, resultB), user),
			want: []model.Message{msg(model.RoleAssistant, callB), msg(model.RoleTool, resultB), user}},
		{name: "complete batch merges results in source order", events: eventsForHistory(callMessage, msg(model.RoleTool, resultB), msg(model.RoleTool, resultA), user),
			want: []model.Message{callMessage, msg(model.RoleTool, resultB, resultA), user}},
		{name: "late results split by call message", events: eventsForHistory(
			msg(model.RoleAssistant, callA), user, msg(model.RoleAssistant, text, callB), msg(model.RoleTool, resultB, resultA)),
			want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user, msg(model.RoleAssistant, text, callB), msg(model.RoleTool, resultB)}},
		{name: "last result wins", events: eventsForHistory(callMessage, msg(model.RoleTool, resultA), user, msg(model.RoleTool, resultB), msg(model.RoleTool, newA)),
			want: []model.Message{callMessage, msg(model.RoleTool, resultB, newA), user}},
		{name: "reused ID does not reuse an earlier result", events: eventsForHistory(
			msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), msg(model.RoleAssistant, callA), user),
			want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user}},
		{name: "reused ID does not answer an earlier call", events: eventsForHistory(
			msg(model.RoleAssistant, callA), user, msg(model.RoleAssistant, callA), msg(model.RoleTool, newA)),
			want: []model.Message{user, msg(model.RoleAssistant, callA), msg(model.RoleTool, newA)}},
		{name: "reused ID keeps independent results", events: eventsForHistory(
			msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user, msg(model.RoleAssistant, callA), msg(model.RoleTool, newA)),
			want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user, msg(model.RoleAssistant, callA), msg(model.RoleTool, newA)}},
		{name: "result before calls belongs only to the first", events: eventsForHistory(
			msg(model.RoleTool, resultA), msg(model.RoleAssistant, callA), user, msg(model.RoleAssistant, callA)),
			want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user}},
		{name: "current and cross invocation calls", events: crossInvocation,
			want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA), user}},
		{name: "partial and metadata events ignored", events: historyEvents{
			{Partial: true, Delta: model.TextDelta{Delta: "draft"}, Message: &callMessage},
			{StopReason: model.StopReasonBlocked}, {Message: &user},
		}, want: []model.Message{user}},
		{name: "stop reasons do not override real results", events: historyEvents{
			{Message: &callMessage, StopReason: model.StopReasonLength},
			{Message: &model.Message{Role: model.RoleTool, Parts: []model.Part{resultA}}},
			{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{callB}}, StopReason: model.StopReasonBlocked},
		}, want: []model.Message{msg(model.RoleAssistant, callA), msg(model.RoleTool, resultA)}},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/streaming=%t", tc.name, streaming), func(t *testing.T) {
				_, invocation := newInvocation(t)
				invocation.Session = historySession{Session: invocation.Session, events: tc.events}
				invocation.Streaming = streaming
				before := historySnapshot(t, tc.events)
				processed, modelCalls := 0, 0
				marker := msg(model.RoleUser, model.NewTextPart("processor context"))
				processor := processingToolset{
					toolsetFunc: toolsetFunc{"history-check", func(context.Context) ([]tool.Tool, error) { return nil, nil }},
					process: func(_ context.Context, req *model.Request) error {
						processed++
						if !reflect.DeepEqual(req.Messages, tc.want) {
							t.Fatalf("processor received history = %s, want %s", mustJSON(t, req.Messages), mustJSON(t, tc.want))
						}
						req.Messages = append(req.Messages, marker)
						return nil
					},
				}
				a, err := llmagent.New(llmagent.Config{
					Name: "chat", MaxModelCalls: 1, Toolsets: []tool.Toolset{processor},
					Tools: []tool.Tool{&stubTool{t: t, definition: model.ToolDefinition{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}},
					Model: modelFunc(func(_ context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
						modelCalls++
						want := append(slices.Clone(tc.want), marker)
						if stream != streaming || processed != modelCalls || !reflect.DeepEqual(req.Messages, want) {
							t.Fatalf("model received history = %s, want %s", mustJSON(t, req.Messages), mustJSON(t, want))
						}
						if err := req.Validate(); err != nil {
							t.Fatal(err)
						}
						return resultStream(model.Result{StopReason: model.StopReasonStop})
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				for run := range 2 {
					invocation.InvocationID = fmt.Sprintf("run-%d", run+1)
					for _, err := range a.Run(t.Context(), invocation) {
						if err != nil {
							t.Fatal(err)
						}
					}
					if !bytes.Equal(before, historySnapshot(t, tc.events)) {
						t.Fatal("request construction modified saved history or its backing slices")
					}
				}
				if modelCalls != 2 || processed != 2 {
					t.Fatalf("model calls=%d, preprocessing calls=%d", modelCalls, processed)
				}
			})
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunRejectsMalformedHistory(t *testing.T) {
	call := historyCall("a")
	for _, tc := range []struct {
		name  string
		event *session.Event
	}{
		{name: "nil event"},
		{name: "missing payload", event: &session.Event{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{{Kind: model.PartToolCall}}}}},
		{name: "blank call ID", event: &session.Event{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{historyCall(" ")}}}},
		{name: "duplicate ID within message", event: &session.Event{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{call, call}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, invocation := newInvocation(t)
			invocation.Session = historySession{Session: invocation.Session, events: historyEvents{{}, tc.event}}
			a, err := llmagent.New(llmagent.Config{Name: "chat", MaxModelCalls: 1,
				Model: modelFunc(func(context.Context, model.Request, bool) iter.Seq2[model.Event, error] {
					t.Fatal("malformed history reached the model")
					return nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for event, err := range a.Run(t.Context(), invocation) {
				count++
				if event != nil || err == nil || !strings.Contains(err.Error(), "history[1]") {
					t.Fatalf("event=%+v, error=%v", event, err)
				}
			}
			if count != 1 {
				t.Fatalf("got %d outcomes, want one error", count)
			}
		})
	}
}
