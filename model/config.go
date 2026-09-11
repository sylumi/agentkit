package model

import (
	"fmt"
	"strings"
)

// GenerateConfig controls one generation. Nil fields leave settings unspecified.
// Callers must keep the config and its nested data unchanged during consumption.
type GenerateConfig struct {
	// MaxOutputTokens requests an output-token upper limit, not a target length.
	// Nil leaves the limit unspecified; an explicit value must be positive.
	// Token accounting and supported limits depend on the provider and model.
	// The budget may include reasoning tokens, so it is not a visible-text budget.
	MaxOutputTokens *int64      `json:"max_output_tokens,omitempty"`
	ToolChoice      *ToolChoice `json:"tool_choice,omitempty"`
}

// ToolChoiceMode controls whether and which tools the model may call.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"     // The model decides whether to call tools.
	ToolChoiceNone     ToolChoiceMode = "none"     // No tool calls are allowed.
	ToolChoiceRequired ToolChoiceMode = "required" // One or more declared tools must be called.
	ToolChoiceNamed    ToolChoiceMode = "named"    // The named tool must be called.
)

// ToolChoice selects a tool policy. Name is required only for ToolChoiceNamed.
// An explicit choice requires a valid Mode; omit the choice to use provider defaults.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode"`
	Name string         `json:"name,omitempty"`
}

// Validate checks local settings without modifying them. Request.Validate checks
// that the selected policy is compatible with the request's tool declarations.
func (c GenerateConfig) Validate() error {
	if c.MaxOutputTokens != nil && *c.MaxOutputTokens <= 0 {
		return fmt.Errorf("max_output_tokens: must be positive")
	}
	if c.ToolChoice == nil {
		return nil
	}
	choice := c.ToolChoice
	switch choice.Mode {
	case ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
		if choice.Name != "" {
			return fmt.Errorf("tool_choice.name: only allowed for mode %q", ToolChoiceNamed)
		}
	case ToolChoiceNamed:
		if strings.TrimSpace(choice.Name) == "" {
			return fmt.Errorf("tool_choice.name: a non-blank name is required")
		}
	default:
		return fmt.Errorf("tool_choice.mode: unknown mode %q", choice.Mode)
	}
	return nil
}
