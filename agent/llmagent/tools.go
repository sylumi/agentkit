package llmagent

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool"
)

func registerTools(ctx context.Context, static []tool.Tool, toolsets []tool.Toolset) ([]model.ToolDefinition, map[string]tool.Tool, error) {
	tools := slices.Clone(static)
	for i, ts := range toolsets {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if ts == nil {
			return nil, nil, fmt.Errorf("llmagent: toolsets[%d] must not be nil", i)
		}
		discovered, err := ts.Tools(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("llmagent: discover tools from toolset %q: %w", ts.Name(), err)
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for j, t := range discovered {
			if t == nil {
				return nil, nil, fmt.Errorf("llmagent: toolset %q: tools[%d] must not be nil", ts.Name(), j)
			}
		}
		tools = append(tools, discovered...)
	}
	var definitions []model.ToolDefinition
	byName := make(map[string]tool.Tool, len(tools))
	for i, t := range tools {
		if t == nil {
			return nil, nil, fmt.Errorf("llmagent: tools[%d] must not be nil", i)
		}
		definition := t.Definition()
		if _, exists := byName[definition.Name]; exists {
			return nil, nil, fmt.Errorf("llmagent: duplicate tool name %q", definition.Name)
		}
		definitions = append(definitions, definition)
		byName[definition.Name] = t
	}
	return definitions, byName, nil
}

func (a *llmAgent) processRequest(ctx context.Context, req *model.Request) error {
	for _, ts := range a.toolsets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if processor, ok := ts.(tool.RequestProcessor); ok {
			if err := processor.ProcessRequest(ctx, req); err != nil {
				return fmt.Errorf("llmagent: process request by toolset %q: %w", ts.Name(), err)
			}
		}
	}
	return ctx.Err()
}

func executeTool(ctx context.Context, toolsByName map[string]tool.Tool, call *model.ToolCallPart) (*model.ToolResultPart, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &model.ToolResultPart{CallID: call.ID}
	t, ok := toolsByName[call.Name]
	if !ok {
		result.Content = fmt.Sprintf("llmagent: unknown tool %q", call.Name)
		result.IsError = true
		return result, nil
	}
	content, err := t.Execute(ctx, call.Arguments)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		result.Content = err.Error()
		result.IsError = true
	} else {
		result.Content = content
	}
	return result, nil
}
