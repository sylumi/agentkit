package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylumi/agentkit/internal/modeldata"
)

func TestSyncAlwaysDownloadsAndReplacesAllData(t *testing.T) {
	t.Chdir(t.TempDir())
	raw := `{"openai":{"models":{"m":{}}},"custom":{"models":{"m":{"tool_call":false}}}}`
	calls := 0
	mockHTTP(t, func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := modeldata.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(snapshot.Models) != 2 || snapshot.Models[0].Provider.ID != "custom" || snapshot.Models[1].Provider.ID != "openai" {
		t.Fatal("sync did not import every provider and model")
	}
	// An identical download must still replace the old snapshot and timestamp.
	oldStamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot.FetchedAt = oldStamp
	if err := saveSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = modeldata.Decode(bytes.NewReader(data))
	if err != nil || !snapshot.FetchedAt.After(oldStamp) || calls != 2 {
		t.Fatal("unchanged source was not downloaded and written again")
	}
	// Old data is neither parsed nor merged into the new download.
	if err := os.WriteFile(outputPath, []byte("damaged old snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	raw = `{"replacement":{"models":{"new":{}}}}`
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = modeldata.Decode(bytes.NewReader(data))
	if err != nil || calls != 3 || len(snapshot.Models) != 1 || snapshot.Models[0].ID != "new" || snapshot.Models[0].Provider.ID != "replacement" {
		t.Fatal("fresh download did not replace the old file")
	}
}

func TestSyncFailurePreservesPreviousSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	raw := `{"openai":{"models":{"m":{}}}}`
	mockHTTP(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})
	if err := run(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`invalid`, `{"openai":{"models":[]}}`, `{"openai":{"models":{"m":{"cost":{"input":"invalid"}}}}}`} {
		raw = invalid
		if err := run(); err == nil {
			t.Fatal("accepted invalid source")
		}
		after, err := os.ReadFile(outputPath)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed sync changed the previous snapshot")
		}
	}
	mockHTTP(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})
	if err := run(); err == nil {
		t.Fatal("expected download failure")
	}
	after, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed download changed the previous snapshot")
	}
}

func TestSyncWriteFailureCleansUpTemporaryFile(t *testing.T) {
	t.Chdir(t.TempDir())
	// A directory at the destination makes the final replacement fail.
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outputPath, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	mockHTTP(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"openai":{"models":{"m":{}}}}`))}, nil
	})
	if err := run(); err == nil {
		t.Fatal("expected replacement failure")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatal("replacement failure damaged the existing destination")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), ".models-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary snapshot was not cleaned up")
	}
}

func TestFetchErrorsAndSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    io.Reader
		failure error
		want    string
	}{
		{"HTTP failure", 503, strings.NewReader("do not publish"), nil, "HTTP 503"},
		{"transport failure", 0, nil, errors.New("offline"), "offline"},
		{"too large", 200, io.LimitReader(zeroReader{}, maxSourceBytes+1), nil, "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockHTTP(t, func(*http.Request) (*http.Response, error) {
				if tc.failure != nil {
					return nil, tc.failure
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(tc.body)}, nil
			})
			if _, err := fetch(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetch error = %v", err)
			}
		})
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockHTTP(t *testing.T, respond transportFunc) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != modeldata.ModelsDevURL {
			t.Fatal("sync did not use the fixed models.dev endpoint")
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("fetch supplied model credentials")
		}
		return respond(r)
	})
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
