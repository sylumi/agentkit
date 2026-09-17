package skill_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func document(name string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\nname: " + name + "\ndescription: A skill\nmetadata:\n  owner: team\n---\nRead references/guide.md.\n")}
}

func TestFileSystemSourceDiscoveryAndResources(t *testing.T) {
	files := fstest.MapFS{
		"zeta/SKILL.md": document("zeta"), "alpha/SKILL.md": document("alpha"),
		"alpha/references/guide.md":        {Data: []byte("Guidance.")},
		"alpha/assets/nested/template.txt": {Data: []byte("Template.")},
		"alpha/scripts/run.sh":             {Data: []byte("echo example")},
		"alpha/private.txt":                {Data: []byte("Not a resource.")},
		"Not-A-Skill/notes.txt":            {Data: []byte("Ignored.")},
	}
	f := sourceFixture(t, files)
	frontmatters, err := f.ListFrontmatters(t.Context())
	if err != nil || len(frontmatters) != 2 || frontmatters[0].Name != "alpha" || frontmatters[1].Name != "zeta" {
		t.Fatalf("list = %#v, error = %v", frontmatters, err)
	}
	instructions, err := f.LoadInstructions(t.Context(), "alpha")
	if err != nil || instructions != "Read references/guide.md.\n" {
		t.Fatalf("instructions = %q, %v", instructions, err)
	}
	resources, err := f.ListResources(t.Context(), "alpha", "")
	if err != nil || !reflect.DeepEqual(resources, []string{"assets/nested/template.txt", "references/guide.md", "scripts/run.sh"}) {
		t.Fatalf("resources = %v, %v", resources, err)
	}
	for resource, want := range map[string]string{"references/guide.md": "Guidance.", "assets/nested/template.txt": "Template.", "scripts/run.sh": "echo example"} {
		stream, err := f.LoadResource(t.Context(), "alpha", resource)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || string(content) != want {
			t.Fatalf("read %s = %q, %v", resource, content, err)
		}
	}
	frontmatters[0].Metadata["owner"] = "changed"
	resources[0] = "changed"
	again, err := f.LoadFrontmatter(t.Context(), "alpha")
	if err != nil || again.Metadata["owner"] != "team" {
		t.Fatalf("caller edits affected future loads: %+v, %v", again, err)
	}
	paths, err := f.ListResources(t.Context(), "alpha", "")
	if err != nil || paths[0] != "assets/nested/template.txt" {
		t.Fatalf("caller edits affected resource listing: %v, %v", paths, err)
	}
}

func TestFileSystemSourceInvalidPathsAndFiles(t *testing.T) {
	f := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md": document("demo"),
	})
	for _, name := range []string{"", "../demo", "demo/../demo", "/demo", "demo\\name"} {
		if _, err := f.LoadInstructions(t.Context(), name); !errors.Is(err, skill.ErrInvalidPath) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, resource := range []string{"", "../SKILL.md", "/references/file", "references/../SKILL.md", "references//file", "references\\file", "SKILL.md", "private.txt", "references"} {
		if _, err := f.LoadResource(t.Context(), "demo", resource); !errors.Is(err, skill.ErrInvalidPath) {
			t.Errorf("resource %q: %v", resource, err)
		}
	}
	if _, err := f.LoadInstructions(t.Context(), "missing"); !errors.Is(err, skill.ErrSkillNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing skill lost error classification: %v", err)
	}
	bad := sourceFixture(t, fstest.MapFS{"demo/SKILL.md": document("other")})
	if _, err := bad.ListFrontmatters(t.Context()); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("mismatched directory: %v", err)
	}
	for _, name := range []string{"demo/references", "demo/references/bad\\name.txt"} {
		invalid := sourceFixture(t, fstest.MapFS{"demo/SKILL.md": document("demo"), name: {Data: []byte("text")}})
		if _, err := invalid.ListResources(t.Context(), "demo", ""); !errors.Is(err, skill.ErrInvalidPath) {
			t.Fatalf("listed an unreadable resource %q: %v", name, err)
		}
	}
}

func TestLazyResourceContentsAndDocumentLimit(t *testing.T) {
	// A resource can be too large or binary without preventing discovery or
	// loading the instructions. Its contents are checked only when requested.
	files := fstest.MapFS{"demo/SKILL.md": document("demo"), "demo/assets/large": {Data: make([]byte, (1<<20)+1)}}
	f := sourceFixture(t, files)
	if _, err := f.ListFrontmatters(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.LoadInstructions(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ListResources(t.Context(), "demo", ""); err != nil {
		t.Fatal(err)
	}
	files["demo/SKILL.md"].Data = append(document("demo").Data, []byte(strings.Repeat("x", 256<<10))...)
	if _, err := f.ListFrontmatters(t.Context()); err != nil {
		t.Fatalf("catalog read the full body: %v", err)
	}
	if _, err := f.LoadInstructions(t.Context(), "demo"); !errors.Is(err, skill.ErrTooLarge) {
		t.Fatalf("large instructions: %v", err)
	}
}

func TestFileSystemSourceConcurrentReads(t *testing.T) {
	f := sourceFixture(t, fstest.MapFS{"demo/SKILL.md": document("demo"), "demo/references/guide.md": {Data: []byte("Guide")}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.ListFrontmatters(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.LoadInstructions(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.LoadResource(ctx, "demo", "references/guide.md"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			fm, err := f.LoadFrontmatter(t.Context(), "demo")
			if err != nil {
				t.Error(err)
				return
			}
			fm.Metadata["owner"] = "caller"
			stream, err := f.LoadResource(t.Context(), "demo", "references/guide.md")
			if err != nil {
				t.Error(err)
				return
			}
			defer stream.Close()
			if _, err := io.ReadAll(stream); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestLocalSymlinksAreRejected(t *testing.T) {
	for _, linkDirectory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[linkDirectory], func(t *testing.T) {
			rootDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(rootDir, "demo"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(rootDir, "demo", "SKILL.md"), document("demo").Data, 0600); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := os.WriteFile(filepath.Join(target, "secret.txt"), []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(rootDir, "demo", "references")
			if !linkDirectory {
				if err := os.Mkdir(link, 0700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "secret.txt")
				target = filepath.Join(target, "secret.txt")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			f := sourceFixture(t, os.DirFS(rootDir))
			if _, err := f.LoadResource(t.Context(), "demo", "references/secret.txt"); !errors.Is(err, skill.ErrInvalidPath) {
				t.Fatalf("read symlink: %v", err)
			}
			if _, err := f.ListResources(t.Context(), "demo", ""); !errors.Is(err, skill.ErrInvalidPath) {
				t.Fatalf("list symlink: %v", err)
			}
		})
	}
}

type readFailureFS struct {
	fs.FS
	closes *int
	cause  error
}

func (f readFailureFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(f.FS, name)
}

func (f readFailureFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &readFailureFile{File: file, closes: f.closes, cause: f.cause}, nil
}

type readFailureFile struct {
	fs.File
	closes *int
	cause  error
}

func (f *readFailureFile) Read([]byte) (int, error) { return 0, f.cause }
func (f *readFailureFile) Close() error {
	*f.closes = *f.closes + 1
	return f.File.Close()
}

func TestReadErrorsPreserveCauseAndCloseFile(t *testing.T) {
	closes := 0
	f := sourceFixture(t, readFailureFS{
		FS:     fstest.MapFS{"demo/SKILL.md": document("demo")},
		closes: &closes, cause: io.ErrUnexpectedEOF,
	})
	if _, err := f.LoadInstructions(t.Context(), "demo"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("underlying read error was lost: %v", err)
	}
	if closes != 1 {
		t.Fatalf("closed %d files, want 1", closes)
	}
}

func sourceFixture(t *testing.T, files fs.FS) skill.Source {
	t.Helper()
	return skill.NewFileSystemSource(files)
}

func TestFileSystemSourceSubpaths(t *testing.T) {
	source := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":                  document("demo"),
		"demo/assets/template":           {},
		"demo/references/guide.md":       {},
		"demo/references/nested/tips.md": {},
	})
	all := []string{"assets/template", "references/guide.md", "references/nested/tips.md"}
	for _, tc := range []struct {
		path string
		want []string
		err  error
	}{
		{"", all, nil}, {".", all, nil},
		{"references", all[1:], nil},
		{"references/nested", all[2:], nil},
		{"references/guide.md", all[1:2], nil},
		{"scripts", nil, skill.ErrResourceNotFound},
		{"references/missing", nil, skill.ErrResourceNotFound},
		{"../references", nil, skill.ErrInvalidPath},
		{"references/../assets", nil, skill.ErrInvalidPath},
		{"references//nested", nil, skill.ErrInvalidPath},
		{"references/", nil, skill.ErrInvalidPath},
		{"references\\guide.md", nil, skill.ErrInvalidPath},
		{"SKILL.md", nil, skill.ErrInvalidPath},
		{"/assets", nil, skill.ErrInvalidPath},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := source.ListResources(t.Context(), "demo", tc.path)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("list %q = %v, %v; want %v, %v", tc.path, got, err, tc.want, tc.err)
			}
		})
	}
}

// namedReads exercises every operation that must distinguish a missing skill
// from a missing resource and honor an already canceled context.
func namedReads(source skill.Source) map[string]func(context.Context, string) error {
	return map[string]func(context.Context, string) error{
		"frontmatter": func(ctx context.Context, name string) error {
			_, err := source.LoadFrontmatter(ctx, name)
			return err
		},
		"instructions": func(ctx context.Context, name string) error {
			_, err := source.LoadInstructions(ctx, name)
			return err
		},
		"resources": func(ctx context.Context, name string) error {
			_, err := source.ListResources(ctx, name, "references/missing")
			return err
		},
		"resource": func(ctx context.Context, name string) error {
			stream, err := source.LoadResource(ctx, name, "references/missing")
			if stream != nil {
				stream.Close()
			}
			return err
		},
	}
}

func TestFileSystemSourceErrors(t *testing.T) {
	source := sourceFixture(t, fstest.MapFS{"demo/SKILL.md": document("demo")})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, read := range namedReads(source) {
		t.Run(name, func(t *testing.T) {
			if err := read(t.Context(), "missing"); !errors.Is(err, skill.ErrSkillNotFound) || !errors.Is(err, fs.ErrNotExist) || errors.Is(err, skill.ErrResourceNotFound) {
				t.Fatalf("missing skill classification: %v", err)
			}
			if err := read(ctx, "demo"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read: %v", err)
			}
			if name == "resources" || name == "resource" {
				if err := read(t.Context(), "demo"); !errors.Is(err, skill.ErrResourceNotFound) || !errors.Is(err, fs.ErrNotExist) || errors.Is(err, skill.ErrSkillNotFound) {
					t.Fatalf("missing resource classification: %v", err)
				}
			}
		})
	}
	if _, err := source.ListFrontmatters(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled listing: %v", err)
	}
}

func TestFileSystemSourceRawStream(t *testing.T) {
	// Raw access can serve binary assets and leaves text policy to the consumer.
	content := strings.Repeat("\x00\xff", 1<<20)
	source := sourceFixture(t, fstest.MapFS{
		"demo/SKILL.md":    document("demo"),
		"demo/assets/file": {Data: []byte(content)},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := source.LoadResource(ctx, "demo", "assets/file")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	got, err := io.ReadAll(stream)
	if err != nil || string(got) != content {
		t.Fatalf("raw read = %d bytes, %v", len(got), err)
	}
	cancel()
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream ignored cancellation: %v", err)
	}
}

func TestFileSystemSourceDoesNotHideReadErrors(t *testing.T) {
	closes := 0
	source := sourceFixture(t, readFailureFS{
		FS: fstest.MapFS{"demo/SKILL.md": document("demo")}, closes: &closes, cause: fs.ErrNotExist,
	})
	// An error reading an already opened skill is not an absent skill and must
	// not be skipped during discovery or by a merged source.
	if _, err := source.ListFrontmatters(t.Context()); !errors.Is(err, fs.ErrNotExist) || errors.Is(err, skill.ErrSkillNotFound) {
		t.Fatalf("discovery hid a read failure: %v", err)
	}
	if closes == 0 {
		t.Fatal("read failure did not close the file")
	}
}
