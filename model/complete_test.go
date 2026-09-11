package model

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"testing"
)

type generateFunc func(context.Context, Request, bool) iter.Seq2[Event, error]

func (f generateFunc) Generate(ctx context.Context, req Request, stream bool) iter.Seq2[Event, error] {
	return f(ctx, req, stream)
}

func completeFixture() ResultEvent {
	return ResultEvent{Result: Result{
		Message:    &Message{Role: RoleAssistant, Parts: []Part{NewTextPart("hello")}},
		StopReason: StopReasonStop,
		Metadata:   &ResponseMetadata{Provider: "test", ResponseID: "resp_1"},
	}}
}

func TestCompleteResultAndCleanup(t *testing.T) {
	var callCtx context.Context
	returned := false
	final := completeFixture()
	req := Request{Instructions: "hello"}
	m := generateFunc(func(ctx context.Context, got Request, stream bool) iter.Seq2[Event, error] {
		if stream || !reflect.DeepEqual(got, req) {
			t.Fatal("wrong request or generation mode")
		}
		callCtx = ctx
		return func(yield func(Event, error) bool) {
			defer func() { returned = true }()
			if !yield(final, nil) {
				t.Fatal("Complete stopped before iterator cleanup")
			}
			if ctx.Err() != nil {
				t.Fatal("Complete canceled before iterator return")
			}
		}
	})
	result, err := Complete(context.Background(), m, req)
	if err != nil || !reflect.DeepEqual(result, &final.Result) || !returned || callCtx.Err() != context.Canceled {
		t.Fatalf("result=%v error=%v returned=%v context=%v", result, err, returned, callCtx.Err())
	}
}

func TestCompleteRejectsUnexpectedEvents(t *testing.T) {
	for _, event := range []Event{
		nil, PartStart{Kind: PartText}, TextDelta{Delta: "text"},
		ThinkingDelta{Delta: "thought"}, ToolCallDelta{Arguments: "{"},
		PartEnd{}, &ResultEvent{}, (*ResultEvent)(nil),
		ResultEvent{},
		ResultEvent{Result: Result{StopReason: StopReasonToolCalls}},
	} {
		t.Run(fmt.Sprintf("%T/%v", event, event), func(t *testing.T) {
			var child context.Context
			m := generateFunc(func(ctx context.Context, _ Request, _ bool) iter.Seq2[Event, error] {
				child = ctx
				return func(yield func(Event, error) bool) {
					if yield(event, nil) {
						t.Fatal("accepted unexpected event")
					}
					if ctx.Err() != context.Canceled {
						t.Fatal("invalid event did not cancel generation")
					}
					// A misbehaving producer must not overwrite the first failure.
					if yield(completeFixture(), nil) {
						t.Fatal("accepted event after failure")
					}
				}
			})
			result, err := Complete(context.Background(), m, Request{Instructions: "hello"})
			var callErr *CallError
			if result != nil || !errors.Is(err, ErrInvalidStream) || !errors.As(err, &callErr) || child.Err() != context.Canceled {
				t.Fatalf("result=%v error=%v", result, err)
			}
			if !reflect.DeepEqual(callErr.Partial, PartialOutput{}) {
				t.Fatal("constructed partial output from invalid event")
			}
		})
	}
}

func TestCompleteFailures(t *testing.T) {
	transportErr := errors.New("transport failed")
	outputTokens := int64(9)
	wrapped := fmt.Errorf("provider: %w", &CallError{Cause: transportErr, Partial: PartialOutput{
		Parts: []PartialPart{{Kind: PartThinking, Thinking: &ThinkingPart{Kind: ThinkingSummary, Text: "partial"}}},
		Usage: Usage{OutputTokens: &outputTokens}, Metadata: &ResponseMetadata{ResponseID: "resp_1"},
	}})
	for _, tc := range []struct {
		name string
		run  func(func(Event, error) bool)
		want error
	}{
		{"EOF", func(func(Event, error) bool) {}, ErrIncompleteStream},
		{"ordinary error", func(y func(Event, error) bool) { y(nil, transportErr) }, transportErr},
		{"provider snapshot", func(y func(Event, error) bool) { y(nil, wrapped) }, transportErr},
		{"mixed event and error", func(y func(Event, error) bool) { y(completeFixture(), transportErr) }, ErrInvalidStream},
		{"missing cause", func(y func(Event, error) bool) { y(nil, &CallError{}) }, ErrInvalidStream},
		{"typed nil error", func(y func(Event, error) bool) { y(nil, (*CallError)(nil)) }, ErrInvalidStream},
		{"duplicate result", func(y func(Event, error) bool) {
			if y(completeFixture(), nil) {
				y(completeFixture(), nil)
			}
		}, ErrInvalidStream},
		{"error after result", func(y func(Event, error) bool) {
			if y(completeFixture(), nil) {
				y(nil, transportErr)
			}
		}, ErrInvalidStream},
		{"delta after result", func(y func(Event, error) bool) {
			if y(completeFixture(), nil) {
				y(TextDelta{Delta: "extra"}, nil)
			}
		}, ErrInvalidStream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var child context.Context
			m := generateFunc(func(ctx context.Context, _ Request, _ bool) iter.Seq2[Event, error] {
				child = ctx
				return tc.run
			})
			result, err := Complete(context.Background(), m, Request{Instructions: "hello"})
			var callErr *CallError
			if result != nil || !errors.Is(err, tc.want) || !errors.As(err, &callErr) || child.Err() != context.Canceled {
				t.Fatalf("result=%v error=%v", result, err)
			}
			if tc.name == "provider snapshot" && err != wrapped {
				t.Fatal("lost received error and snapshot")
			}
			if tc.name == "error after result" && !errors.Is(err, transportErr) {
				t.Fatal("lost terminal protocol failure cause")
			}
		})
	}
}

func TestCompleteInputsAndCancellation(t *testing.T) {
	called := false
	m := generateFunc(func(context.Context, Request, bool) iter.Seq2[Event, error] { called = true; return nil })
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		model Model
		req   Request
	}{
		{nil, m, Request{Instructions: "hello"}},
		{canceled, m, Request{Instructions: "hello"}},
		{context.Background(), nil, Request{Instructions: "hello"}},
	} {
		if result, err := Complete(tc.ctx, tc.model, tc.req); result != nil || err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if called {
		t.Fatal("invalid input invoked model")
	}
	if _, err := Complete(context.Background(), m, Request{Instructions: "hello"}); !errors.Is(err, ErrInvalidStream) {
		t.Fatal(err)
	}
	for _, withSnapshot := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		providerErr := &CallError{Cause: context.Canceled, Partial: PartialOutput{Metadata: &ResponseMetadata{ResponseID: "canceled_response"}}}
		m = generateFunc(func(child context.Context, _ Request, _ bool) iter.Seq2[Event, error] {
			return func(y func(Event, error) bool) {
				cancel()
				if child.Err() != context.Canceled {
					t.Fatal("cancellation did not propagate")
				}
				if withSnapshot {
					y(nil, providerErr)
				}
			}
		})
		result, err := Complete(ctx, m, Request{Instructions: "hello"})
		cancel()
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("result=%v error=%v", result, err)
		}
		if withSnapshot && err != providerErr {
			t.Fatal("lost cancellation snapshot")
		}
	}
}

func TestCompleteDelegatesRequestValidation(t *testing.T) {
	var failure error
	m := generateFunc(func(_ context.Context, req Request, stream bool) iter.Seq2[Event, error] {
		if stream {
			t.Fatal("Complete requested streaming generation")
		}
		return func(yield func(Event, error) bool) {
			if err := req.Validate(); err != nil {
				failure = fmt.Errorf("provider: %w", &CallError{Cause: err})
				yield(nil, failure)
			} else {
				t.Fatal("expected an invalid request")
			}
		}
	})
	result, err := Complete(context.Background(), m, Request{})
	if failure == nil || result != nil || err != failure {
		t.Fatalf("result=%v error=%v; expected the model's validation error", result, err)
	}
}
