package skilltool_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/tool/skilltoolset/internal/skilltool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

type streamSource struct {
	skill.Source
	open func(context.Context) (io.ReadCloser, error)
}

func (s streamSource) LoadResource(ctx context.Context, _, _ string) (io.ReadCloser, error) {
	return s.open(ctx)
}

type trackedStream struct {
	io.Reader
	bytes, reads, closes int
}

func (s *trackedStream) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	s.bytes += n
	s.reads++
	return n, err
}

func (s *trackedStream) Close() error { s.closes++; return nil }

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestResourceToolClosesAndLimitsCustomStreams(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reader  func(context.CancelFunc) io.Reader
		want    string
		wantErr error
	}{
		{"text", func(context.CancelFunc) io.Reader { return strings.NewReader("Hello, 世界!") }, "Hello, 世界!", nil},
		{"empty", func(context.CancelFunc) io.Reader { return strings.NewReader("") }, "", nil},
		{"exact limit", func(context.CancelFunc) io.Reader { return strings.NewReader(strings.Repeat("x", 1<<20)) }, strings.Repeat("x", 1<<20), nil},
		{"over limit", func(context.CancelFunc) io.Reader { return strings.NewReader(strings.Repeat("x", (1<<20)+100)) }, "", skill.ErrTooLarge},
		{"invalid UTF-8", func(context.CancelFunc) io.Reader { return strings.NewReader("\xff") }, "", skill.ErrInvalidSkill},
		{"NUL", func(context.CancelFunc) io.Reader { return strings.NewReader("a\x00b") }, "", skill.ErrInvalidSkill},
		{"read failure", func(context.CancelFunc) io.Reader {
			return io.MultiReader(strings.NewReader("partial"), readerFunc(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }))
		}, "", io.ErrUnexpectedEOF},
		{"cancel during read", func(cancel context.CancelFunc) io.Reader {
			return readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "partial"), nil })
		}, "", context.Canceled},
		{"cancel on EOF", func(cancel context.CancelFunc) io.Reader {
			return readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "partial"), io.EOF })
		}, "", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream := &trackedStream{Reader: tc.reader(cancel)}
			resource, err := skilltool.LoadSkillResource(streamSource{open: func(context.Context) (io.ReadCloser, error) { return stream, nil }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := resource.Execute(ctx, json.RawMessage(`{"name":"demo","path":"assets/file"}`))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if stream.closes != 1 || stream.bytes > (1<<20)+1 {
				t.Fatalf("closes = %d, bytes read = %d", stream.closes, stream.bytes)
			}
			if tc.wantErr != nil {
				if got != "" {
					t.Fatal("returned partial content on failure")
				}
				return
			}
			var result skilltool.ResourceResult
			if err := json.Unmarshal([]byte(got), &result); err != nil {
				t.Fatal(err)
			}
			if result.Name != "demo" || result.Path != "assets/file" || result.Content != tc.want {
				t.Fatal("resource result changed")
			}
		})
	}
}

func TestResourceToolCancellationAfterOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := &trackedStream{Reader: strings.NewReader("unused")}
	resource, err := skilltool.LoadSkillResource(streamSource{open: func(context.Context) (io.ReadCloser, error) {
		cancel()
		return stream, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Execute(ctx, json.RawMessage(`{"name":"demo","path":"assets/file"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if stream.reads != 0 || stream.closes != 1 {
		t.Fatalf("reads = %d, closes = %d", stream.reads, stream.closes)
	}
}

func TestResourceToolValidatesPathBeforeCustomSource(t *testing.T) {
	resource, err := skilltool.LoadSkillResource(streamSource{open: func(context.Context) (io.ReadCloser, error) {
		t.Fatal("invalid path reached the source")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Execute(t.Context(), json.RawMessage(`{"name":"demo","path":"../secret"}`)); !errors.Is(err, skill.ErrInvalidPath) {
		t.Fatalf("invalid path = %v", err)
	}
}
