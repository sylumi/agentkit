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

func fixture(t *testing.T, files fs.FS) *skill.FileSystem {
	t.Helper()
	f, err := skill.NewFileSystem(files)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFileSystemDiscoveryAndResources(t *testing.T) {
	files := fstest.MapFS{
		"zeta/SKILL.md": document("zeta"), "alpha/SKILL.md": document("alpha"),
		"alpha/references/guide.md":        {Data: []byte("Guidance.")},
		"alpha/assets/nested/template.txt": {Data: []byte("Template.")},
		"alpha/scripts/run.sh":             {Data: []byte("echo example")},
		"alpha/private.txt":                {Data: []byte("Not a resource.")},
		"Not-A-Skill/notes.txt":            {Data: []byte("Ignored.")},
	}
	f := fixture(t, files)
	metadata, err := f.List(t.Context())
	if err != nil || len(metadata) != 2 || metadata[0].Name != "alpha" || metadata[1].Name != "zeta" {
		t.Fatalf("list = %#v, error = %v", metadata, err)
	}
	doc, err := f.Load(t.Context(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Instructions != "Read references/guide.md.\n" || !reflect.DeepEqual(doc.Resources, []string{"assets/nested/template.txt", "references/guide.md", "scripts/run.sh"}) {
		t.Fatalf("skill = %#v", doc)
	}
	for resource, want := range map[string]string{"references/guide.md": "Guidance.", "assets/nested/template.txt": "Template.", "scripts/run.sh": "echo example"} {
		content, err := f.ReadResource(t.Context(), "alpha", resource)
		if err != nil || content != want {
			t.Fatalf("read %s = %q, %v", resource, content, err)
		}
	}
	metadata[0].Attributes["owner"] = "changed"
	doc.Metadata.Attributes["owner"] = "changed"
	doc.Resources[0] = "changed"
	again, err := f.Load(t.Context(), "alpha")
	if err != nil || again.Metadata.Attributes["owner"] != "team" || again.Resources[0] != "assets/nested/template.txt" {
		t.Fatalf("caller edits affected future loads: %+v, %v", again, err)
	}
}

func TestFileSystemInvalidPathsAndFiles(t *testing.T) {
	f := fixture(t, fstest.MapFS{
		"demo/SKILL.md":          document("demo"),
		"demo/references/binary": {Data: []byte{0, 255}},
		"demo/references/large":  {Data: []byte(strings.Repeat("x", (1<<20)+1))},
		"demo/references/empty":  {},
	})
	for _, name := range []string{"", "../demo", "demo/../demo", "/demo", "demo\\name"} {
		if _, err := f.Load(t.Context(), name); !errors.Is(err, skill.ErrInvalidPath) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, resource := range []string{"", "../SKILL.md", "/references/file", "references/../SKILL.md", "references//file", "references\\file", "SKILL.md", "private.txt", "references"} {
		if _, err := f.ReadResource(t.Context(), "demo", resource); !errors.Is(err, skill.ErrInvalidPath) {
			t.Errorf("resource %q: %v", resource, err)
		}
	}
	for _, tc := range []struct {
		path string
		want error
	}{
		{"references/missing", skill.ErrNotFound}, {"references/binary", skill.ErrInvalidSkill}, {"references/large", skill.ErrTooLarge},
	} {
		if _, err := f.ReadResource(t.Context(), "demo", tc.path); !errors.Is(err, tc.want) {
			t.Errorf("resource %q: %v, want %v", tc.path, err, tc.want)
		}
	}
	if got, err := f.ReadResource(t.Context(), "demo", "references/empty"); err != nil || got != "" {
		t.Fatalf("empty resource = %q, %v", got, err)
	}
	if _, err := f.Load(t.Context(), "missing"); !errors.Is(err, skill.ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing skill lost error classification: %v", err)
	}
	bad := fixture(t, fstest.MapFS{"demo/SKILL.md": document("other")})
	if _, err := bad.List(t.Context()); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("mismatched directory: %v", err)
	}
	if _, err := skill.NewFileSystem(nil); err == nil {
		t.Fatal("accepted nil filesystem")
	}
	for _, name := range []string{"demo/references", "demo/references/bad\\name.txt"} {
		invalid := fixture(t, fstest.MapFS{"demo/SKILL.md": document("demo"), name: {Data: []byte("text")}})
		if _, err := invalid.Load(t.Context(), "demo"); !errors.Is(err, skill.ErrInvalidPath) {
			t.Fatalf("listed an unreadable resource %q: %v", name, err)
		}
	}
}

func TestLazyResourceContentsAndDocumentLimit(t *testing.T) {
	// A resource can be too large or binary without preventing discovery or
	// loading the instructions. Its contents are checked only when requested.
	files := fstest.MapFS{"demo/SKILL.md": document("demo"), "demo/assets/large": {Data: make([]byte, (1<<20)+1)}}
	f := fixture(t, files)
	if _, err := f.List(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Load(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	files["demo/SKILL.md"].Data = append(document("demo").Data, []byte(strings.Repeat("x", 256<<10))...)
	if _, err := f.List(t.Context()); err != nil {
		t.Fatalf("catalog read the full body: %v", err)
	}
	if _, err := f.Load(t.Context(), "demo"); !errors.Is(err, skill.ErrTooLarge) {
		t.Fatalf("large instructions: %v", err)
	}
}

func TestFileSystemCancellationAndConcurrentReads(t *testing.T) {
	f := fixture(t, fstest.MapFS{"demo/SKILL.md": document("demo"), "demo/references/guide.md": {Data: []byte("Guide")}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.Load(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.ReadResource(ctx, "demo", "references/guide.md"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			doc, err := f.Load(t.Context(), "demo")
			if err != nil {
				t.Error(err)
				return
			}
			doc.Metadata.Attributes["owner"] = "caller"
			if _, err := f.ReadResource(t.Context(), "demo", "references/guide.md"); err != nil {
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
			f := fixture(t, os.DirFS(rootDir))
			if _, err := f.ReadResource(t.Context(), "demo", "references/secret.txt"); !errors.Is(err, skill.ErrInvalidPath) {
				t.Fatalf("read symlink: %v", err)
			}
			if _, err := f.Load(t.Context(), "demo"); !errors.Is(err, skill.ErrInvalidPath) {
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
	f := fixture(t, readFailureFS{
		FS:     fstest.MapFS{"demo/SKILL.md": document("demo")},
		closes: &closes, cause: io.ErrUnexpectedEOF,
	})
	if _, err := f.Load(t.Context(), "demo"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("underlying read error was lost: %v", err)
	}
	if closes != 1 {
		t.Fatalf("closed %d files, want 1", closes)
	}
}
