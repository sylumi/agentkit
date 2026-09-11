package model_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func eventFixtures() []model.Event {
	return []model.Event{
		model.PartStart{Kind: model.PartText},
		model.TextDelta{Index: 1, Delta: " hello\n"},
		model.ThinkingDelta{Index: 1, Delta: " Check constraints.\n"},
		model.ToolCallDelta{Index: 2, ID: "call_", Name: " weather ", Arguments: " \n { \"id\": 9007199254740993, \"city\":"},
		model.PartEnd{Index: 3},
		model.ResultEvent{Result: resultFixture()},
	}
}

type embeddedEvent struct{ model.Event }

type wrappedTextEvent struct {
	model.TextDelta
	called *bool
}

func (e wrappedTextEvent) String() string {
	*e.called = true
	return "wrapped text"
}

func TestValidateEventTypes(t *testing.T) {
	for i, event := range eventFixtures() {
		t.Run(fmt.Sprintf("%T", event), func(t *testing.T) {
			before := eventFixtures()[i]
			if err := model.ValidateEvent(event); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("validation changed an event or its nested data")
			}
		})
	}
	called := false
	unsupported := []model.Event{
		nil,
		(*model.PartStart)(nil), (*model.TextDelta)(nil), (*model.ThinkingDelta)(nil),
		(*model.ToolCallDelta)(nil), (*model.PartEnd)(nil), (*model.ResultEvent)(nil),
		&model.PartStart{Kind: model.PartText}, &model.TextDelta{Delta: "text"},
		&model.ThinkingDelta{Delta: "thinking"}, &model.ToolCallDelta{ID: "call"},
		&model.PartEnd{}, &model.ResultEvent{Result: resultFixture()},
		embeddedEvent{}, embeddedEvent{Event: model.TextDelta{Delta: "text"}},
		&embeddedEvent{}, (*embeddedEvent)(nil),
		wrappedTextEvent{TextDelta: model.TextDelta{Delta: "text"}, called: &called},
	}
	for i, event := range unsupported {
		t.Run(fmt.Sprintf("unsupported/%d/%T", i, event), func(t *testing.T) {
			if err := model.ValidateEvent(event); err == nil || !strings.HasPrefix(err.Error(), "event:") {
				t.Fatalf("ValidateEvent() = %v, want unsupported event error", err)
			}
		})
	}
	if called {
		t.Fatal("validation called an unsupported event's method")
	}
}

func TestValidateEventFields(t *testing.T) {
	tests := []struct {
		name  string
		event model.Event
		path  string
	}{
		{"tool start", model.PartStart{Kind: model.PartToolCall}, ""},
		{"thinking start", model.PartStart{Kind: model.PartThinking}, ""},
		{"non-sequential index is locally valid", model.PartStart{Index: math.MaxInt, Kind: model.PartText}, ""},
		{"negative start", model.PartStart{Index: -1, Kind: model.PartText}, "part_start.index"},
		{"missing block kind", model.PartStart{}, "part_start.kind"},
		{"unknown block kind", model.PartStart{Kind: "image"}, "part_start.kind"},
		{"tool result block", model.PartStart{Kind: model.PartToolResult}, "part_start.kind"},
		{"negative text index", model.TextDelta{Index: -1, Delta: "hello"}, "text_delta.index"},
		{"empty text", model.TextDelta{}, "text_delta.delta"},
		{"whitespace text", model.TextDelta{Delta: " \t\n\u3000"}, ""},
		{"negative thinking index", model.ThinkingDelta{Index: -1, Delta: "check"}, "thinking_delta.index"},
		{"empty thinking", model.ThinkingDelta{}, "thinking_delta.delta"},
		{"whitespace thinking", model.ThinkingDelta{Delta: " \t\n"}, ""},
		{"negative call index", model.ToolCallDelta{Index: -1, ID: "call_"}, "tool_call_delta.index"},
		{"empty call", model.ToolCallDelta{}, "tool_call_delta"},
		{"ID fragment", model.ToolCallDelta{ID: "call_"}, ""},
		{"name fragment", model.ToolCallDelta{Name: "wea"}, ""},
		{"arguments before metadata", model.ToolCallDelta{Arguments: `{"city":`}, ""},
		{"closing arguments", model.ToolCallDelta{Arguments: `"Beijing"}`}, ""},
		{"arguments not parsed", model.ToolCallDelta{Arguments: "[]"}, ""},
		{"whitespace ID", model.ToolCallDelta{ID: " "}, ""},
		{"whitespace name", model.ToolCallDelta{Name: "\t"}, ""},
		{"whitespace arguments", model.ToolCallDelta{Arguments: "\n"}, ""},
		{"negative end", model.PartEnd{Index: -1}, "part_end.index"},
		{"end without history", model.PartEnd{Index: 12}, ""},
		{"result without output", model.ResultEvent{Result: model.Result{StopReason: model.StopReasonStop}}, ""},
		{"length result", model.ResultEvent{Result: model.Result{StopReason: model.StopReasonLength}}, ""},
		{"blocked result", model.ResultEvent{Result: model.Result{StopReason: model.StopReasonBlocked}}, ""},
		{"zero result", model.ResultEvent{}, "result.stop_reason"},
		{"missing required call", model.ResultEvent{Result: model.Result{StopReason: model.StopReasonToolCalls}}, "result.message"},
		{"result usage error", model.ResultEvent{Result: model.Result{StopReason: model.StopReasonStop, Usage: model.Usage{InputTokens: tokenCount(-1)}}}, "result.usage.input_tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := model.ValidateEvent(tt.event)
			if tt.path == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.path+":") {
				t.Fatalf("ValidateEvent() = %v, want error at %s", err, tt.path)
			}
		})
	}
}

func TestValidateEventErrorOrder(t *testing.T) {
	tests := []struct {
		event model.Event
		path  string
	}{
		{model.PartStart{Index: -1}, "part_start.index"},
		{model.TextDelta{Index: -1}, "text_delta.index"},
		{model.ThinkingDelta{Index: -1}, "thinking_delta.index"},
		{model.ToolCallDelta{Index: -1}, "tool_call_delta.index"},
	}
	for _, tt := range tests {
		if err := model.ValidateEvent(tt.event); err == nil || !strings.HasPrefix(err.Error(), tt.path+":") {
			t.Fatalf("ValidateEvent() = %v, want first error at %s", err, tt.path)
		}
	}
}

func TestValidateEventErrorChain(t *testing.T) {
	for _, arguments := range []string{`{"city":`, "[]"} {
		t.Run(arguments, func(t *testing.T) {
			result := resultFixture()
			setCallArguments(result.Message, 1, json.RawMessage(arguments))
			err := model.ValidateEvent(model.ResultEvent{Result: result})
			if err == nil || !strings.HasPrefix(err.Error(), "result.message.parts[1].tool_call.arguments:") {
				t.Fatalf("ValidateEvent() = %v, want nested argument error", err)
			}
			if arguments == "[]" {
				var cause *json.UnmarshalTypeError
				if !errors.As(err, &cause) {
					t.Fatalf("lost JSON type error: %v", err)
				}
			} else {
				var cause *json.SyntaxError
				if !errors.As(err, &cause) {
					t.Fatalf("lost JSON syntax error: %v", err)
				}
			}
		})
	}
}

func TestValidateEventDoesNotMutateNestedData(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*model.Result)
		wantErr bool
	}{
		{"valid result", func(*model.Result) {}, false},
		{"invalid reason", func(r *model.Result) { r.StopReason = "unknown" }, true},
		{"nil parts", func(r *model.Result) { r.Message.Parts = nil }, true},
		{"empty parts", func(r *model.Result) { r.Message.Parts = []model.Part{} }, true},
		{"nil arguments", func(r *model.Result) { setCallArguments(r.Message, 1, nil) }, true},
		{"empty arguments", func(r *model.Result) { setCallArguments(r.Message, 1, json.RawMessage{}) }, true},
		{"truncated arguments", func(r *model.Result) { setCallArguments(r.Message, 1, json.RawMessage(" {")) }, true},
		{"negative input", func(r *model.Result) { *r.Usage.InputTokens = -1 }, true},
		{"negative output", func(r *model.Result) { *r.Usage.OutputTokens = -1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			makeEvent := func() model.ResultEvent {
				result := resultFixture()
				setCallArguments(result.Message, 1, json.RawMessage(" \n { \"id\": 9007199254740993 } \t"))
				tt.change(&result)
				return model.ResultEvent{Result: result}
			}
			event, before := makeEvent(), makeEvent()
			if err := model.ValidateEvent(event); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateEvent() = %v, want error: %t", err, tt.wantErr)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("validation changed nested data, raw bytes, or nil/empty distinctions")
			}
		})
	}
}

func TestPartialOutputJSONRoundTrip(t *testing.T) {
	empty := ""
	tests := []struct {
		name     string
		partial  model.PartialOutput
		wantJSON string
	}{
		{"zero snapshot", model.PartialOutput{}, `{"usage":{}}`},
		{
			"thinking before visible text",
			model.PartialOutput{Parts: []model.PartialPart{{Kind: model.PartThinking, Thinking: &model.ThinkingPart{}}}},
			`{"parts":[{"kind":"thinking","thinking":{"text":""},"ended":false}],"usage":{}}`,
		},
		{
			"ended thinking followed by unfinished thinking",
			model.PartialOutput{Parts: []model.PartialPart{
				{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Text: "Check inputs."}, Ended: true},
				{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Text: "Then check"}},
			}},
			`{"parts":[{"kind":"thinking","thinking":{"text":"Check inputs."},"ended":true},{"kind":"thinking","thinking":{"text":"Then check"},"ended":false}],"usage":{}}`,
		},
		{"empty parts are omitted", model.PartialOutput{Parts: []model.PartialPart{}}, `{"usage":{}}`},
		{
			"tool block before any delta",
			model.PartialOutput{Parts: []model.PartialPart{{Kind: model.PartToolCall, ToolCall: &model.PartialToolCall{}}}},
			`{"parts":[{"kind":"tool_call","tool_call":{"id":"","name":"","arguments":""},"ended":false}],"usage":{}}`,
		},
		{
			"ended empty text and open call with known usage",
			model.PartialOutput{
				Parts: []model.PartialPart{
					{Kind: model.PartText, Text: &empty, Ended: true},
					{Kind: model.PartToolCall, ToolCall: &model.PartialToolCall{ID: "call_", Name: "wea", Arguments: " \n {\"id\":9007199254740993,\"city\":"}},
				},
				Usage: model.Usage{InputTokens: tokenCount(math.MaxInt64), OutputTokens: tokenCount(0)},
			},
			`{"parts":[{"kind":"text","text":"","ended":true},{"kind":"tool_call","tool_call":{"id":"call_","name":"wea","arguments":" \n {\"id\":9007199254740993,\"city\":"},"ended":false}],"usage":{"input_tokens":9223372036854775807,"output_tokens":0}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.partial)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.wantJSON {
				t.Fatalf("JSON = %s, want %s", data, tt.wantJSON)
			}
			var got model.PartialOutput
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := tt.partial
			if len(want.Parts) == 0 {
				want.Parts = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON round trip changed diagnostic content or usage: %s", data)
			}
		})
	}
}
