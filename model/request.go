package model

import (
	"fmt"
	"strings"
)

// Request contains input for one model generation.
// Instructions, Tools, and Config apply to this call only; no values are inherited.
// Its slices and nested payloads must remain unchanged while being consumed.
// Decode JSON into a fresh Request, then Validate before use; standard decoding
// neither validates input nor guarantees an unchanged receiver on failure.
type Request struct {
	Instructions string           `json:"instructions,omitempty"`
	Messages     []Message        `json:"messages,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
	Config       *GenerateConfig  `json:"config,omitempty"`
}

// Validate checks input presence, messages, tools, and generation config in order.
// It is read-only and returns the first error with its field path and cause.
// It does not check conversation associations, compile schemas, or execute tools.
func (r Request) Validate() error {
	if len(r.Messages) == 0 && strings.TrimSpace(r.Instructions) == "" {
		return fmt.Errorf("request: messages or non-blank instructions are required")
	}
	for i, message := range r.Messages {
		if err := message.Validate(); err != nil {
			return fmt.Errorf("messages[%d].%w", i, err)
		}
	}

	seenNames := make(map[string]struct{})
	for i, tool := range r.Tools {
		if err := tool.Validate(); err != nil {
			return fmt.Errorf("tools[%d].%w", i, err)
		}
		if _, exists := seenNames[tool.Name]; exists {
			return fmt.Errorf("tools[%d].name: duplicate tool name %q", i, tool.Name)
		}
		seenNames[tool.Name] = struct{}{}
	}
	if r.Config != nil {
		if err := r.Config.Validate(); err != nil {
			return fmt.Errorf("config.%w", err)
		}
		if choice := r.Config.ToolChoice; choice != nil {
			switch choice.Mode {
			case ToolChoiceRequired:
				if len(r.Tools) == 0 {
					return fmt.Errorf("config.tool_choice.mode: required mode needs at least one declared tool")
				}
			case ToolChoiceNamed:
				if _, exists := seenNames[choice.Name]; !exists {
					return fmt.Errorf("config.tool_choice.name: tool %q is not declared", choice.Name)
				}
			}
		}
	}
	return nil
}
