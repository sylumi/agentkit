// Package llmagent provides an agent that calls a language model.
package llmagent

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/tool"
)

const defaultMaxModelCalls = 10

// Config describes an LLM agent.
type Config struct {
	Name        string
	Description string
	Model       model.LLM
	Instruction string

	Tools []tool.Tool
	// Toolsets are discovered once per Run, after Tools. Names must be unique
	// across all sources.
	Toolsets []tool.Toolset
	// GenerateConfig and its nested data must remain unchanged during Run.
	GenerateConfig *model.GenerateConfig
	// MaxModelCalls limits model calls per Run; zero defaults to 10.
	MaxModelCalls int
}

type llmAgent struct {
	name           string
	description    string
	model          model.LLM
	instruction    string
	tools          []tool.Tool
	toolsets       []tool.Toolset
	generateConfig *model.GenerateConfig
	maxModelCalls  int
}

// New creates an agent without calling the model.
func New(cfg Config) (agent.Agent, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("llmagent: name is required")
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("llmagent: model is required")
	}
	if cfg.MaxModelCalls < 0 {
		return nil, fmt.Errorf("llmagent: max model calls must not be negative")
	}
	if cfg.MaxModelCalls == 0 {
		cfg.MaxModelCalls = defaultMaxModelCalls
	}
	return &llmAgent{
		name:           cfg.Name,
		description:    cfg.Description,
		model:          cfg.Model,
		instruction:    cfg.Instruction,
		tools:          slices.Clone(cfg.Tools),
		toolsets:       slices.Clone(cfg.Toolsets),
		generateConfig: cfg.GenerateConfig,
		maxModelCalls:  cfg.MaxModelCalls,
	}, nil
}

func (a *llmAgent) Name() string        { return a.name }
func (a *llmAgent) Description() string { return a.description }

// Run yields model and tool events until the model finishes or the call limit is reached.
func (a *llmAgent) Run(ctx context.Context, invocation *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		definitions, toolsByName, err := registerTools(ctx, a.tools, a.toolsets)
		if err != nil {
			yield(nil, err)
			return
		}
		for calls := 0; ; calls++ {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if calls >= a.maxModelCalls {
				yield(nil, fmt.Errorf("llmagent: model call limit reached (%d)", a.maxModelCalls))
				return
			}
			var complete *session.Event
			for event, err := range a.generate(ctx, invocation, definitions) {
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(event, nil) {
					return
				}
				if !event.Partial {
					complete = event
				}
			}
			if complete == nil || complete.StopReason != model.StopReasonToolCalls {
				return
			}
			for _, part := range complete.Message.Parts {
				if part.Kind != model.PartToolCall {
					continue
				}
				result, err := executeTool(ctx, toolsByName, part.ToolCall)
				if err != nil {
					yield(nil, err)
					return
				}
				toolEvent := session.NewEvent(invocation.InvocationID)
				toolEvent.Author = a.name
				toolEvent.Message = &model.Message{
					Role:  model.RoleTool,
					Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: result}},
				}
				if !yield(toolEvent, nil) {
					return
				}
			}
		}
	}
}

func (a *llmAgent) generate(ctx context.Context, invocation *agent.InvocationContext, definitions []model.ToolDefinition) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		req := model.Request{
			Instructions: a.instruction,
			Tools:        definitions,
			Config:       a.generateConfig,
		}
		for event := range invocation.Session.Events().All() {
			if event.Message != nil {
				req.Messages = append(req.Messages, *event.Message)
			}
		}
		if err := a.processRequest(ctx, &req); err != nil {
			yield(nil, err)
			return
		}
		event := session.NewEvent(invocation.InvocationID)
		event.Author = a.name
		var generationErr error
		finished := false
		for output, err := range a.model.Generate(ctx, req, invocation.Streaming) {
			if err != nil {
				generationErr = err
				break
			}
			if err := ctx.Err(); err != nil {
				generationErr = err
				break
			}
			if err := model.ValidateEvent(output); err != nil {
				generationErr = fmt.Errorf("llmagent: model event: %w", err)
				break
			}
			complete, ok := output.(model.ResultEvent)
			if !ok {
				if invocation.Streaming {
					partial := *event
					partial.Partial = true
					partial.Delta = output
					if !yield(&partial, nil) {
						return
					}
				}
				continue
			}
			result := complete.Result
			event.Message = result.Message
			event.StopReason = result.StopReason
			event.Usage = &result.Usage
			event.Metadata = result.Metadata
			finished = true
			break
		}
		// Close the model iterator before handing off a final outcome or error.
		if generationErr != nil {
			yield(nil, generationErr)
			return
		}
		if !finished {
			yield(nil, fmt.Errorf("llmagent: model returned no result"))
			return
		}
		yield(event, nil)
	}
}
