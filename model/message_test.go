package model_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func textPart(text string) model.Part { return model.NewTextPart(text) }

func thinkingPart(text string) model.Part {
	return model.Part{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Text: text}}
}

func callPart(id, name, arguments string) model.Part {
	return model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{ID: id, Name: name, Arguments: json.RawMessage(arguments)}}
}

func resultPart(id, content string, isError bool) model.Part {
	return model.Part{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: id, Content: content, IsError: isError}}
}

func setCallArguments(message *model.Message, index int, arguments json.RawMessage) {
	message.Parts[index].ToolCall.Arguments = arguments
}

func TestMessageRoleContentMatrix(t *testing.T) {
	for _, role := range []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, "", "system", "unknown"} {
		for _, part := range []model.Part{textPart(""), thinkingPart(""), callPart("call", "tool", `{}`), resultPart("call", "", true)} {
			t.Run(string(role)+"/"+string(part.Kind), func(t *testing.T) {
				want := role == model.RoleUser && part.Kind == model.PartText ||
					role == model.RoleAssistant && part.Kind != model.PartToolResult ||
					role == model.RoleTool && part.Kind == model.PartToolResult
				message := model.Message{Role: role, Parts: []model.Part{part}}
				if err := message.Validate(); (err == nil) != want {
					t.Fatalf("Validate() = %v, want valid=%t", err, want)
				}
			})
		}
	}
}

func TestPartRejectsInvalidPayloads(t *testing.T) {
	text := ""
	for _, part := range []model.Part{
		{}, {Kind: model.PartText}, {Kind: "unknown", Text: &text},
		{Kind: model.PartThinking, Text: &text},
		{Kind: model.PartText, Thinking: &model.ThinkingPart{}},
		{Kind: model.PartToolCall, ToolResult: &model.ToolResultPart{CallID: "id"}},
		{Kind: model.PartToolResult, ToolCall: &model.ToolCallPart{ID: "id", Name: "tool", Arguments: json.RawMessage(`{}`)}},
		{Kind: model.PartText, Text: &text, Thinking: &model.ThinkingPart{}},
		{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: "invalid"}},
	} {
		if err := part.Validate(); err == nil {
			t.Errorf("accepted invalid part: %#v", part)
		}
	}
}

func TestMessageValidateStructure(t *testing.T) {
	for _, role := range []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool} {
		for _, parts := range [][]model.Part{nil, {}, {{}}} {
			if err := (model.Message{Role: role, Parts: parts}).Validate(); err == nil {
				t.Fatalf("accepted empty/invalid parts for %s", role)
			}
		}
	}
	for _, tc := range []struct {
		part  model.Part
		field string
	}{
		{callPart("", "weather", `{}`), "tool_call.id"},
		{callPart(" \t\n\u3000", "weather", `{}`), "tool_call.id"},
		{callPart("id", "", `{}`), "tool_call.name"},
		{callPart("id", "\t\u3000", `{}`), "tool_call.name"},
		{resultPart("", "", false), "tool_result.call_id"},
		{resultPart(" \n\u3000", "", true), "tool_result.call_id"},
	} {
		if err := tc.part.Validate(); err == nil || !strings.HasPrefix(err.Error(), tc.field) {
			t.Fatalf("Validate() = %v, want %s", err, tc.field)
		}
	}
	for _, role := range []model.Role{model.RoleAssistant, model.RoleTool} {
		makePart := func(id string) model.Part {
			if role == model.RoleTool {
				return resultPart(id, "", false)
			}
			return callPart(id, "tool", `{}`)
		}
		for _, id := range []string{"call", " call "} {
			m := model.Message{Role: role, Parts: []model.Part{makePart("call"), makePart(id)}}
			if err := m.Validate(); (err != nil) != (id == "call") {
				t.Fatalf("duplicate ID check: %v", err)
			}
		}
	}
}

func TestMessageValidateArguments(t *testing.T) {
	for _, tc := range []struct {
		arguments string
		valid     bool
	}{
		{`{}`, true}, {" \n { \"city\" : \"Beijing\" } \t", true},
		{`{"options":{"days":[1,2],"enabled":true,"extra":null}}`, true},
		{`{"id":9007199254740993,"decimal":1.234567890123456789,"exponent":1e1000}`, true},
		{"", false}, {" \n\t", false}, {`null`, false}, {`[]`, false}, {`"value"`, false},
		{`42`, false}, {`true`, false}, {`{"city":`, false}, {`{"city":{}`, false},
		{`{"city":"Beijing",}`, false}, {`{} {}`, false}, {`{} extra`, false},
	} {
		t.Run(tc.arguments, func(t *testing.T) {
			m := model.Message{Role: model.RoleAssistant, Parts: []model.Part{callPart("call", "weather", tc.arguments)}}
			before := bytes.Clone(m.Parts[0].ToolCall.Arguments)
			err := m.Validate()
			if (err == nil) != tc.valid || err != nil && !strings.HasPrefix(err.Error(), "parts[0].tool_call.arguments") {
				t.Fatalf("Validate() = %v, want valid=%t", err, tc.valid)
			}
			if !bytes.Equal(before, m.Parts[0].ToolCall.Arguments) {
				t.Fatal("validation changed argument bytes")
			}
		})
	}
}

func TestMessageValidateDoesNotMutate(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			m := model.Message{Role: model.RoleAssistant, Parts: []model.Part{textPart(" first "), callPart(" call ", " tool\t", " \n { \"id\" : 9007199254740993 } \t"), thinkingPart(""), textPart("")}}
			if invalid {
				m.Parts = append(m.Parts, callPart("", "tool", `{}`))
			}
			before, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			arguments := bytes.Clone(m.Parts[1].ToolCall.Arguments)
			if err := m.Validate(); (err != nil) != invalid {
				t.Fatalf("Validate() = %v", err)
			}
			after, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || !bytes.Equal(arguments, m.Parts[1].ToolCall.Arguments) {
				t.Fatal("validation mutated message")
			}
		})
	}
}

func TestMessageConversationJSONRoundTrip(t *testing.T) {
	want := []model.Message{
		{Role: model.RoleUser, Parts: []model.Part{textPart("Weather?")}},
		{Role: model.RoleAssistant, Parts: []model.Part{thinkingPart("Check both cities."), textPart("Checking."), callPart("call_1", "weather", `{"id":9007199254740993,"decimal":1.234567890123456789}`), textPart(""), thinkingPart(""), callPart("call_2", "weather", `{}`)}},
		{Role: model.RoleTool, Parts: []model.Part{resultPart("call_2", "unavailable", true), resultPart("call_1", "sunny", false)}},
		{Role: model.RoleAssistant, Parts: []model.Part{textPart("One result is available.")}},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got []model.Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed messages: %s", data)
	}
}
