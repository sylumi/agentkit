package inmemory

import (
	"iter"
	"slices"
	"sync"
	"time"

	"github.com/sylumi/agentkit/session"
)

// copySession is called while the service lock is held.
func copySession(record *localSession, includeEvents bool) *localSession {
	view := &localSession{
		key:       record.key,
		updatedAt: record.updatedAt,
	}
	if includeEvents {
		view.events = slices.Clone(record.events)
	}
	return view
}

type sessionKey struct {
	appName   string
	userID    string
	sessionID string
}

type localSession struct {
	key sessionKey

	mu        sync.RWMutex
	events    []*session.Event
	updatedAt time.Time
}

func (s *localSession) ID() string      { return s.key.sessionID }
func (s *localSession) AppName() string { return s.key.appName }
func (s *localSession) UserID() string  { return s.key.userID }

func (s *localSession) Events() session.Events {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return events(s.events)
}

// LastUpdateTime reports the last service commit time, not the event timestamp.
func (s *localSession) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

type events []*session.Event

func (e events) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e events) Len() int { return len(e) }

func (e events) At(index int) *session.Event {
	if index < 0 || index >= len(e) {
		return nil
	}
	return e[index]
}
