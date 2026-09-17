package skilltool_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset/internal/skilltool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

type unreadableFS struct{ calls int }

func (f *unreadableFS) Open(string) (fs.File, error) { f.calls++; return nil, fs.ErrPermission }

func TestBadArgumentsFailBeforeFileAccess(t *testing.T) {
	files := &unreadableFS{}
	reader, err := skill.NewFileSystem(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		newTool func(*skill.FileSystem) (tool.Tool, error)
		inputs  []string
	}{
		{skilltool.ListSkills, []string{`null`, `[]`, `{"name":"demo"}`, `{} {}`}},
		{skilltool.LoadSkill, []string{`{}`, `{"name":1}`, `{"skill_name":"demo"}`, `{"name":"demo","extra":true}`}},
		{skilltool.LoadSkillResource, []string{`{"name":"demo"}`, `{"name":"demo","path":null}`, `{"skill_name":"demo","resource_path":"assets/file"}`}},
	} {
		wrapped, err := tc.newTool(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := wrapped.Definition().Validate(); err != nil {
			t.Fatal(err)
		}
		for _, input := range tc.inputs {
			if _, err := wrapped.Execute(t.Context(), json.RawMessage(input)); err == nil {
				t.Errorf("%s accepted %s", wrapped.Definition().Name, input)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := wrapped.Execute(ctx, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation: %v", err)
		}
	}
	if files.calls != 0 {
		t.Fatalf("invalid arguments reached filesystem %d times", files.calls)
	}
}

func TestToolErrorsPreserveCauses(t *testing.T) {
	reader, err := skill.NewFileSystem(fstest.MapFS{"demo/SKILL.md": {Data: []byte("---\nname: demo\ndescription: Demo\n---\nInstructions")}})
	if err != nil {
		t.Fatal(err)
	}
	load, err := skilltool.LoadSkill(reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := load.Execute(t.Context(), json.RawMessage(`{"name":"missing"}`)); !errors.Is(err, skill.ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing skill error = %v", err)
	}
	resource, err := skilltool.LoadSkillResource(reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Execute(t.Context(), json.RawMessage(`{"name":"demo","path":"../secret"}`)); !errors.Is(err, skill.ErrInvalidPath) {
		t.Fatalf("invalid path error = %v", err)
	}
}
