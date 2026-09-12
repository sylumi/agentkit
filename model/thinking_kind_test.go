package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestThinkingKindJSONRoundTrip(t *testing.T) {
	for _, kind := range []ThinkingKind{ThinkingUnknown, ThinkingText, ThinkingSummary} {
		t.Run(string(kind), func(t *testing.T) {
			part := ThinkingPart{Kind: kind, Text: "Checking."}
			req := Request{Messages: []Message{{Role: RoleAssistant, Parts: []Part{{Kind: PartThinking, Thinking: &part}}}}}
			if err := req.Validate(); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Request
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(req, decoded) {
				t.Fatalf("history changed: %s", data)
			}
			result := Result{StopReason: StopReasonStop, Message: &req.Messages[0]}
			data, err = json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var restored Result
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result, restored) {
				t.Fatalf("result changed: %s", data)
			}
		})
	}
}

func TestLegacyThinkingKindStaysUnknown(t *testing.T) {
	for _, raw := range []string{
		`{"role":"assistant","parts":[{"kind":"thinking","thinking":{"text":"old"}}]}`,
		`{"role":"assistant","parts":[{"kind":"thinking","thinking":{"text":"old","replay":{"provider":"deepseek","model":"test","format":"responses.reasoning_text","id":"rs_1"}}}]}`,
	} {
		var message Message
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		part := *message.Parts[0].Thinking
		if part.Kind != ThinkingUnknown {
			t.Fatalf("guessed kind from legacy data: %q", part.Kind)
		}
		data, err := json.Marshal(message)
		if err != nil || string(data) != `{"role":"assistant","parts":[{"kind":"thinking","thinking":{"text":"old"}}]}` {
			t.Fatalf("legacy content changed or retired replay retained: %s, %v", data, err)
		}
	}
}

func TestThinkingKindValidation(t *testing.T) {
	message := &Message{Role: RoleAssistant, Parts: []Part{{Kind: PartThinking, Thinking: &ThinkingPart{Kind: "invalid"}}}}
	if err := message.Validate(); err == nil || !strings.Contains(err.Error(), "thinking.kind") {
		t.Fatalf("invalid message kind: %v", err)
	}
	for _, event := range []Event{
		PartStart{Kind: PartThinking, ThinkingKind: "invalid"},
		PartStart{Kind: PartText, ThinkingKind: ThinkingSummary},
		PartStart{Kind: PartToolCall, ThinkingKind: ThinkingText},
		ResultEvent{Result: Result{Message: message, StopReason: StopReasonStop}},
	} {
		if err := ValidateEvent(event); err == nil {
			t.Fatalf("accepted invalid event: %+v", event)
		}
	}
}

func TestThinkingKindInPartialOutputJSON(t *testing.T) {
	for _, kind := range []ThinkingKind{ThinkingUnknown, ThinkingText, ThinkingSummary} {
		t.Run(string(kind), func(t *testing.T) {
			partial := PartialOutput{Parts: []PartialPart{{
				Kind: PartThinking, Thinking: &ThinkingPart{Kind: kind, Text: "Checking."},
			}}}
			data, err := json.Marshal(partial)
			if err != nil {
				t.Fatal(err)
			}
			var restored PartialOutput
			if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(restored, partial) {
				t.Fatalf("partial round trip: %s, %v", data, err)
			}
		})
	}
}
