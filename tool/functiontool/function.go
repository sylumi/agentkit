// Package functiontool wraps typed Go functions as tools.
package functiontool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool"
)

// Config describes a function tool.
type Config struct {
	Name        string
	Description string
	// InputSchema overrides the schema inferred from Args when non-nil.
	// It must describe a JSON object compatible with Args.
	InputSchema json.RawMessage
}

type functionTool[Args, Result any] struct {
	definition model.ToolDefinition
	schema     *jsonschema.Resolved
	handler    func(context.Context, Args) (Result, error)
}

// New wraps handler as a tool. By default, Args must infer to an object schema,
// such as a struct or a map. Struct fields use json tags for names and optional
// fields, and jsonschema tags for descriptions. Unknown struct fields are rejected.
// Results are JSON-encoded, including string results. Handler errors are preserved.
func New[Args, Result any](cfg Config, handler func(context.Context, Args) (Result, error)) (tool.Tool, error) {
	if handler == nil {
		return nil, fmt.Errorf("functiontool: handler must not be nil")
	}
	inputSchema := bytes.Clone(cfg.InputSchema)
	if inputSchema == nil {
		schema, err := jsonschema.For[Args](nil)
		if err != nil {
			return nil, fmt.Errorf("functiontool: infer input schema: %w", err)
		}
		inputSchema, err = json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("functiontool: encode input schema: %w", err)
		}
	}
	definition := model.ToolDefinition{
		Name: cfg.Name, Description: cfg.Description, InputSchema: inputSchema,
	}
	if err := definition.Validate(); err != nil {
		return nil, fmt.Errorf("functiontool: definition: %w", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(inputSchema, &schema); err != nil {
		return nil, fmt.Errorf("functiontool: input schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("functiontool: resolve input schema: %w", err)
	}
	return &functionTool[Args, Result]{definition: definition, schema: resolved, handler: handler}, nil
}

func (f *functionTool[Args, Result]) Definition() model.ToolDefinition {
	definition := f.definition
	definition.InputSchema = bytes.Clone(definition.InputSchema)
	return definition
}

func (f *functionTool[Args, Result]) Execute(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Validate the JSON value before decoding into Args, so missing properties
	// cannot disappear into Go zero values.
	var value any
	if err := json.Unmarshal(arguments, &value); err != nil {
		return "", fmt.Errorf("functiontool: decode arguments: %w", err)
	}
	if err := f.schema.Validate(value); err != nil {
		return "", fmt.Errorf("functiontool: validate arguments: %w", err)
	}
	// Decode from the original JSON to preserve typed integer values.
	var input Args
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("functiontool: decode arguments: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	output, err := f.handler(ctx, input)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("functiontool: encode result: %w", err)
	}
	return string(data), nil
}
