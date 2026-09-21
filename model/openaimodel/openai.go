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
	info   model.ModelInfo
}

// NewModel validates the base configuration and creates a model without sending
// a request. The returned model supports independent concurrent calls. Options
// are applied in this order: SDK environment defaults, Config.Options,
// Config.APIKey, a non-nil Config.HTTPClient, Config.BaseURL, and WithMaxRetries(0).
// Options use SDK validation.
// BaseURL changes the API root only; it does not switch to Chat Completions or
// detect the endpoint's capabilities. Credentials supplied through Options are
// checked by the SDK at request time, not by this constructor.
func NewModel(cfg Config) (model.LLM, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	info := cfg.Model.Clone()
	opts := append([]option.RequestOption(nil), cfg.Options...)
	// Apply even an empty key to clear SDK defaults and option-supplied API keys.
	opts = append(opts, option.WithAPIKey(cfg.APIKey))
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	opts = append(opts, option.WithBaseURL(cfg.BaseURL), option.WithMaxRetries(0))
	client := openai.NewClient(opts...)
	return &openAIModel{client: &client, info: info}, nil
}

// ModelName returns the configured model ID for display.
func (m *openAIModel) ModelName() string { return m.info.ID }

// Generate returns a lazy, single-use iterator. Each consumed iterator owns its
// context, HTTP request, and output buffers. stream selects SSE or ordinary JSON
// responses; ordinary responses emit only a ResultEvent. Thinking output contains
// visible reasoning text and summaries. Encrypted reasoning data is discarded;
// it is not emitted or retained for replay. Historical thinking text and summaries
// are sent as ordinary assistant text, not as native reasoning items.
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
		if m == nil || m.client == nil {
			fail(fmt.Errorf("responses: model must be constructed with NewModel"))
			return
		}
		if m.info.ID == "" {
			fail(fmt.Errorf("responses: model ID is required"))
			return
		}
		if err := req.Validate(); err != nil {
			fail(err)
			return
		}
		params, err := buildOpenAIParams(m.info, req)
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
	state := responseStream{provider: m.info.Provider.ID}
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
	state := responseStream{provider: m.info.Provider.ID, yield: func(event model.Event) bool {
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
