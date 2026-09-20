// Package agent defines agents and invocation data.
package agent

import (
	"context"
	"iter"

	"github.com/sylumi/agentkit/session"
)

// Agent produces events for one invocation.
type Agent interface {
	Name() string
	Description() string

	// Run lazily yields partial and complete events. Persist each non-partial
	// event to invocation.Session before advancing the iterator. Partial events
	// are for live display only and must not be saved or used to execute tools.
	// Implementations honor ctx cancellation and stop when yield returns false.
	// An execution error is yielded once with a nil event, then Run ends.
	Run(ctx context.Context, invocation *InvocationContext) iter.Seq2[*session.Event, error]
}
