package model_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func requestFixture() model.Request {
	return model.Request{
		Instructions: " Follow the tool results.\n",
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{textPart("Weather in \u5317\u4eac?")}},
			{Role: model.RoleAssistant, Parts: []model.Part{
				textPart("Checking the weather."),
				callPart("call_1", "weather", `{"city":"Beijing","id":9007199254740993}`),
				textPart(""),
			}},
			{Role: model.RoleTool, Parts: []model.Part{resultPart("call_1", "service unavailable", true)}},
		},
		Tools: []model.ToolDefinition{
			{
				Name: "weather", Description: "Current weather",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"},"id":{"type":"integer","minimum":9007199254740993}},"required":["city"],"additionalProperties":false}`),
			},
			toolDefinition("clock", objectSchema),
		},
	}
}

// Preserve nested values and nil/empty distinctions for read-only assertions.
func cloneRequestForTest(request model.Request) model.Request {
	if request.Config != nil {
		config := *request.Config
		if config.MaxOutputTokens != nil {
			config.MaxOutputTokens = tokenCount(*config.MaxOutputTokens)
		}
		if config.ToolChoice != nil {
			choice := *config.ToolChoice
			config.ToolChoice = &choice
		}
		request.Config = &config
	}
	request.Messages = slices.Clone(request.Messages)
	for i, message := range request.Messages {
		message.Parts = slices.Clone(message.Parts)
		for j, part := range message.Parts {
			if part.Text != nil {
				text := *part.Text
				part.Text = &text
			}
			if part.Thinking != nil {
				thinking := *part.Thinking
				part.Thinking = &thinking
			}
			if part.ToolCall != nil {
				call := *part.ToolCall
				call.Arguments = bytes.Clone(call.Arguments)
				part.ToolCall = &call
			}
			if part.ToolResult != nil {
				result := *part.ToolResult
				part.ToolResult = &result
			}
			message.Parts[j] = part
		}
		request.Messages[i] = message
	}
	request.Tools = slices.Clone(request.Tools)
	for i := range request.Tools {
		request.Tools[i].InputSchema = bytes.Clone(request.Tools[i].InputSchema)
	}
	return request
}

func TestRequestValidate(t *testing.T) {
	messages := []model.Message{{Role: model.RoleUser, Parts: []model.Part{textPart("hello")}}}
	historical := requestFixture()
	historical.Tools = nil
	tests := []struct {
		name      string
		request   model.Request
		wantField string
	}{
		{"zero request", model.Request{}, "request"},
		{"empty messages", model.Request{Messages: []model.Message{}}, "request"},
		{"blank instructions", model.Request{Instructions: " \t\n\u3000"}, "request"},
		{"tools only", model.Request{Tools: []model.ToolDefinition{toolDefinition("weather", objectSchema)}}, "request"},
		{"messages only", model.Request{Messages: messages}, ""},
		{"instructions only", model.Request{Instructions: "Say hello."}, ""},
		{"instructions with empty messages", model.Request{Instructions: "Say hello.", Messages: []model.Message{}}, ""},
		{"blank instructions with messages", model.Request{Instructions: " \t\n", Messages: messages}, ""},
		{"empty tools", model.Request{Messages: messages, Tools: []model.ToolDefinition{}}, ""},
		{"full request", requestFixture(), ""},
		{"retired historical tool and final tool message", historical, ""},
		{
			"empty text is structurally valid",
			model.Request{Messages: []model.Message{{Role: model.RoleUser, Parts: []model.Part{textPart("")}}}},
			"",
		},
		{
			"invalid message",
			model.Request{Messages: []model.Message{{}}},
			"messages[0].role",
		},
		{
			"nested message error",
			model.Request{Messages: []model.Message{
				messages[0],
				{Role: model.RoleAssistant, Parts: []model.Part{callPart("call_1", "weather", `{"city":`)}},
			}},
			"messages[1].parts[0].tool_call.arguments",
		},
		{
			"invalid tool name",
			model.Request{Messages: messages, Tools: []model.ToolDefinition{toolDefinition("", objectSchema)}},
			"tools[0].name",
		},
		{
			"invalid schema",
			model.Request{Messages: messages, Tools: []model.ToolDefinition{toolDefinition("weather", `null`)}},
			"tools[0].input_schema",
		},
		{
			"invalid schema root type",
			model.Request{Messages: messages, Tools: []model.ToolDefinition{toolDefinition("weather", `{"type":null}`)}},
			"tools[0].input_schema.type",
		},
		{
			"duplicate tool names",
			model.Request{Messages: messages, Tools: []model.ToolDefinition{
				toolDefinition("weather", objectSchema), toolDefinition("weather", objectSchema),
			}},
			"tools[1].name",
		},
		{
			"tool names are compared exactly",
			model.Request{Messages: messages, Tools: []model.ToolDefinition{
				toolDefinition("weather", objectSchema), toolDefinition("Weather", objectSchema), toolDefinition(" weather ", objectSchema),
			}},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want error at %s", err, tt.wantField)
			}
		})
	}
}

func TestRequestValidateErrorOrder(t *testing.T) {
	tests := []struct {
		name      string
		request   model.Request
		wantField string
	}{
		{
			"input before declarations",
			model.Request{Tools: []model.ToolDefinition{{}}},
			"request",
		},
		{
			"messages before declarations",
			model.Request{Messages: []model.Message{{}}, Tools: []model.ToolDefinition{{}}},
			"messages[0].role",
		},
		{
			"earlier message before later message",
			model.Request{Messages: []model.Message{{Role: model.RoleUser}, {}}},
			"messages[0].parts",
		},
		{
			"earlier declaration before later declaration",
			model.Request{Instructions: "hello", Tools: []model.ToolDefinition{toolDefinition("weather", "{"), {}}},
			"tools[0].input_schema",
		},
		{
			"invalid declaration before duplicate name",
			model.Request{Instructions: "hello", Tools: []model.ToolDefinition{
				toolDefinition("weather", objectSchema), toolDefinition("weather", "{"),
			}},
			"tools[1].input_schema",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want first error at %s", err, tt.wantField)
			}
		})
	}
}

func TestRequestValidateErrorChain(t *testing.T) {
	tests := []struct {
		name      string
		request   model.Request
		wantField string
		syntax    bool
	}{
		{
			"message arguments",
			model.Request{Messages: []model.Message{
				{Role: model.RoleAssistant, Parts: []model.Part{callPart("call_1", "weather", "{")}},
			}},
			"messages[0].parts[0].tool_call.arguments",
			true,
		},
		{
			"tool schema",
			model.Request{Instructions: "hello", Tools: []model.ToolDefinition{toolDefinition("weather", "{")}},
			"tools[0].input_schema",
			true,
		},
		{
			"schema root type",
			model.Request{Instructions: "hello", Tools: []model.ToolDefinition{toolDefinition("weather", `{"type":42}`)}},
			"tools[0].input_schema.type",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want error at %s", err, tt.wantField)
			}
			if tt.syntax {
				var cause *json.SyntaxError
				if !errors.As(err, &cause) {
					t.Fatalf("error chain lost JSON syntax error: %v", err)
				}
			} else {
				var cause *json.UnmarshalTypeError
				if !errors.As(err, &cause) {
					t.Fatalf("error chain lost JSON type error: %v", err)
				}
			}
		})
	}
}

func TestRequestValidateDoesNotMutate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*model.Request)
		wantErr bool
	}{
		{"valid request", func(*model.Request) {}, false},
		{"nil lists", func(r *model.Request) { r.Messages, r.Tools = nil, nil }, false},
		{"empty lists", func(r *model.Request) { r.Messages, r.Tools = []model.Message{}, []model.ToolDefinition{} }, false},
		{"invalid message", func(r *model.Request) { r.Messages[1] = model.Message{} }, true},
		{"invalid schema", func(r *model.Request) { r.Tools[1].InputSchema = json.RawMessage("{") }, true},
		{"nil schema", func(r *model.Request) { r.Tools[1].InputSchema = nil }, true},
		{"empty schema", func(r *model.Request) { r.Tools[1].InputSchema = json.RawMessage{} }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := requestFixture()
			setCallArguments(&request.Messages[1], 1, json.RawMessage(" \n { \"id\": 9007199254740993 } \t"))
			request.Tools[0].Name = " weather "
			request.Tools[0].Description = " local weather\n"
			request.Tools[0].InputSchema = json.RawMessage(" \n { \"type\": \"object\", \"properties\": { \"id\": { \"minimum\": 9007199254740993 } } } \t")
			tt.mutate(&request)
			before := cloneRequestForTest(request)
			if err := request.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, want error: %t", err, tt.wantErr)
			}
			if !reflect.DeepEqual(request, before) {
				t.Fatal("Validate changed request data or nil/empty distinctions")
			}
		})
	}
}

func TestRequestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		request model.Request
	}{
		{"full request", requestFixture()},
		{"nil lists", model.Request{Instructions: "Say hello."}},
		{"empty lists", model.Request{Instructions: "Say hello.", Messages: []model.Message{}, Tools: []model.ToolDefinition{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.request.Validate(); err != nil {
				t.Fatalf("original request: %v", err)
			}
			data, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatal(err)
			}
			var got model.Request
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("decoded request: %v", err)
			}
			want := tt.request
			if len(want.Messages) == 0 {
				want.Messages = nil
			}
			if len(want.Tools) == 0 {
				want.Tools = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON round trip changed request semantics: %s", data)
			}
		})
	}
}
