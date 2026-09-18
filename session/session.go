// Package session provides conversation history.
package session

import (
	"crypto/rand"
	"iter"
	"time"

	"github.com/sylumi/agentkit/model"
)

// Session is a working view of a conversation. Other views do not auto-refresh.
type Session interface {
	ID() string
	AppName() string
	UserID() string
	Events() Events
	// LastUpdateTime is the last service commit time.
	LastUpdateTime() time.Time
}

// Events is a snapshot in append order. Returned events are read-only.
type Events interface {
	All() iter.Seq[*Event]
	Len() int
	// At returns nil when the index is out of range.
	At(index int) *Event
}

// Event records a complete message or generation outcome.
type Event struct {
	ID           string    `json:"id"`
	InvocationID string    `json:"invocation_id"`
	Author       string    `json:"author"`
	Timestamp    time.Time `json:"timestamp"`

	Message    *model.Message          `json:"message,omitempty"`
	StopReason model.StopReason        `json:"stop_reason,omitempty"`
	Usage      *model.Usage            `json:"usage,omitempty"`
	Metadata   *model.ResponseMetadata `json:"metadata,omitempty"`
}

// NewEvent assigns an ID and timestamp. The caller supplies author and content.
func NewEvent(invocationID string) *Event {
	return &Event{
		ID:           rand.Text(),
		InvocationID: invocationID,
		Timestamp:    time.Now().UTC(),
	}
}
