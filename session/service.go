package session

import (
	"context"
	"errors"
)

var (
	ErrNotFound       = errors.New("session: not found")
	ErrAlreadyExists  = errors.New("session: already exists")
	ErrInvalidSession = errors.New("session: invalid working view")
)

// Service stores sessions and complete events. Requests must be non-nil.
type Service interface {
	Create(context.Context, *CreateRequest) (*CreateResponse, error)
	Get(context.Context, *GetRequest) (*GetResponse, error)
	List(context.Context, *ListRequest) (*ListResponse, error)
	// Delete removes a session. A valid request for a missing session returns nil.
	Delete(context.Context, *DeleteRequest) error
	// AppendEvent commits a complete event, then updates the supplied view.
	// Partial events are ignored without changing history or update times.
	// Treat submitted events and their nested data as read-only.
	AppendEvent(context.Context, Session, *Event) error
}

type CreateRequest struct {
	AppName string
	UserID  string
	// SessionID is generated when empty.
	SessionID string
}

type CreateResponse struct {
	Session Session
}

type GetRequest struct {
	AppName   string
	UserID    string
	SessionID string
}

type GetResponse struct {
	Session Session
}

type ListRequest struct {
	AppName string
	UserID  string
}

type ListResponse struct {
	// Sessions are sorted by ID and omit history. Use Get to load full history.
	Sessions []Session
}

type DeleteRequest struct {
	AppName   string
	UserID    string
	SessionID string
}
