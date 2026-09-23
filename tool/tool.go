// Package tool defines executable tools and tool collections.
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

// Toolset groups tools and must support concurrent runs.
type Toolset interface {
	// Name identifies the collection without renaming its tools.
	Name() string

	// Tools discovers tools once per LLM agent Run and honors cancellation.
	// The caller owns the slice; tool instances may be shared.
	Tools(ctx context.Context) ([]Tool, error)
}

// RequestProcessor lets a Toolset enrich each fresh request before generation.
// Processors run in configured order, even for empty toolsets; errors stop the run.
type RequestProcessor interface {
	// ProcessRequest honors cancellation and supports concurrent calls.
	// Do not retain req or add or rename tools.
	// Copy shared history, config, or tool definitions before modifying them.
	ProcessRequest(ctx context.Context, req *model.Request) error
}
