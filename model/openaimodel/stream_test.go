package openaimodel

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

func TestThinkingAcrossStreamAndFinal(t *testing.T) {
	for _, tc := range []struct {
		name, partType, field, added, delta, done string
		kind                                      model.ThinkingKind
	}{
		{"text", "reasoning_text", "content", "response.content_part.added", "response.reasoning_text.delta", "response.reasoning_text.done", model.ThinkingText},
		{"summary", "summary_text", "summary", "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", model.ThinkingSummary},
	} {
		for _, mode := range []string{"final only", "delta and final", "done and final"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var received []model.Event
				s := responseStream{yield: func(e model.Event) bool {
					if err := model.ValidateEvent(e); err != nil {
						t.Fatal(err)
					}
					received = append(received, e)
					return true
				}}
				apply := func(event map[string]any) {
					t.Helper()
					data, err := json.Marshal(event)
					if err != nil {
						t.Fatal(err)
					}
					var e responses.ResponseStreamEventUnion
					if err := json.Unmarshal(data, &e); err != nil {
						t.Fatal(err)
					}
					if _, err := s.apply(e); err != nil {
						t.Fatal(err)
					}
				}
				if mode != "final only" {
					apply(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs_1"}})
					apply(map[string]any{"type": tc.added, "output_index": 0, "content_index": 0, "summary_index": 0, "item_id": "rs_1", "part": map[string]any{"type": tc.partType, "text": ""}})
					if part := s.snapshot().Parts[0].Thinking; part.Kind != tc.kind || part.Text != "" {
						t.Fatalf("empty snapshot lost kind: %+v", part)
					}
					apply(map[string]any{"type": tc.delta, "output_index": 0, "content_index": 0, "summary_index": 0, "item_id": "rs_1", "delta": "Check"})
					first := s.snapshot()
					if part := first.Parts[0].Thinking; part.Kind != tc.kind || part.Text != "Check" {
						t.Fatalf("partial snapshot lost kind: %+v", part)
					}
					first.Parts[0].Thinking.Kind = model.ThinkingUnknown
					if s.snapshot().Parts[0].Thinking.Kind != tc.kind {
						t.Fatal("snapshot aliases kind")
					}
					if mode == "done and final" {
						apply(map[string]any{"type": tc.done, "output_index": 0, "content_index": 0, "summary_index": 0, "item_id": "rs_1", "text": "Checking."})
					}
				}
				apply(map[string]any{"type": "response.completed", "response": map[string]any{
					"id": "resp_1", "status": "completed", "output": []map[string]any{{
						"id": "rs_1", "type": "reasoning", tc.field: []map[string]any{{"type": tc.partType, "text": "Checking."}},
					}},
				}})
				want := []model.Event{model.PartStart{Index: 0, Kind: model.PartThinking, ThinkingKind: tc.kind}}
				if mode == "final only" {
					want = append(want, model.ThinkingDelta{Index: 0, Delta: "Checking."})
				} else {
					want = append(want, model.ThinkingDelta{Index: 0, Delta: "Check"}, model.ThinkingDelta{Index: 0, Delta: "ing."})
				}
				want = append(want, model.PartEnd{Index: 0})
				if !reflect.DeepEqual(received[:len(received)-1], want) {
					t.Fatalf("events = %#v, want %#v before final result", received, want)
				}
				result := received[len(received)-1].(model.ResultEvent).Result
				part := *result.Message.Parts[0].Thinking
				if part.Kind != tc.kind || part.Text != "Checking." {
					t.Fatalf("final thinking: %+v", part)
				}
				if final := s.snapshot().Parts[0]; !final.Ended || final.Thinking.Kind != tc.kind {
					t.Fatalf("final snapshot lost kind: %+v", final)
				}
			})
		}
	}
}

func TestReasoningRejectsInvalidStream(t *testing.T) {
	item := `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning"}}`
	start := `{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"rs_1","part":{"type":"reasoning_text","text":""}}`
	delta := `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_1","delta":"prefix"}`
	done := `{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"item_id":"rs_1","text":"prefix"}`
	final := `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"rs_1","type":"reasoning","content":[{"type":"reasoning_text","text":"prefix"}]}]}}`
	for _, tc := range []struct {
		name    string
		setup   []string
		invalid string
	}{
		{"delta before start", []string{item}, delta},
		{"done before start", []string{item}, done},
		{"wrong item", []string{item, start}, strings.ReplaceAll(delta, "rs_1", "other")},
		{"wrong content index", []string{item, start}, strings.ReplaceAll(delta, `"content_index":0`, `"content_index":1`)},
		{"text on message", []string{`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"message","role":"assistant"}}`}, start},
		{"invalid initial text", []string{item}, strings.ReplaceAll(start, `"text":""`, `"text":123`)},
		{"invalid delta", []string{item, start}, strings.ReplaceAll(delta, `"delta":"prefix"`, `"delta":123`)},
		{"invalid done", []string{item, start}, strings.ReplaceAll(done, `"text":"prefix"`, `"text":null`)},
		{"delta after done", []string{item, start, delta, done}, delta},
		{"changed done prefix", []string{item, start, delta}, strings.ReplaceAll(done, "prefix", "changed")},
		{"changed final prefix", []string{item, start, delta}, strings.ReplaceAll(final, "prefix", "changed")},
		{"final omits content", []string{item, start, delta}, `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"rs_1","type":"reasoning","summary":[]}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := responseStream{}
			apply := func(raw string) error {
				t.Helper()
				var event responses.ResponseStreamEventUnion
				if err := json.Unmarshal([]byte(raw), &event); err != nil {
					t.Fatal(err)
				}
				_, err := s.apply(event)
				return err
			}
			for _, raw := range tc.setup {
				if err := apply(raw); err != nil {
					t.Fatal(err)
				}
			}
			if err := apply(tc.invalid); !errors.Is(err, model.ErrInvalidStream) {
				t.Fatalf("error = %v, want invalid stream", err)
			}
			if s.result != nil {
				t.Fatal("invalid reasoning produced a final result")
			}
		})
	}
}

func TestEncryptedReasoningField(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{"absent", "", ""},
		{"null", "null", ""},
		{"empty", `""`, ""},
		{"encrypted", `"private-token"`, "non-empty string, 13 bytes"},
		{"false", "false", "must be a string or null, got bool"},
		{"number", "123", "must be a string or null, got float64"},
		{"object", `{}`, "must be a string or null, got map[string]interface {}"},
		{"array", `[]`, "must be a string or null, got []interface {}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"id":"rs_1","type":"reasoning","content":[],"summary":[]`
			if tt.raw != "" {
				raw += `,"encrypted_content":` + tt.raw
			}
			raw += "}"
			var item responses.ResponseOutputItemUnion
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				t.Fatal(err)
			}
			s := responseStream{}
			err := s.item(0, item, true)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, model.ErrInvalidStream) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want invalid stream containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "private-token") {
				t.Fatal("error exposed encrypted content")
			}
		})
	}
}

func TestResponsesPartialSnapshotIndependence(t *testing.T) {
	s := responseStream{yield: func(model.Event) bool { return true }}
	for _, raw := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_1","part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":"prefix"}`,
	} {
		var event responses.ResponseStreamEventUnion
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if _, err := s.apply(event); err != nil {
			t.Fatal(err)
		}
	}
	first := s.snapshot()
	if err := s.append(s.blocks[0], " suffix"); err != nil {
		t.Fatal(err)
	}
	if *first.Parts[0].Text != "prefix" {
		t.Fatal("old snapshot changed")
	}
	*first.Parts[0].Text = "changed"
	if !strings.HasSuffix(*s.snapshot().Parts[0].Text, "suffix") || reflect.DeepEqual(first, s.snapshot()) {
		t.Fatal("snapshot aliases internal state")
	}
}

func TestSummarySnapshotIndependence(t *testing.T) {
	s := responseStream{
		yield: func(model.Event) bool { return true },
	}
	for _, raw := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":""}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"item_id":"rs_1","delta":"prefix"}`,
	} {
		var event responses.ResponseStreamEventUnion
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if _, err := s.apply(event); err != nil {
			t.Fatal(err)
		}
	}
	first := s.snapshot()
	first.Parts[0].Thinking.Kind = model.ThinkingUnknown
	if err := s.append(s.blocks[0], " suffix"); err != nil {
		t.Fatal(err)
	}
	second := s.snapshot()
	if first.Parts[0].Thinking.Text != "prefix" || second.Parts[0].Thinking.Text != "prefix suffix" ||
		second.Parts[0].Thinking.Kind != model.ThinkingSummary {
		t.Fatal("snapshot aliases reasoning state")
	}
}

func TestFinishPreservesValidationErrors(t *testing.T) {
	for _, arguments := range []string{`{"id":`, `[]`} {
		t.Run(arguments, func(t *testing.T) {
			ended := false
			s := responseStream{yield: func(event model.Event) bool {
				if _, ok := event.(model.PartEnd); ok {
					ended = true
				}
				return true
			}}
			b := &responseBlock{kind: model.PartToolCall, id: "call", name: "lookup"}
			err := s.finish(b, arguments, false)
			if !errors.Is(err, model.ErrInvalidStream) || !strings.Contains(err.Error(), "parts[0].tool_call.arguments:") {
				t.Fatalf("lost validation path or classification: %v", err)
			}
			if arguments == `[]` {
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
			if b.ended || ended {
				t.Fatal("invalid arguments ended the block")
			}
		})
	}
}

func TestIncompleteToolSnapshotIndependence(t *testing.T) {
	b := &responseBlock{kind: model.PartToolCall, id: "call", name: "lookup"}
	s := responseStream{blocks: []*responseBlock{b}}
	empty := s.snapshot()
	if empty.Parts[0].ToolCall.Arguments != "" || empty.Parts[0].Ended {
		t.Fatal("lost empty tool state")
	}
	if err := s.finish(b, `{"id":`, true); err != nil {
		t.Fatal(err)
	}
	first := s.snapshot()
	if first.Parts[0].ToolCall.Arguments != `{"id":` || first.Parts[0].Ended {
		t.Fatal("lost incomplete tool state")
	}
	if err := s.finish(b, `{"id":1}`, false); err != nil {
		t.Fatal(err)
	}
	second := s.snapshot()
	if first.Parts[0].ToolCall.Arguments != `{"id":` || first.Parts[0].Ended || empty.Parts[0].ToolCall.Arguments != "" {
		t.Fatal("completion mutated an earlier snapshot")
	}
	if !second.Parts[0].Ended || second.Parts[0].ToolCall.Arguments != `{"id":1}` {
		t.Fatal("lost completed tool state")
	}
	first.Parts[0].ToolCall.ID = "changed"
	first.Parts[0].ToolCall.Name = "changed"
	second.Parts[0].ToolCall.Arguments = "changed"
	next := s.snapshot()
	if next.Parts[0].ToolCall.ID != "call" || next.Parts[0].ToolCall.Name != "lookup" || next.Parts[0].ToolCall.Arguments != `{"id":1}` {
		t.Fatal("snapshot aliases tool state")
	}
}

func TestResponsesRejectInvalidBlockLifecycle(t *testing.T) {
	item := `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`
	start := `{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_1","part":{"type":"output_text","text":""}}`
	delta := `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":"prefix"}`
	done := `{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg_1","text":"prefix"}`
	for _, tc := range []struct {
		name    string
		setup   []string
		invalid string
	}{
		{"content before item", nil, start},
		{"delta before block", []string{item}, delta},
		{"duplicate item", []string{item, start, delta}, item},
		{"duplicate block", []string{item, start, delta}, start},
		{"wrong item ID", []string{item, start}, strings.ReplaceAll(delta, "msg_1", "other")},
		{"wrong block kind", []string{item, start}, strings.ReplaceAll(delta, "response.output_text.delta", "response.reasoning_summary_text.delta")},
		{"delta after end", []string{item, start, delta, done}, delta},
		{"changed completed prefix", []string{item, start, delta}, strings.ReplaceAll(done, "prefix", "changed")},
		{"final omits block", []string{item, start, delta}, `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[]}}`},
		{"duplicate tool call IDs", nil, `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"same","name":"lookup","arguments":"{}"},{"id":"fc_2","type":"function_call","call_id":"same","name":"lookup","arguments":"{}"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotResult := false
			s := responseStream{yield: func(event model.Event) bool {
				if _, ok := event.(model.ResultEvent); ok {
					gotResult = true
				}
				return true
			}}
			apply := func(raw string) error {
				t.Helper()
				var event responses.ResponseStreamEventUnion
				if err := json.Unmarshal([]byte(raw), &event); err != nil {
					t.Fatal(err)
				}
				_, err := s.apply(event)
				return err
			}
			for _, raw := range tc.setup {
				if err := apply(raw); err != nil {
					t.Fatal(err)
				}
			}
			if err := apply(tc.invalid); !errors.Is(err, model.ErrInvalidStream) {
				t.Fatalf("error = %v, want invalid stream", err)
			}
			if gotResult {
				t.Fatal("invalid stream produced a final result")
			}
		})
	}
}
