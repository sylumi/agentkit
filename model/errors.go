package model

import "errors"

// ErrIncompleteStream identifies a stream that returned without a terminal
// result or error. EOF alone does not confirm a generation outcome.
var ErrIncompleteStream = errors.New("model: stream ended without a terminal result")

// ErrInvalidStream identifies invalid event data or event ordering.
var ErrInvalidStream = errors.New("model: invalid event stream")

// CallError preserves a failed generation's cause and diagnostic output.
// Cause must be non-nil. Partial is not a confirmed generation result.
// Construction does not copy or validate Partial; the producer must provide
// a stable snapshot and stop modifying its nested data after delivery.
type CallError struct {
	Cause   error
	Partial PartialOutput
}

// Error describes the failure without rendering the diagnostic content.
// Nil receivers and missing causes remain printable, but are not valid failures.
func (e *CallError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return "model: call failed"
	}
	return "model: call failed: " + e.Cause.Error()
}

// Unwrap preserves the original cause for errors.Is and errors.As.
func (e *CallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
