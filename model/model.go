package model

import (
	"context"
	"iter"
)

// Model generates assistant output using a configured model connection.
// Implementations must support independent concurrent calls without sharing
// mutable generation state between them.
type Model interface {
	// Generate returns a lazy, single-use iterator. No request or background
	// reading starts until iteration, which checks ctx and validates req before
	// I/O. Reusing the iterator must fail without sending another request.
	// With stream=true, adapters use streaming requests and may emit incremental
	// events. With stream=false, adapters use ordinary requests and emit only
	// one ResultEvent on success. Both modes return a CallError on failure.
	//
	// Each yield carries either a valid event value and nil error, or a nil Event
	// and a *CallError. Continuous consumption must receive exactly one terminal
	// ResultEvent or error, followed by iterator return without further yields.
	// Events, results, and failure snapshots must remain unchanged after delivery.
	// The implementation assembles the final Result and failure snapshots;
	// consumers may use them directly. Content deltas are optional. When emitted,
	// their content must be consistent with the final result, which the adapter
	// is responsible for ensuring.
	//
	// Implementations must propagate context cancellation to I/O and preserve
	// its cause and partial output in CallError. If yield returns false, they
	// must stop yielding, terminate the request, release resources, and return.
	// An early-exiting consumer receives no additional terminal event or error.
	//
	// ctx must not be nil. Callers must keep req and its nested data unchanged
	// from iterator creation through consumption; implementations must not
	// modify the request. Generate does not execute tools or commit history.
	Generate(ctx context.Context, req Request, stream bool) iter.Seq2[Event, error]
}
