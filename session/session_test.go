package session_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

func userEvent(text string) *session.Event {
	event := session.NewEvent("invocation")
	event.Author = "user"
	event.Message = &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(text)}}
	return event
}

func toolCallMessage() *model.Message {
	return &model.Message{Role: model.RoleAssistant, Parts: []model.Part{{
		Kind: model.PartToolCall,
		ToolCall: &model.ToolCallPart{
			ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{ "id": 9007199254740993 }`),
		},
	}}}
}

func TestAppendEventPreservesPayloadWithoutValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event *session.Event
	}{
		{"empty record", &session.Event{}},
		{"message without parts", &session.Event{Message: &model.Message{Role: model.RoleUser}}},
		{"metadata without generation", &session.Event{Metadata: &model.ResponseMetadata{ResponseID: "response"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, view := createSession(t, "payload")
			if err := service.AppendEvent(t.Context(), view, tc.event); err != nil {
				t.Fatal(err)
			}
			if got := getSession(t, service, view).Events().At(0); !reflect.DeepEqual(got, tc.event) {
				t.Fatalf("event changed: got %+v, want %+v", got, tc.event)
			}
		})
	}
}

func TestEventJSONAndAppendPreserveContent(t *testing.T) {
	service, view := createSession(t, "json")
	event := userEvent("hello")
	event.Author = "assistant"
	event.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("answer")}}
	event.StopReason = model.StopReasonStop
	count := int64(0)
	event.Usage = &model.Usage{InputTokens: &count}
	event.Metadata = &model.ResponseMetadata{Provider: "test", ResponseID: "provider-response"}

	before, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AppendEvent(t.Context(), view, event); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(event)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("append changed event: %s, %v", after, err)
	}
	var decoded session.Event
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, &decoded) {
		t.Fatalf("event changed during JSON round trip: %+v", decoded)
	}
	if event.ID == event.Metadata.ResponseID || event.ID == session.NewEvent("invocation").ID {
		t.Fatal("event ID was reused")
	}
}
