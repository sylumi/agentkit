package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

const (
	defaultMaxInputRunes = 4000
	defaultWriteTimeout  = 10 * time.Second
	maxRequestBodyBytes  = 64 << 10
)

const (
	codeInvalidRequest       = "invalid_request"
	codeInvalidText          = "invalid_text"
	codeRequestTooLarge      = "request_too_large"
	codeRunFailed            = "run_failed"
	codeRunTimeout           = "run_timeout"
	codeSessionBusy          = "session_busy"
	codeSessionNotFound      = "session_not_found"
	codeStorageError         = "storage_error"
	codeUnsupportedMediaType = "unsupported_media_type"
)

// RunStatus identifies the terminal state of an agent execution run.
type RunStatus string

const (
	StatusFinished  RunStatus = "finished"
	StatusCancelled RunStatus = "cancelled"
	StatusError     RunStatus = "error"
)

const (
	sseEventData = "event"
	sseEventDone = "done"
	sseConnected = ": connected\n\n"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, struct {
		Error apiError `json:"error"`
	}{apiError{code, message}})
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType, "Use application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err == nil {
		if err = decoder.Decode(new(any)); errors.Is(err, io.EOF) {
			return true
		}
	}
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		fail(w, http.StatusRequestEntityTooLarge, codeRequestTooLarge, "Request body exceeds 64 KiB.")
	} else {
		fail(w, http.StatusBadRequest, codeInvalidRequest, "Expected one JSON object with supported fields.")
	}
	return false
}
