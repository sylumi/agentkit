package server

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/sylumi/agentkit/session"
)

type summary struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
	Running   bool      `json:"running"`
}

type snapshot struct {
	summary
	Events []*session.Event `json:"events"`
}

func toSnapshot(current session.Session, running bool) snapshot {
	events := make([]*session.Event, 0, current.Events().Len())
	for event := range current.Events().All() {
		events = append(events, event)
	}
	return snapshot{summary{current.ID(), current.LastUpdateTime(), running}, events}
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	created, err := s.store.Create(r.Context(), &session.CreateRequest{
		AppName: s.appName,
		UserID:  s.userID,
	})
	if err != nil {
		storageError(w, err)
		return
	}

	reply(w, http.StatusCreated, toSnapshot(created.Session, false))
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	listed, err := s.store.List(r.Context(), &session.ListRequest{AppName: s.appName, UserID: s.userID})
	if err != nil {
		storageError(w, err)
		return
	}
	items := make([]summary, 0, len(listed.Sessions))
	s.mu.Lock()
	for _, current := range listed.Sessions {
		items = append(items, summary{current.ID(), current.LastUpdateTime(), s.active[current.ID()]})
	}
	s.mu.Unlock()
	slices.SortFunc(items, func(a, b summary) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	reply(w, http.StatusOK, struct {
		Sessions []summary `json:"sessions"`
	}{items})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	loaded, err := s.store.Get(r.Context(), &session.GetRequest{AppName: s.appName, UserID: s.userID, SessionID: id})
	running := s.active[id]
	s.mu.Unlock()
	if err != nil {
		storageError(w, err)
		return
	}
	reply(w, http.StatusOK, toSnapshot(loaded.Session, running))
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	release, ok := s.reserve(id)
	if !ok {
		fail(w, http.StatusConflict, codeSessionBusy, "Wait for the current run to finish before deleting this session.")
		return
	}
	defer release()
	err := s.store.Delete(r.Context(), &session.DeleteRequest{AppName: s.appName, UserID: s.userID, SessionID: id})
	if err != nil {
		storageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reserve also protects deletion, so a run cannot start during a delete.
func (s *Server) reserve(id string) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[id] {
		return nil, false
	}
	s.active[id] = true
	return sync.OnceFunc(func() {
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
	}), true
}

func storageError(w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrNotFound) {
		fail(w, http.StatusNotFound, codeSessionNotFound, "Session not found.")
	} else {
		fail(w, http.StatusInternalServerError, codeStorageError, "Session storage operation failed.")
	}
}
