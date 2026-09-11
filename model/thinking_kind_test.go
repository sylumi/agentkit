package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
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

func TestThinkingKindInCompleteAndFailureSnapshots(t *testing.T) {
	for _, kind := range []ThinkingKind{ThinkingUnknown, ThinkingText, ThinkingSummary} {
		for _, completed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/completed=%t", kind, completed), func(t *testing.T) {
				m := generateFunc(func(context.Context, Request, bool) iter.Seq2[Event, error] {
					return func(yield func(Event, error) bool) {
						part := ThinkingPart{Kind: kind, Text: "Checking."}
						if completed {
							yield(ResultEvent{Result: Result{StopReason: StopReasonStop, Message: &Message{Role: RoleAssistant, Parts: []Part{{Kind: PartThinking, Thinking: &part}}}}}, nil)
						} else {
							yield(nil, &CallError{Cause: ErrIncompleteStream, Partial: PartialOutput{Parts: []PartialPart{{Kind: PartThinking, Thinking: &part}}}})
						}
					}
				})
				result, err := Complete(context.Background(), m, Request{Instructions: "hello"})
				if completed {
					if err != nil || result.Message.Parts[0].Thinking.Kind != kind {
						t.Fatalf("result=%v, error=%v", result, err)
					}
					return
				}
				var callErr *CallError
				if !errors.Is(err, ErrIncompleteStream) || !errors.As(err, &callErr) {
					t.Fatalf("error=%v", err)
				}
				part := callErr.Partial.Parts[0].Thinking
				if part.Kind != kind || part.Text != "Checking." {
					t.Fatalf("lost thinking in failure: %+v", part)
				}
				data, err := json.Marshal(callErr.Partial)
				if err != nil {
					t.Fatal(err)
				}
				var restored PartialOutput
				if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(restored, callErr.Partial) {
					t.Fatalf("partial round trip: %s, %v", data, err)
				}
			})
		}
	}
}
