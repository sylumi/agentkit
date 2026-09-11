package model_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func tokenCount(value int64) *int64 {
	return &value
}

func resultFixture() model.Result {
	return model.Result{
		Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{
			textPart("Checking \u5317\u4eac.\n"),
			callPart("call_1", "weather", `{"city":"Beijing","id":9007199254740993}`),
			textPart(""),
			callPart("call_2", "clock", `{}`),
		}},
		StopReason: model.StopReasonToolCalls,
		Usage: model.Usage{
			InputTokens: tokenCount(9007199254740993), OutputTokens: tokenCount(math.MaxInt64),
		},
	}
}

func TestUsageValidate(t *testing.T) {
	tests := []struct {
		name      string
		usage     model.Usage
		wantField string
	}{
		{"unknown counts", model.Usage{}, ""},
		{"known input zero", model.Usage{InputTokens: tokenCount(0)}, ""},
		{"known output zero", model.Usage{OutputTokens: tokenCount(0)}, ""},
		{"both zero", model.Usage{InputTokens: tokenCount(0), OutputTokens: tokenCount(0)}, ""},
		{"known input only", model.Usage{InputTokens: tokenCount(12)}, ""},
		{"known output only", model.Usage{OutputTokens: tokenCount(18)}, ""},
		{"both positive", model.Usage{InputTokens: tokenCount(12), OutputTokens: tokenCount(18)}, ""},
		{"maximum counts without summing", model.Usage{InputTokens: tokenCount(math.MaxInt64), OutputTokens: tokenCount(math.MaxInt64)}, ""},
		{"negative input", model.Usage{InputTokens: tokenCount(-1)}, "input_tokens"},
		{"negative output", model.Usage{OutputTokens: tokenCount(-1)}, "output_tokens"},
		{"input checked first", model.Usage{InputTokens: tokenCount(-1), OutputTokens: tokenCount(-1)}, "input_tokens"},
		{"minimum input", model.Usage{InputTokens: tokenCount(math.MinInt64)}, "input_tokens"},
		{"minimum output", model.Usage{OutputTokens: tokenCount(math.MinInt64)}, "output_tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := model.Usage{}
			if tt.usage.InputTokens != nil {
				before.InputTokens = tokenCount(*tt.usage.InputTokens)
			}
			if tt.usage.OutputTokens != nil {
				before.OutputTokens = tokenCount(*tt.usage.OutputTokens)
			}
			err := tt.usage.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want error at %s", err, tt.wantField)
			}
			if !reflect.DeepEqual(tt.usage, before) {
				t.Fatal("Validate changed token counts or unknown values")
			}
		})
	}
}

func TestResultValidateMessageMatrix(t *testing.T) {
	reasons := []model.StopReason{
		model.StopReasonStop, model.StopReasonToolCalls, model.StopReasonLength, model.StopReasonBlocked,
	}
	tests := []struct {
		name       string
		message    *model.Message
		wantFields [4]string // stop, tool_calls, length, blocked
	}{
		{"no output", nil, [4]string{"", "message", "", ""}},
		{
			"thinking without answer text",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{thinkingPart("Check inputs.")}},
			[4]string{"", "message", "", ""},
		},
		{
			"empty thinking",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{thinkingPart("")}},
			[4]string{"", "message", "", ""},
		},
		{
			"thinking and a complete call",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{thinkingPart("Check weather."), callPart("call_1", "weather", `{}`)}},
			[4]string{"message", "", "", ""},
		},
		{
			"text",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{textPart("The weather is")}},
			[4]string{"", "message", "", ""},
		},
		{
			"empty text",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{textPart("")}},
			[4]string{"", "message", "", ""},
		},
		{
			"complete call",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{callPart("call_1", "weather", `{}`)}},
			[4]string{"message", "", "", ""},
		},
		{"text and multiple calls", resultFixture().Message, [4]string{"message", "", "", ""}},
		{
			"missing parts",
			&model.Message{Role: model.RoleAssistant},
			[4]string{"message.parts", "message.parts", "message.parts", "message.parts"},
		},
		{
			"incomplete arguments",
			&model.Message{Role: model.RoleAssistant, Parts: []model.Part{callPart("call_1", "weather", `{"city":`)}},
			[4]string{
				"message.parts[0].tool_call.arguments", "message.parts[0].tool_call.arguments",
				"message.parts[0].tool_call.arguments", "message.parts[0].tool_call.arguments",
			},
		},
	}
	for _, tt := range tests {
		for i, reason := range reasons {
			t.Run(tt.name+"/"+string(reason), func(t *testing.T) {
				result := model.Result{Message: tt.message, StopReason: reason}
				err := result.Validate()
				wantField := tt.wantFields[i]
				if wantField == "" {
					if err != nil {
						t.Fatalf("Validate() = %v", err)
					}
					return
				}
				if err == nil || !strings.HasPrefix(err.Error(), wantField+":") {
					t.Fatalf("Validate() = %v, want error at %s", err, wantField)
				}
			})
		}
	}
}

func TestResultValidateInvalidReason(t *testing.T) {
	for _, reason := range []model.StopReason{"", "unknown", "error", "cancelled", "STOP", " stop "} {
		t.Run("reason/"+string(reason), func(t *testing.T) {
			result := model.Result{StopReason: reason}
			if err := result.Validate(); err == nil || !strings.HasPrefix(err.Error(), "stop_reason:") {
				t.Fatalf("Validate() = %v, want error at stop_reason", err)
			}
		})
	}
}

func TestDecodedResultRejectsWrongRole(t *testing.T) {
	for _, role := range []string{"", "unknown", "user", "tool"} {
		t.Run(role, func(t *testing.T) {
			data := `{"message":{"role":"` + role + `","parts":[]},"stop_reason":"stop"}`
			var result model.Result
			if err := json.Unmarshal([]byte(data), &result); err != nil {
				t.Fatal(err)
			}
			if err := result.Validate(); err == nil || !strings.HasPrefix(err.Error(), "message.role:") {
				t.Fatalf("Validate() = %v, want role error", err)
			}
		})
	}
}

func TestResultValidateErrorOrder(t *testing.T) {
	tests := []struct {
		name      string
		result    model.Result
		wantField string
	}{
		{
			"reason before message and usage",
			model.Result{Message: &model.Message{Role: model.RoleAssistant}, Usage: model.Usage{InputTokens: tokenCount(-1)}},
			"stop_reason",
		},
		{
			"parts before missing call and usage",
			model.Result{Message: &model.Message{Role: model.RoleAssistant}, StopReason: model.StopReasonToolCalls, Usage: model.Usage{InputTokens: tokenCount(-1)}},
			"message.parts",
		},
		{
			"later invalid part before stop combination",
			model.Result{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{
				callPart("call_1", "weather", `{}`), callPart("call_2", "clock", "{"),
			}}, StopReason: model.StopReasonStop},
			"message.parts[1].tool_call.arguments",
		},
		{
			"missing call before usage",
			model.Result{StopReason: model.StopReasonToolCalls, Usage: model.Usage{InputTokens: tokenCount(-1)}},
			"message",
		},
		{
			"forbidden call before usage",
			model.Result{Message: resultFixture().Message, StopReason: model.StopReasonStop, Usage: model.Usage{InputTokens: tokenCount(-1)}},
			"message",
		},
		{
			"input usage before output",
			model.Result{StopReason: model.StopReasonStop, Usage: model.Usage{InputTokens: tokenCount(-1), OutputTokens: tokenCount(-1)}},
			"usage.input_tokens",
		},
		{
			"output usage after valid input",
			model.Result{StopReason: model.StopReasonBlocked, Usage: model.Usage{InputTokens: tokenCount(12), OutputTokens: tokenCount(-1)}},
			"usage.output_tokens",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.result.Validate(); err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want first error at %s", err, tt.wantField)
			}
		})
	}
}

func TestResultValidateErrorChain(t *testing.T) {
	for _, arguments := range []string{`{"city":`, `[]`} {
		t.Run(arguments, func(t *testing.T) {
			result := resultFixture()
			setCallArguments(result.Message, 1, json.RawMessage(arguments))
			err := result.Validate()
			if err == nil || !strings.HasPrefix(err.Error(), "message.parts[1].tool_call.arguments:") {
				t.Fatalf("Validate() = %v, want nested argument error", err)
			}
			if arguments == `[]` {
				var cause *json.UnmarshalTypeError
				if !errors.As(err, &cause) {
					t.Fatalf("error chain lost JSON type error: %v", err)
				}
			} else {
				var cause *json.SyntaxError
				if !errors.As(err, &cause) {
					t.Fatalf("error chain lost JSON syntax error: %v", err)
				}
			}
		})
	}
}

func TestResultValidateDoesNotMutate(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*model.Result)
		wantErr bool
	}{
		{"valid calls", func(*model.Result) {}, false},
		{"no output", func(r *model.Result) { r.Message = nil; r.StopReason = model.StopReasonBlocked }, false},
		{"unknown usage", func(r *model.Result) { r.Usage = model.Usage{} }, false},
		{"known zero", func(r *model.Result) { *r.Usage.OutputTokens = 0 }, false},
		{"length with calls", func(r *model.Result) { r.StopReason = model.StopReasonLength }, false},
		{"invalid reason", func(r *model.Result) { r.StopReason = "unknown" }, true},
		{"empty part", func(r *model.Result) { r.Message.Parts[0] = model.Part{} }, true},
		{"nil parts", func(r *model.Result) { r.Message.Parts = nil }, true},
		{"empty parts", func(r *model.Result) { r.Message.Parts = []model.Part{} }, true},
		{"nil arguments", func(r *model.Result) { setCallArguments(r.Message, 1, nil) }, true},
		{"empty arguments", func(r *model.Result) { setCallArguments(r.Message, 1, json.RawMessage{}) }, true},
		{"truncated arguments", func(r *model.Result) { setCallArguments(r.Message, 1, json.RawMessage(`{"city":`)) }, true},
		{"invalid combination", func(r *model.Result) { r.StopReason = model.StopReasonStop }, true},
		{"negative input", func(r *model.Result) { *r.Usage.InputTokens = -1 }, true},
		{"negative output", func(r *model.Result) { *r.Usage.OutputTokens = -1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			makeResult := func() model.Result {
				result := resultFixture()
				result.Message.Parts[1].ToolCall = &model.ToolCallPart{
					ID: " call_1 ", Name: " weather ", Arguments: json.RawMessage(" \n { \"id\": 9007199254740993 } \t"),
				}
				tt.change(&result)
				return result
			}
			result, before := makeResult(), makeResult()
			if err := result.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, want error: %t", err, tt.wantErr)
			}
			if !reflect.DeepEqual(result, before) {
				t.Fatal("Validate changed result data, raw arguments, or nil/empty distinctions")
			}
		})
	}
}

func TestResultJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		result    model.Result
		wantUsage string
	}{
		{"no output or usage", model.Result{StopReason: model.StopReasonStop}, `{}`},
		{"length without output", model.Result{StopReason: model.StopReasonLength}, `{}`},
		{
			"blocked with known output zero",
			model.Result{StopReason: model.StopReasonBlocked, Usage: model.Usage{OutputTokens: tokenCount(0)}},
			`{"output_tokens":0}`,
		},
		{
			"empty text with known input zero",
			model.Result{Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{textPart("")}}, StopReason: model.StopReasonStop, Usage: model.Usage{InputTokens: tokenCount(0)}},
			`{"input_tokens":0}`,
		},
		{
			"both counts known zero",
			model.Result{StopReason: model.StopReasonStop, Usage: model.Usage{InputTokens: tokenCount(0), OutputTokens: tokenCount(0)}},
			`{"input_tokens":0,"output_tokens":0}`,
		},
		{"calls and precise counts", resultFixture(), `{"input_tokens":9007199254740993,"output_tokens":9223372036854775807}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.result.Validate(); err != nil {
				t.Fatalf("original result: %v", err)
			}
			data, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["stop_reason"]) != `"`+string(tt.result.StopReason)+`"` {
				t.Fatalf("missing or incorrect stop_reason: %s", data)
			}
			if string(fields["usage"]) != tt.wantUsage {
				t.Fatalf("usage = %s, want %s", fields["usage"], tt.wantUsage)
			}
			if _, present := fields["message"]; present != (tt.result.Message != nil) {
				t.Fatalf("message presence does not match nil/non-nil message: %s", data)
			}
			var got model.Result
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("decoded result: %v", err)
			}
			if !reflect.DeepEqual(got, tt.result) {
				t.Fatalf("JSON round trip changed result content or counts: %s", data)
			}
		})
	}
}
