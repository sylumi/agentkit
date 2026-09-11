package model

import (
	"context"
	"errors"
	"fmt"
)

// Complete requests non-streaming generation and returns its final result.
// It delegates request validation to Model.Generate, which must validate before I/O.
// It accepts exactly one valid ResultEvent and rejects incremental events.
// Returned data must not be modified while still in use. A received CallError
// and its adapter-owned failure snapshot are preserved; Complete does not
// accumulate content or construct partial output from invalid events.
//
// Complete waits for iterator return and cancels its child context on exit.
// The model must honor cancellation and release its resources; no goroutines
// are started here. Complete does not execute tools or update history.
func Complete(ctx context.Context, m Model, req Request) (*Result, error) {
	if ctx == nil {
		return nil, &CallError{Cause: fmt.Errorf("context: must not be nil")}
	}
	if err := ctx.Err(); err != nil {
		return nil, &CallError{Cause: err}
	}
	if m == nil {
		return nil, &CallError{Cause: fmt.Errorf("model: must not be nil")}
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var result *Result
	invalid := func(err error) error {
		return &CallError{Cause: errors.Join(ErrInvalidStream, err)}
	}
	seq := m.Generate(callCtx, req, false)
	if seq == nil {
		return nil, invalid(fmt.Errorf("iterator: must not be nil"))
	}
	var failure error
	seq(func(event Event, err error) bool {
		if failure != nil {
			return false
		}
		switch {
		case result != nil:
			failure = invalid(errors.Join(fmt.Errorf("event: received yield after result"), err))
		case err != nil:
			var callErr *CallError
			switch {
			case event != nil:
				failure = invalid(errors.Join(fmt.Errorf("event: expected nil with an error"), err))
			case !errors.As(err, &callErr):
				failure = &CallError{Cause: err}
			case callErr == nil || callErr.Cause == nil:
				failure = invalid(errors.Join(fmt.Errorf("call_error: a non-nil cause is required"), err))
			default:
				failure = err
			}
		default:
			e, ok := event.(ResultEvent)
			if !ok {
				failure = invalid(fmt.Errorf("event: non-streaming generation requires a ResultEvent, got %T", event))
			} else if err := e.Result.Validate(); err != nil {
				failure = invalid(err)
			} else {
				result = &e.Result
			}
		}
		if failure != nil {
			cancel()
			return false
		}
		return true
	})
	if failure != nil {
		return nil, failure
	}
	if result != nil {
		return result, nil
	}
	cause := ctx.Err()
	if cause == nil {
		cause = ErrIncompleteStream
	}
	return nil, &CallError{Cause: cause}
}
