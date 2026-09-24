package session_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

func TestEventJSONPreservesContent(t *testing.T) {
	event := session.NewEvent("invocation")
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

func TestPartialEventsRoundTrip(t *testing.T) {
	for _, delta := range []model.Event{
		model.PartStart{Index: 0, Kind: model.PartThinking, ThinkingKind: model.ThinkingSummary},
		model.ThinkingDelta{Index: 0, Delta: "Checking"},
		model.TextDelta{Index: 1, Delta: "Hello"},
		model.ToolCallDelta{Index: 2, ID: "call", Name: "lookup", Arguments: `{"city":`},
		model.PartEnd{Index: 0},
	} {
		event := session.NewEvent("run")
		event.Author, event.Partial, event.Delta = "agent", true, delta
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded session.Event
		if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(event, &decoded) {
			t.Fatalf("partial event changed during JSON round trip: %s, %v", data, err)
		}
	}
}
