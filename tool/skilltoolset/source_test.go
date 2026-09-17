package skilltoolset_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/tool/skilltoolset"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

// memorySource verifies that the toolset works without filesystem access.
type memorySource struct{}

func (memorySource) ListFrontmatters(context.Context) ([]skill.Frontmatter, error) {
	return []skill.Frontmatter{{Name: "demo", Description: "A <custom> skill"}}, nil
}

func (s memorySource) LoadFrontmatter(ctx context.Context, name string) (skill.Frontmatter, error) {
	if name != "demo" {
		return skill.Frontmatter{}, skill.ErrSkillNotFound
	}
	frontmatters, _ := s.ListFrontmatters(ctx)
	return frontmatters[0], nil
}

func (s memorySource) LoadInstructions(ctx context.Context, name string) (string, error) {
	if _, err := s.LoadFrontmatter(ctx, name); err != nil {
		return "", err
	}
	return "BODY_MARKER: Read references/guide.md.", nil
}

func (s memorySource) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	if _, err := s.LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	switch subpath {
	case "", ".", "references", "references/guide.md":
		return []string{"references/guide.md"}, nil
	default:
		return nil, skill.ErrResourceNotFound
	}
}

func (s memorySource) LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	if _, err := s.LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	if resourcePath != "references/guide.md" {
		return nil, skill.ErrResourceNotFound
	}
	return io.NopCloser(strings.NewReader("RESOURCE_MARKER: Guide")), nil
}

func TestToolsetCustomSource(t *testing.T) {
	ts, err := skilltoolset.New(skilltoolset.Config{Source: memorySource{}})
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := ts.Instructions(t.Context())
	if err != nil || !strings.Contains(instructions, "A &lt;custom&gt; skill") || strings.Contains(instructions, "BODY_MARKER") || strings.Contains(instructions, "RESOURCE_MARKER") {
		t.Fatalf("catalog = %q, %v", instructions, err)
	}
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		args string
		want string
	}{
		{`{}`, `{"skills":[{"name":"demo","description":"A <custom> skill"}]}`},
		{`{"name":"demo"}`, `{"frontmatter":{"name":"demo","description":"A <custom> skill"},"instructions":"BODY_MARKER: Read references/guide.md.","resources":["references/guide.md"]}`},
		{`{"name":"demo","path":"references/guide.md"}`, `{"name":"demo","path":"references/guide.md","content":"RESOURCE_MARKER: Guide"}`},
	} {
		got, err := tools[i].Execute(t.Context(), json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		var actual, expected any
		if err := json.Unmarshal([]byte(got), &actual); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.want), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("tool %d = %s, want %s", i, got, tc.want)
		}
	}
}

func TestToolsetSourceConfiguration(t *testing.T) {
	// Constructing tools does not access the source.
	if _, err := skilltoolset.New(skilltoolset.Config{Source: &unusedSource{}}); err != nil {
		t.Fatal(err)
	}
	ts, err := skilltoolset.New(skilltoolset.Config{Source: skill.NewMergedSource(memorySource{}, memorySource{})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Instructions(t.Context()); !errors.Is(err, skill.ErrDuplicateSkill) {
		t.Fatalf("instructions did not detect duplicates: %v", err)
	}
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`)); !errors.Is(err, skill.ErrDuplicateSkill) {
		t.Fatalf("list tool did not detect duplicates: %v", err)
	}
}

type unusedSource struct{ skill.Source }
