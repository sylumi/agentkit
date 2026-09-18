// Package llmagent provides an agent that calls a language model.
package llmagent

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

// Config describes an LLM agent.
type Config struct {
	Name        string
	Description string
	Model       model.LLM
	Instruction string
}

type llmAgent struct {
	name        string
	description string
	model       model.LLM
	instruction string
}

// New creates an agent without calling the model.
func New(cfg Config) (agent.Agent, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("llmagent: name is required")
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("llmagent: model is required")
	}
	return &llmAgent{
		name:        cfg.Name,
		description: cfg.Description,
		model:       cfg.Model,
		instruction: cfg.Instruction,
	}, nil
}

func (a *llmAgent) Name() string        { return a.name }
func (a *llmAgent) Description() string { return a.description }

// Run calls the model once and yields its complete result without saving it.
func (a *llmAgent) Run(ctx context.Context, invocation *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		req := model.Request{Instructions: a.instruction}
		for event := range invocation.Session.Events().All() {
			if event.Message != nil {
				req.Messages = append(req.Messages, *event.Message)
			}
		}
		for output, err := range a.model.Generate(ctx, req, false) {
			if err != nil {
				yield(nil, err)
				return
			}
			result := output.(model.ResultEvent).Result
			event := session.NewEvent(invocation.InvocationID)
			event.Author = a.name
			event.Message = result.Message
			event.StopReason = result.StopReason
			event.Usage = &result.Usage
			event.Metadata = result.Metadata
			yield(event, nil)
			return
		}
	}
}
