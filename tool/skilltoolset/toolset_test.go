package skilltoolset_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func newToolset(t *testing.T) *skilltoolset.Toolset {
	t.Helper()
	ts, err := skilltoolset.New(skilltoolset.Config{FS: fstest.MapFS{
		"greeting/SKILL.md":                {Data: []byte("---\nname: greeting\ndescription: Greet someone\n---\nBODY_MARKER: Read references/greeting.txt.\n")},
		"greeting/references/greeting.txt": {Data: []byte("RESOURCE_MARKER: Hello!")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestProgressiveSkillWorkflow(t *testing.T) {
	ts := newToolset(t)
	instructions, err := ts.Instructions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	req := model.Request{
		Instructions: "You are a helpful assistant.\n" + instructions,
		Messages:     []model.Message{{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("Please greet me.")}}},
	}
	registered := map[string]tool.Tool{}
	for _, implementation := range ts.Tools() {
		definition := implementation.Definition()
		req.Tools = append(req.Tools, definition)
		registered[definition.Name] = implementation
	}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.Instructions, "BODY_MARKER") || strings.Contains(req.Instructions, "RESOURCE_MARKER") {
		t.Fatal("initial request includes undiscovered content")
	}

	for i, tc := range []struct{ tool, args string }{
		{"list_skills", `{}`},
		{"load_skill", `{"name":"greeting"}`},
		{"load_skill_resource", `{"name":"greeting","path":"references/greeting.txt"}`},
	} {
		call := model.ToolCallPart{ID: tc.tool, Name: tc.tool, Arguments: json.RawMessage(tc.args)}
		content, err := registered[call.Name].Execute(t.Context(), call.Arguments)
		if err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			if strings.Contains(content, "BODY_MARKER") || strings.Contains(content, "RESOURCE_MARKER") {
				t.Fatal("list leaked content")
			}
		case 1:
			var loaded skill.Skill
			if err := json.Unmarshal([]byte(content), &loaded); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(loaded.Instructions, "BODY_MARKER") || len(loaded.Resources) != 1 || loaded.Resources[0] != "references/greeting.txt" || strings.Contains(content, "RESOURCE_MARKER") {
				t.Fatalf("wrong instruction load: %s", content)
			}
		case 2:
			var resource struct{ Content string }
			if err := json.Unmarshal([]byte(content), &resource); err != nil {
				t.Fatal(err)
			}
			if resource.Content != "RESOURCE_MARKER: Hello!" {
				t.Fatalf("wrong resource: %s", content)
			}
		}
		// The same ordinary call/result messages can be passed to the next model
		// turn. The toolset does not own call IDs or alter the request/history.
		req.Messages = append(req.Messages,
			model.Message{Role: model.RoleAssistant, Parts: []model.Part{{Kind: model.PartToolCall, ToolCall: &call}}},
			model.Message{Role: model.RoleTool, Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: call.ID, Content: content}}}},
		)
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(req.Messages) != 7 {
		t.Fatalf("history length = %d", len(req.Messages))
	}
}

type unavailableFS struct{ reads int }

func (f *unavailableFS) Open(string) (fs.File, error) { f.reads++; return nil, fs.ErrPermission }

func TestConstructionAndOwnership(t *testing.T) {
	if _, err := skilltoolset.New(skilltoolset.Config{}); err == nil {
		t.Fatal("accepted nil filesystem")
	}
	files := &unavailableFS{}
	ts, err := skilltoolset.New(skilltoolset.Config{FS: files})
	if err != nil || files.reads != 0 {
		t.Fatalf("constructor read files: %v, %d", err, files.reads)
	}
	returned := ts.Tools()
	if len(returned) != 3 {
		t.Fatalf("tool count = %d", len(returned))
	}
	returned[0] = nil
	if ts.Tools()[0] == nil {
		t.Fatal("tool slice aliases internal storage")
	}
	if _, err := ts.Instructions(t.Context()); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("filesystem error lost: %v", err)
	}
}

func TestConcurrentToolCalls(t *testing.T) {
	ts := newToolset(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := ts.Instructions(t.Context()); err != nil {
				t.Error(err)
			}
			for i, args := range []string{`{}`, `{"name":"greeting"}`, `{"name":"greeting","path":"references/greeting.txt"}`} {
				if _, err := ts.Tools()[i].Execute(t.Context(), json.RawMessage(args)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ts.Instructions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
