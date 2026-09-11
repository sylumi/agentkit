// Package model defines provider-independent messages for model interactions.
package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Role identifies a message's role in the conversation, not its permissions.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartKind identifies content in stream starts, partial output, and JSON.
type PartKind string

const (
	PartText       PartKind = "text"
	PartThinking   PartKind = "thinking"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
)

// Message contains ordered, complete content from a user, assistant, or tool.
// Generation metadata belongs to Result; incomplete content to PartialOutput.
// Standard JSON decoding does not validate messages. Decode into a fresh value,
// then call Validate before use. Nested data must remain unchanged during a call.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one complete content block. Exactly one payload must be non-nil and
// match Kind. Text may point to an empty string. Validate checks these rules;
// standard JSON encoding and decoding do not. Nested payloads are not immutable.
type Part struct {
	Kind       PartKind        `json:"kind"`
	Text       *string         `json:"text,omitempty"`
	Thinking   *ThinkingPart   `json:"thinking,omitempty"`
	ToolCall   *ToolCallPart   `json:"tool_call,omitempty"`
	ToolResult *ToolResultPart `json:"tool_result,omitempty"`
}

// NewTextPart constructs a text block, preserving an explicitly empty string.
func NewTextPart(text string) Part {
	return Part{Kind: PartText, Text: &text}
}

// ThinkingKind distinguishes visible reasoning text from a reasoning summary.
type ThinkingKind string

const (
	// ThinkingUnknown preserves legacy content without guessing its semantics.
	ThinkingUnknown ThinkingKind = ""
	ThinkingText    ThinkingKind = "text"
	ThinkingSummary ThinkingKind = "summary"
)

func (k ThinkingKind) validate() error {
	switch k {
	case ThinkingUnknown, ThinkingText, ThinkingSummary:
		return nil
	default:
		return fmt.Errorf("unknown thinking kind %q", k)
	}
}

// ThinkingPart contains visible reasoning text or a summary returned by a model.
// Text may be empty.
// It does not represent the model's complete internal reasoning or imply that
// an empty block was redacted. Thinking is separate from answer text.
// Kind is unknown for legacy content; new adapters should specify it explicitly.
type ThinkingPart struct {
	Kind ThinkingKind `json:"kind,omitempty"`
	Text string       `json:"text"`
}

// ToolCallPart requests a tool invocation with a complete JSON object as Arguments.
// ID is opaque and unique within its message, not necessarily across a session.
type ToolCallPart struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResultPart responds to a tool call identified by CallID.
// Content may be empty. IsError reports execution failure, not invalid structure.
type ToolResultPart struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Validate checks role, complete parts, and unique call IDs within the message.
// It is read-only and does not check conversation associations, tool schemas,
// provider compatibility, or generation success. Error wording is not stable API.
func (m Message) Validate() error {
	switch m.Role {
	case RoleUser, RoleAssistant, RoleTool:
	default:
		return fmt.Errorf("role: unknown role %q", m.Role)
	}
	if len(m.Parts) == 0 {
		return fmt.Errorf("parts: at least one content block is required")
	}

	seenIDs := make(map[string]struct{})
	for i, part := range m.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("parts[%d].%w", i, err)
		}
		if m.Role == RoleUser && part.Kind != PartText ||
			m.Role == RoleAssistant && part.Kind == PartToolResult ||
			m.Role == RoleTool && part.Kind != PartToolResult {
			return fmt.Errorf("parts[%d]: kind %q is not allowed for role %q", i, part.Kind, m.Role)
		}
		var id, field string
		switch part.Kind {
		case PartToolCall:
			id, field = part.ToolCall.ID, "tool_call.id"
		case PartToolResult:
			id, field = part.ToolResult.CallID, "tool_result.call_id"
		}
		if id != "" {
			if _, exists := seenIDs[id]; exists {
				return fmt.Errorf("parts[%d].%s: duplicate ID %q in this message", i, field, id)
			}
			seenIDs[id] = struct{}{}
		}
	}
	return nil
}

// Validate checks the tag, payload exclusivity, and complete field values.
// It does not check role placement or validate arguments against a tool schema.
func (p Part) Validate() error {
	payloads := 0
	for _, present := range []bool{p.Text != nil, p.Thinking != nil, p.ToolCall != nil, p.ToolResult != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("payload: expected exactly one content payload")
	}
	switch p.Kind {
	case PartText:
		if p.Text != nil {
			return nil
		}
	case PartThinking:
		if p.Thinking == nil {
			break
		}
		if err := p.Thinking.Kind.validate(); err != nil {
			return fmt.Errorf("thinking.kind: %w", err)
		}
		return nil
	case PartToolCall:
		if p.ToolCall == nil {
			break
		}
		if strings.TrimSpace(p.ToolCall.ID) == "" {
			return fmt.Errorf("tool_call.id: a non-blank ID is required")
		}
		if strings.TrimSpace(p.ToolCall.Name) == "" {
			return fmt.Errorf("tool_call.name: a non-blank name is required")
		}
		if _, err := decodeJSONObject(p.ToolCall.Arguments); err != nil {
			return fmt.Errorf("tool_call.arguments: %w", err)
		}
		return nil
	case PartToolResult:
		if p.ToolResult == nil {
			break
		}
		if strings.TrimSpace(p.ToolResult.CallID) == "" {
			return fmt.Errorf("tool_result.call_id: a non-blank call ID is required")
		}
		return nil
	default:
		return fmt.Errorf("kind: unknown kind %q", p.Kind)
	}
	return fmt.Errorf("payload: does not match kind %q", p.Kind)
}
