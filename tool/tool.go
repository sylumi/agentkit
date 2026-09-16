// Package tool defines executable tools available to an agent.
package tool

import (
	"context"
	"encoding/json"

	"github.com/sylumi/agentkit/model"
)

// Tool provides a model-facing declaration and its implementation.
type Tool interface {
	// Definition returns the tool's name, description, and input schema.
	Definition() model.ToolDefinition

	// Execute parses and checks arguments, performs the operation, and returns
	// content for the model. Content may be plain text or a JSON string.
	// Implementations must honor ctx cancellation and leave arguments unchanged.
	// The caller associates the result with a call ID and handles errors.
	Execute(ctx context.Context, arguments json.RawMessage) (string, error)
}
