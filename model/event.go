package model

import "fmt"

// Event is an in-process generation event. Only the six concrete value types
// below are supported; pointers and external wrappers are rejected by ValidateEvent.
// Producers must not modify an event or its nested data after delivery.
// Event does not define a JSON wire format; decode into a concrete type only
// when a separate transport protocol identifies that type.
type Event interface {
	isEvent()
}

// PartStart opens a text, thinking, or tool-call block with no initial content.
// Index is allocated consecutively from zero within one generation stream.
// It identifies a stream block, not necessarily its position in Result.Message;
// the final message follows provider output order even when blocks interleave.
// ThinkingKind applies only to thinking blocks and remains fixed for the block.
// Its zero value means unknown, for compatibility with older producers.
type PartStart struct {
	Index        int          `json:"index"`
	Kind         PartKind     `json:"kind"`
	ThinkingKind ThinkingKind `json:"thinking_kind,omitempty"`
}

// TextDelta appends non-empty text to the block identified by Index.
// An explicit empty text block is represented by start and end without a delta.
type TextDelta struct {
	Index int    `json:"index"`
	Delta string `json:"delta"`
}

// ThinkingDelta appends non-empty visible reasoning text to a thinking block.
// Provider-native signatures and encrypted data are not visible reasoning text.
// The block's PartStart.ThinkingKind distinguishes text from a summary.
type ThinkingDelta struct {
	Index int    `json:"index"`
	Delta string `json:"delta"`
}

// ToolCallDelta appends each non-empty field to a tool-call block.
// ID and Name may also arrive in fragments. Arguments need not be valid JSON.
type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// PartEnd marks a block whose accumulated data forms a complete assistant Part.
// It does not confirm generation completion or authorize tool execution.
type PartEnd struct {
	Index int `json:"index"`
}

// ResultEvent carries the authoritative final message and usage, even without
// prior deltas. Nested data must remain unchanged after delivery.
type ResultEvent struct {
	Result Result
}

func (PartStart) isEvent()     {}
func (TextDelta) isEvent()     {}
func (ThinkingDelta) isEvent() {}
func (ToolCallDelta) isEvent() {}
func (PartEnd) isEvent()       {}
func (ResultEvent) isEvent()   {}

// ValidateEvent checks the concrete event type and local field constraints.
// It accepts only the six event value types, rejecting nil, all pointers
// (including typed nil), and external wrappers without calling their methods.
// It is read-only and preserves nested error paths and causes. It does not
// check event ordering or accumulated content.
func ValidateEvent(event Event) error {
	switch e := event.(type) {
	case PartStart:
		if e.Index < 0 {
			return fmt.Errorf("part_start.index: must be non-negative")
		}
		switch e.Kind {
		case PartText, PartThinking, PartToolCall:
		default:
			return fmt.Errorf("part_start.kind: expected text, thinking, or tool_call, got %q", e.Kind)
		}
		if e.Kind == PartThinking {
			if err := e.ThinkingKind.validate(); err != nil {
				return fmt.Errorf("part_start.thinking_kind: %w", err)
			}
		} else if e.ThinkingKind != ThinkingUnknown {
			return fmt.Errorf("part_start.thinking_kind: only allowed on thinking blocks")
		}
	case TextDelta:
		if e.Index < 0 {
			return fmt.Errorf("text_delta.index: must be non-negative")
		}
		if e.Delta == "" {
			return fmt.Errorf("text_delta.delta: a non-empty delta is required")
		}
	case ThinkingDelta:
		if e.Index < 0 {
			return fmt.Errorf("thinking_delta.index: must be non-negative")
		}
		if e.Delta == "" {
			return fmt.Errorf("thinking_delta.delta: a non-empty delta is required")
		}
	case ToolCallDelta:
		if e.Index < 0 {
			return fmt.Errorf("tool_call_delta.index: must be non-negative")
		}
		if e.ID == "" && e.Name == "" && e.Arguments == "" {
			return fmt.Errorf("tool_call_delta: at least one non-empty field is required")
		}
	case PartEnd:
		if e.Index < 0 {
			return fmt.Errorf("part_end.index: must be non-negative")
		}
	case ResultEvent:
		if err := e.Result.Validate(); err != nil {
			return fmt.Errorf("result.%w", err)
		}
	default:
		return fmt.Errorf("event: unsupported event type %T", event)
	}
	return nil
}
