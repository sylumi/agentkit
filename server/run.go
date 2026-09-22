package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/session"
)

type done struct {
	Status RunStatus `json:"status"`
	Error  *apiError `json:"error,omitempty"`
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" || utf8.RuneCountInString(body.Text) > defaultMaxInputRunes {
		fail(w, http.StatusBadRequest, codeInvalidText, "Text must contain 1 to 4000 characters and cannot be blank.")
		return
	}
	id := r.PathValue("id")
	release, ok := s.reserve(id)
	if !ok {
		fail(w, http.StatusConflict, codeSessionBusy, "This session is already running.")
		return
	}
	defer release()
	_, err := s.store.Get(r.Context(), &session.GetRequest{AppName: s.appName, UserID: s.userID, SessionID: id})
	if err != nil {
		storageError(w, err)
		return
	}
	ctx := r.Context()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := writeDeadline(rc); err != nil {
		return
	}
	if _, err := fmt.Fprint(w, sseConnected); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	input := &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(body.Text)}}
	result := done{Status: StatusFinished}

	for event, err := range s.runner.Run(ctx, s.userID, id, input, runner.WithStreaming(true)) {
		if err != nil {
			result = runError(err)
			if result.Error != nil && result.Error.Code == codeRunFailed {
				slog.ErrorContext(ctx, "agent run failed", "session_id", id, "error", err)
			}
			break
		}
		if err := emit(w, rc, sseEventData, event); err != nil {
			return // Leaving the iterator stops further model and tool calls.
		}
	}
	if err := ctx.Err(); err != nil {
		result = runError(err)
	}
	// No more events can be committed by this run after releasing its slot.
	release()
	_ = emit(w, rc, sseEventDone, result)
}

func emit(w http.ResponseWriter, rc *http.ResponseController, kind string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// A slow reader must not keep a session locked indefinitely. Not every
	// ResponseWriter supports deadlines (for example, an in-process recorder).
	if err := writeDeadline(rc); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data); err != nil {
		return err
	}
	return rc.Flush()
}

func writeDeadline(rc *http.ResponseController) error {
	err := rc.SetWriteDeadline(time.Now().Add(defaultWriteTimeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func runError(err error) done {
	if errors.Is(err, context.Canceled) {
		return done{Status: StatusCancelled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return done{Status: StatusError, Error: &apiError{codeRunTimeout, "The run reached its time limit."}}
	}
	return done{Status: StatusError, Error: &apiError{codeRunFailed, "Agent execution failed. Check the server log for details."}}
}
