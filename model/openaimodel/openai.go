// Package openaimodel implements model.LLM using the OpenAI Responses API.
package openaimodel

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync/atomic"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

type openAIModel struct {
	client *openai.Client
	name   string
}

// NewModel validates the base configuration and creates a model without sending
// a request. The returned model supports independent concurrent calls. Options
// are applied in this order: SDK environment defaults, Config fields,
// Config.Options, then WithMaxRetries(0). SDK options use SDK validation.
// BaseURL changes the API root only; it does not switch to Chat Completions or
// detect the endpoint's capabilities.
func NewModel(cfg Config) (model.LLM, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithHTTPClient(cfg.HTTPClient),
	}
	opts = append(opts, cfg.Options...)
	opts = append(opts, option.WithMaxRetries(0))
	client := openai.NewClient(opts...)
	return &openAIModel{client: &client, name: cfg.Model}, nil
}

// Generate returns a lazy, single-use iterator. Each consumed iterator owns its
// context, HTTP request, and output buffers. stream selects SSE or ordinary JSON
// responses; ordinary responses emit only a ResultEvent. Thinking output contains
// visible reasoning text and summaries. Thinking history is unsupported.
// Tool results with IsError are encoded as JSON text containing is_error and
// content, since Responses has no error flag.
func (m *openAIModel) Generate(ctx context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
	var used atomic.Bool
	return func(yield func(model.Event, error) bool) {
		fail := func(err error) { yield(nil, &model.CallError{Cause: err}) }
		if !used.CompareAndSwap(false, true) {
			fail(fmt.Errorf("responses: iterator already consumed"))
			return
		}
		if ctx == nil {
			fail(fmt.Errorf("responses: context must not be nil"))
			return
		}
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		if m == nil || m.client == nil || m.name == "" {
			fail(fmt.Errorf("responses: model must be constructed with NewModel"))
			return
		}
		if err := req.Validate(); err != nil {
			fail(err)
			return
		}
		params, err := buildOpenAIParams(m.name, req)
		if err != nil {
			fail(err)
			return
		}
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		if stream {
			m.generateStream(callCtx, params, yield)
		} else {
			m.generateComplete(callCtx, params, yield)
		}
	}
}

func (m *openAIModel) generateComplete(ctx context.Context, params responses.ResponseNewParams, yield func(model.Event, error) bool) {
	r, err := m.client.Responses.New(ctx, params)
	if ctx.Err() != nil {
		err = errors.Join(ctx.Err(), err)
	}
	if err != nil {
		yield(nil, &model.CallError{Cause: err})
		return
	}
	if r == nil {
		yield(nil, &model.CallError{Cause: protocolError("missing response")})
		return
	}
	state := responseStream{}
	err = state.completeResponse(*r)
	if ctx.Err() != nil {
		err = errors.Join(ctx.Err(), err)
	}
	if err != nil {
		yield(nil, &model.CallError{Cause: err, Partial: state.snapshot()})
		return
	}
	yield(model.ResultEvent{Result: *state.result}, nil)
}

func (m *openAIModel) generateStream(callCtx context.Context, params responses.ResponseNewParams, yield func(model.Event, error) bool) {
	stream := m.client.Responses.NewStreaming(callCtx, params)
	defer stream.Close()
	consumerStopped := false
	state := responseStream{yield: func(event model.Event) bool {
		if callCtx.Err() != nil {
			return false
		}
		consumerStopped = !yield(event, nil)
		return !consumerStopped
	}}
	for stream.Next() {
		done, err := state.apply(stream.Current())
		if errors.Is(err, errConsumerStopped) {
			if !consumerStopped && callCtx.Err() != nil {
				yield(nil, &model.CallError{Cause: callCtx.Err(), Partial: state.snapshot()})
			}
			return
		}
		if err != nil {
			yield(nil, &model.CallError{Cause: err, Partial: state.snapshot()})
			return
		}
		if done {
			return
		}
	}
	err := stream.Err()
	if callCtx.Err() != nil {
		err = errors.Join(callCtx.Err(), err)
	}
	if err == nil {
		err = model.ErrIncompleteStream
	}
	yield(nil, &model.CallError{Cause: err, Partial: state.snapshot()})
}
