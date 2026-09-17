package skill_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

type errorSource struct {
	err error
}

func (s errorSource) ListFrontmatters(context.Context) ([]skill.Frontmatter, error) {
	return nil, s.err
}
func (s errorSource) LoadFrontmatter(context.Context, string) (skill.Frontmatter, error) {
	return skill.Frontmatter{}, s.err
}
func (s errorSource) LoadInstructions(context.Context, string) (string, error) {
	return "", s.err
}
func (s errorSource) ListResources(context.Context, string, string) ([]string, error) {
	return nil, s.err
}
func (s errorSource) LoadResource(context.Context, string, string) (io.ReadCloser, error) {
	return nil, s.err
}

type listedSource struct {
	skill.Source
	frontmatters []skill.Frontmatter
}

func (s listedSource) ListFrontmatters(context.Context) ([]skill.Frontmatter, error) {
	return append([]skill.Frontmatter{}, s.frontmatters...), nil
}

func TestMergedSourceCatalog(t *testing.T) {
	first := sourceFixture(t, fstest.MapFS{"zeta/SKILL.md": document("zeta")})
	second := sourceFixture(t, fstest.MapFS{"alpha/SKILL.md": document("alpha")})
	sources := []skill.Source{first, second}
	merged := skill.NewMergedSource(sources...)
	sources[0] = errorSource{fs.ErrPermission}
	frontmatters, err := merged.ListFrontmatters(t.Context())
	if err != nil || len(frontmatters) != 2 || frontmatters[0].Name != "alpha" || frontmatters[1].Name != "zeta" {
		t.Fatalf("catalog is unsorted or source slice aliases caller storage: %v, %v", frontmatters, err)
	}
	frontmatters[0].Metadata["owner"] = "changed"
	again, err := merged.LoadFrontmatter(t.Context(), "alpha")
	if err != nil || again.Metadata["owner"] != "team" {
		t.Fatalf("caller mutation persisted: %v, %v", again, err)
	}
	for name, source := range map[string]skill.Source{
		"across sources": skill.NewMergedSource(first, first),
		"within source": skill.NewMergedSource(listedSource{frontmatters: []skill.Frontmatter{
			{Name: "duplicate"}, {Name: "duplicate"},
		}}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := source.ListFrontmatters(t.Context()); !errors.Is(err, skill.ErrDuplicateSkill) {
				t.Fatalf("duplicate was accepted: %v", err)
			}
		})
	}
}

func TestMergedSourceLookupOrder(t *testing.T) {
	first := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":        document("demo"),
		"demo/assets/shared":   {Data: []byte("first")},
		"demo/assets/first.md": {},
	})
	second := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":         {Data: []byte("---\nname: demo\ndescription: Second\n---\nSecond instructions")},
		"demo/assets/shared":    {Data: []byte("second")},
		"demo/assets/second.md": {},
		"demo/references/guide": {},
	})
	missing := errorSource{fmt.Errorf("wrapped: %w", skill.ErrSkillNotFound)}
	merged := skill.NewMergedSource(missing, first, second)
	fm, err := merged.LoadFrontmatter(t.Context(), "demo")
	if err != nil || fm.Description != "A skill" {
		t.Fatalf("frontmatter = %v, %v", fm, err)
	}
	instructions, err := merged.LoadInstructions(t.Context(), "demo")
	if err != nil || instructions != "Read references/guide.md.\n" {
		t.Fatalf("instructions = %q, %v", instructions, err)
	}
	paths, err := merged.ListResources(t.Context(), "demo", "assets")
	if err != nil || !reflect.DeepEqual(paths, []string{"assets/first.md", "assets/shared"}) {
		t.Fatalf("resources = %v, %v", paths, err)
	}
	stream, err := merged.LoadResource(t.Context(), "demo", "assets/shared")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	content, err := io.ReadAll(stream)
	if err != nil || string(content) != "first" {
		t.Fatalf("resource = %q, %v", content, err)
	}
	if _, err := merged.ListResources(t.Context(), "demo", "references"); !errors.Is(err, skill.ErrResourceNotFound) {
		t.Fatalf("borrowed directory from later source: %v", err)
	}
	if _, err := merged.LoadResource(t.Context(), "demo", "assets/second.md"); !errors.Is(err, skill.ErrResourceNotFound) {
		t.Fatalf("borrowed file from later source: %v", err)
	}
}

func TestMergedSourceErrors(t *testing.T) {
	valid := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":           document("demo"),
		"demo/references/missing": {Data: []byte("would hide the error")},
	})
	for _, cause := range []error{fs.ErrPermission, skill.ErrInvalidSkill, skill.ErrResourceNotFound, fs.ErrNotExist, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			wrapped := fmt.Errorf("origin failure: %w", cause)
			merged := skill.NewMergedSource(errorSource{wrapped}, valid)
			for name, read := range namedReads(merged) {
				if err := read(t.Context(), "demo"); err != wrapped {
					t.Errorf("%s: swallowed origin error: %v", name, err)
				}
			}
			if _, err := merged.ListFrontmatters(t.Context()); err != wrapped {
				t.Fatalf("catalog error = %v", err)
			}
		})
	}
	// Listing must not hide errors from a source even if it reports a missing skill.
	if _, err := skill.NewMergedSource(errorSource{skill.ErrSkillNotFound}, valid).ListFrontmatters(t.Context()); !errors.Is(err, skill.ErrSkillNotFound) {
		t.Fatalf("listing swallowed a source error: %v", err)
	}
	for name, merged := range map[string]skill.Source{
		"empty": skill.NewMergedSource(),
		"absent": skill.NewMergedSource(errorSource{skill.ErrSkillNotFound},
			errorSource{fmt.Errorf("wrapped: %w", skill.ErrSkillNotFound)}),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			for operation, read := range namedReads(merged) {
				if err := read(t.Context(), "missing"); !errors.Is(err, skill.ErrSkillNotFound) {
					t.Errorf("%s: missing skill = %v", operation, err)
				}
				if err := read(ctx, "missing"); !errors.Is(err, context.Canceled) {
					t.Errorf("%s: cancellation = %v", operation, err)
				}
			}
			if _, err := merged.ListFrontmatters(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled catalog = %v", err)
			}
		})
	}
	if got, err := skill.NewMergedSource().ListFrontmatters(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("empty catalog = %v, %v", got, err)
	}
}

func TestMergedSourceConcurrentReads(t *testing.T) {
	merged := skill.NewMergedSource(sourceFixture(t, fstest.MapFS{}), sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":           document("demo"),
		"demo/references/missing": {Data: []byte("Resource")},
	}))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for name, read := range namedReads(merged) {
				if err := read(t.Context(), "demo"); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
			frontmatters, err := merged.ListFrontmatters(t.Context())
			if err != nil || len(frontmatters) != 1 {
				t.Errorf("catalog = %v, %v", frontmatters, err)
				return
			}
			frontmatters[0].Metadata["owner"] = "caller"
		})
	}
	wg.Wait()
}
