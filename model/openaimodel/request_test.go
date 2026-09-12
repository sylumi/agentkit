package openaimodel

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func requestFixture() model.Request {
	text := "hello"
	return model.Request{
		Instructions: "Be concise.",
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(text)}},
			{Role: model.RoleAssistant, Parts: []model.Part{
				model.NewTextPart(text),
				{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(` {"id":9007199254740993} `)}},
			}},
			{Role: model.RoleTool, Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call_1", Content: "missing", IsError: true}}}},
		},
		Tools: []model.ToolDefinition{{Name: "lookup", Description: "Look up an ID", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":9007199254740993}}}`)}},
	}
}

func TestRequestMapping(t *testing.T) {
	req := requestFixture()
	p, err := buildOpenAIParams("test-model", req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Store        *bool  `json:"store"`
		Input        []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Tools []struct {
			Strict     *bool           `json:"strict"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Model != "test-model" || wire.Instructions != req.Instructions || wire.Store == nil || *wire.Store || len(wire.Input) != 4 {
		t.Fatalf("incorrect request: %s", data)
	}
	if wire.Input[0].Role != "user" || wire.Input[1].Role != "assistant" || wire.Input[2].Arguments != ` {"id":9007199254740993} ` || wire.Input[3].CallID != "call_1" {
		t.Fatalf("lost input order, role, ID, or raw arguments: %s", data)
	}
	for _, item := range wire.Input[:2] {
		if len(item.Content) != 1 || item.Content[0].Type != "input_text" || item.Content[0].Text != "hello" {
			t.Fatalf("lost text content: %s", data)
		}
	}
	var toolError struct {
		IsError bool   `json:"is_error"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(wire.Input[3].Output), &toolError); err != nil || !toolError.IsError || toolError.Content != "missing" {
		t.Fatal("lost tool error marker")
	}
	if wire.Tools[0].Strict == nil || *wire.Tools[0].Strict || !bytes.Contains(wire.Tools[0].Parameters, []byte("9007199254740993")) {
		t.Fatalf("changed schema or enabled strict mode: %s", data)
	}
	p.Tools[0].OfFunction.Parameters["type"] = "changed"
	p.Input.OfInputItemList[0].OfMessage.Content.OfInputItemContentList[0].OfInputText.Text = "changed"
	if !reflect.DeepEqual(req, requestFixture()) {
		t.Fatal("mapping retained mutable request data")
	}
	message := &req.Messages[2]
	message.Parts[0].ToolResult.IsError = false
	p, err = buildOpenAIParams("test", req)
	if err != nil || p.Input.OfInputItemList[3].OfFunctionCallOutput.Output.OfString.Value != "missing" {
		t.Fatalf("ordinary tool output: %v", err)
	}
}

func TestRequestTextMessageBoundaries(t *testing.T) {
	text := func(value string) model.Part {
		return model.NewTextPart(value)
	}
	call := model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
		ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"id":1}`),
	}}
	for _, tt := range []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{
			name: "user text blocks",
			messages: []model.Message{
				{Role: model.RoleUser, Parts: []model.Part{text("first\n"), text(" second ")}},
			},
			want: `[{"role":"user","content":[{"type":"input_text","text":"first\n"},{"type":"input_text","text":" second "}]}]`,
		},
		{
			name: "separate assistant messages and empty text",
			messages: []model.Message{
				{Role: model.RoleAssistant, Parts: []model.Part{text(""), text("answer")}},
				{Role: model.RoleAssistant, Parts: []model.Part{text("")}},
			},
			want: `[
				{"role":"assistant","content":[{"type":"input_text","text":""},{"type":"input_text","text":"answer"}]},
				{"role":"assistant","content":[{"type":"input_text","text":""}]}
			]`,
		},
		{
			name: "tool call separates text runs",
			messages: []model.Message{
				{Role: model.RoleAssistant, Parts: []model.Part{text("before"), text(" call"), call, text("after"), text(" call")}},
				{Role: model.RoleTool, Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call_1", Content: "found"}}}},
			},
			want: `[
				{"role":"assistant","content":[{"type":"input_text","text":"before"},{"type":"input_text","text":" call"}]},
				{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"id\":1}"},
				{"role":"assistant","content":[{"type":"input_text","text":"after"},{"type":"input_text","text":" call"}]},
				{"type":"function_call_output","call_id":"call_1","output":"found"}
			]`,
		},
		{
			name: "batched tool results keep IDs and order",
			messages: []model.Message{
				{Role: model.RoleTool, Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call_2", Content: "second"}}, {Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call_1", Content: "first"}}}},
			},
			want: `[
				{"type":"function_call_output","call_id":"call_2","output":"second"},
				{"type":"function_call_output","call_id":"call_1","output":"first"}
			]`,
		},
		{
			name: "tool call at both text boundaries",
			messages: []model.Message{
				{Role: model.RoleAssistant, Parts: []model.Part{call, text("middle"), text(" text"), {Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{
					ID: "call_2", Name: "lookup", Arguments: json.RawMessage(`{}`),
				}}}},
			},
			want: `[
				{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"id\":1}"},
				{"role":"assistant","content":[{"type":"input_text","text":"middle"},{"type":"input_text","text":" text"}]},
				{"type":"function_call","call_id":"call_2","name":"lookup","arguments":"{}"}
			]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := model.Request{Messages: tt.messages}
			if err := req.Validate(); err != nil {
				t.Fatal(err)
			}
			p, err := buildOpenAIParams("test-model", req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(p.Input)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("input = %s\nwant = %s", data, tt.want)
			}
		})
	}
}

func TestRequestGenerateConfigMapping(t *testing.T) {
	limit := int64(1024)
	for _, tt := range []struct {
		name       string
		config     *model.GenerateConfig
		wantChoice string
	}{
		{"unspecified", nil, ""},
		{"empty config", &model.GenerateConfig{}, ""},
		{"limit only", &model.GenerateConfig{MaxOutputTokens: &limit}, ""},
		{"auto", &model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceAuto}}, `"auto"`},
		{"none", &model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNone}}, `"none"`},
		{"required", &model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceRequired}}, `"required"`},
		{"named", &model.GenerateConfig{MaxOutputTokens: &limit, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "lookup"}}, `{"name":"lookup","type":"function"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := requestFixture()
			req.Config = tt.config
			if err := req.Validate(); err != nil {
				t.Fatal(err)
			}
			p, err := buildOpenAIParams("test-model", req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			var gotChoice, wantChoice any
			if raw := wire["tool_choice"]; raw != nil {
				if err := json.Unmarshal(raw, &gotChoice); err != nil {
					t.Fatal(err)
				}
			}
			if tt.wantChoice != "" {
				if err := json.Unmarshal([]byte(tt.wantChoice), &wantChoice); err != nil {
					t.Fatal(err)
				}
			} else if _, exists := wire["tool_choice"]; exists {
				t.Fatal("unspecified choice must be omitted")
			}
			if !reflect.DeepEqual(gotChoice, wantChoice) {
				t.Fatalf("tool_choice = %s, want %s", wire["tool_choice"], tt.wantChoice)
			}
			wantLimit := ""
			if tt.config != nil && tt.config.MaxOutputTokens != nil {
				wantLimit = "1024"
			}
			if string(wire["max_output_tokens"]) != wantLimit {
				t.Fatalf("max_output_tokens = %s, want %s", wire["max_output_tokens"], wantLimit)
			}
		})
	}
}

func TestRequestRejectsThinkingHistory(t *testing.T) {
	for _, kind := range []model.ThinkingKind{model.ThinkingUnknown, model.ThinkingText, model.ThinkingSummary} {
		part := model.ThinkingPart{Kind: kind, Text: "thinking"}
		req := model.Request{Messages: []model.Message{{Role: model.RoleAssistant, Parts: []model.Part{{Kind: model.PartThinking, Thinking: &part}}}}}
		_, err := buildOpenAIParams("test-model", req)
		if err == nil || !strings.Contains(err.Error(), "thinking history is not supported") {
			t.Fatalf("accepted thinking history kind %q: %v", kind, err)
		}
	}
}
