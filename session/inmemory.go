package session

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type inMemoryService struct {
	mu       sync.RWMutex
	sessions map[sessionKey]*session
}

// InMemoryService creates a service whose data lasts for the life of the instance.
func InMemoryService() Service {
	return &inMemoryService{sessions: make(map[sessionKey]*session)}
}

func (s *inMemoryService) Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("session: app_name and user_id are required")
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	key := sessionKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: sessionID,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[key]; exists {
		return nil, fmt.Errorf("%w: %q", ErrAlreadyExists, sessionID)
	}
	record := &session{
		key:       key,
		updatedAt: time.Now().UTC(),
	}
	s.sessions[key] = record
	return &CreateResponse{Session: copySession(record, true)}, nil
}

func (s *inMemoryService) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session: app_name, user_id and session_id are required")
	}
	key := sessionKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.sessions[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, req.SessionID)
	}
	return &GetResponse{Session: copySession(record, true)}, nil
}

func (s *inMemoryService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("session: app_name and user_id are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := &ListResponse{Sessions: []Session{}}
	for key, record := range s.sessions {
		if key.appName == req.AppName && key.userID == req.UserID {
			result.Sessions = append(result.Sessions, copySession(record, false))
		}
	}
	slices.SortFunc(result.Sessions, func(a, b Session) int {
		return strings.Compare(a.ID(), b.ID())
	})
	return result, nil
}

func (s *inMemoryService) Delete(ctx context.Context, req *DeleteRequest) error {
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("session: app_name, user_id and session_id are required")
	}
	key := sessionKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
	return nil
}

func (s *inMemoryService) AppendEvent(ctx context.Context, current Session, event *Event) error {
	view, ok := current.(*session)
	if !ok || view == nil {
		return ErrInvalidSession
	}
	if event == nil {
		return fmt.Errorf("session: event is required")
	}
	storedEvent := *event

	s.mu.Lock()
	defer s.mu.Unlock()
	view.mu.Lock()
	defer view.mu.Unlock()
	record, ok := s.sessions[view.key]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, view.ID())
	}
	record.events = append(record.events, &storedEvent)
	record.updatedAt = time.Now().UTC()
	view.events = append(view.events, &storedEvent)
	view.updatedAt = record.updatedAt
	return nil
}

// copySession is called while the service lock is held.
func copySession(record *session, includeEvents bool) *session {
	view := &session{
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

type session struct {
	key sessionKey

	mu        sync.RWMutex
	events    []*Event
	updatedAt time.Time
}

func (s *session) ID() string      { return s.key.sessionID }
func (s *session) AppName() string { return s.key.appName }
func (s *session) UserID() string  { return s.key.userID }

func (s *session) Events() Events {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return events(s.events)
}

// LastUpdateTime reports the last service commit time, not the event timestamp.
func (s *session) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

type events []*Event

func (e events) All() iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e events) Len() int { return len(e) }

func (e events) At(index int) *Event {
	if index < 0 || index >= len(e) {
		return nil
	}
	return e[index]
}
