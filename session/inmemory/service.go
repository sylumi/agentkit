// Package inmemory provides an in-memory session service.
package inmemory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sylumi/agentkit/session"
)

type service struct {
	mu       sync.RWMutex
	sessions map[sessionKey]*localSession
}

// New creates a service whose data lasts for the life of the instance.
func New() session.Service {
	return &service{sessions: make(map[sessionKey]*localSession)}
}

func (s *service) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
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
		return nil, fmt.Errorf("%w: %q", session.ErrAlreadyExists, sessionID)
	}
	record := &localSession{
		key:       key,
		updatedAt: time.Now().UTC(),
	}
	s.sessions[key] = record
	return &session.CreateResponse{Session: copySession(record, true)}, nil
}

func (s *service) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
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
		return nil, fmt.Errorf("%w: %q", session.ErrNotFound, req.SessionID)
	}
	return &session.GetResponse{Session: copySession(record, true)}, nil
}

func (s *service) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("session: app_name and user_id are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := &session.ListResponse{Sessions: []session.Session{}}
	for key, record := range s.sessions {
		if key.appName == req.AppName && key.userID == req.UserID {
			result.Sessions = append(result.Sessions, copySession(record, false))
		}
	}
	slices.SortFunc(result.Sessions, func(a, b session.Session) int {
		return strings.Compare(a.ID(), b.ID())
	})
	return result, nil
}

func (s *service) Delete(ctx context.Context, req *session.DeleteRequest) error {
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

func (s *service) AppendEvent(ctx context.Context, current session.Session, event *session.Event) error {
	view, ok := current.(*localSession)
	if !ok || view == nil {
		return session.ErrInvalidSession
	}
	if event == nil {
		return fmt.Errorf("session: event is required")
	}
	if event.Partial {
		return nil
	}
	storedEvent := *event

	s.mu.Lock()
	defer s.mu.Unlock()
	view.mu.Lock()
	defer view.mu.Unlock()
	record, ok := s.sessions[view.key]
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrNotFound, view.ID())
	}
	record.events = append(record.events, &storedEvent)
	record.updatedAt = time.Now().UTC()
	view.events = append(view.events, &storedEvent)
	view.updatedAt = record.updatedAt
	return nil
}
