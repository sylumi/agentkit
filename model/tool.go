package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolDefinition describes a tool available to a model without its execution code.
// InputSchema must be a JSON object with an explicit root type of "object".
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Validate checks the declaration's name and schema root without modifying data.
// It does not compile JSON Schema or validate any tool invocation's arguments.
func (t ToolDefinition) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("name: a non-blank name is required")
	}

	schema, err := decodeJSONObject(t.InputSchema)
	if err != nil {
		return fmt.Errorf("input_schema: %w", err)
	}

	rawType, exists := schema["type"]
	if !exists {
		return fmt.Errorf("input_schema.type: an explicit root type is required")
	}
	var schemaType string
	if err := json.Unmarshal(rawType, &schemaType); err != nil {
		return fmt.Errorf("input_schema.type: expected string \"object\": %w", err)
	}
	if schemaType != "object" {
		return fmt.Errorf("input_schema.type: expected string \"object\"")
	}
	return nil
}
