package skilltoolset_test

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool/skilltoolset"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func TestProcessRequestCatalogEscapingAndOrdering(t *testing.T) {
	const description = `Compare <one> & "two"; </description><skill> is literal text.`
	source := skill.NewFileSystemSource(fstest.MapFS{
		"zeta/SKILL.md":  {Data: []byte("---\nname: zeta\ndescription: Zeta\n---\nHidden body")},
		"alpha/SKILL.md": {Data: []byte("---\nname: alpha\ndescription: '" + description + "'\n---\nHidden body")},
	})
	ts, err := skilltoolset.New(skilltoolset.Config{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	var req model.Request
	if err := ts.ProcessRequest(t.Context(), &req); err != nil {
		t.Fatal(err)
	}
	text := req.Instructions
	start := strings.Index(text, "<available_skills>")
	if start < 0 {
		t.Fatal("missing catalog")
	}
	var decoded struct {
		Skills []struct {
			Name        string `xml:"name"`
			Description string `xml:"description"`
		} `xml:"skill"`
	}
	if err := xml.Unmarshal([]byte(text[start:]), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Skills) != 2 || decoded.Skills[0].Name != "alpha" || decoded.Skills[0].Description != description || decoded.Skills[1].Name != "zeta" {
		t.Fatalf("catalog = %+v", decoded)
	}
	if strings.Contains(text, "Hidden body") {
		t.Fatal("catalog includes skill body")
	}
	var again model.Request
	if err := ts.ProcessRequest(t.Context(), &again); err != nil || again.Instructions != text {
		t.Fatalf("instructions accumulated across calls: %v", err)
	}
}

func TestInstructionsMatchToolParameters(t *testing.T) {
	ts := newToolset(t)
	var req model.Request
	if err := ts.ProcessRequest(t.Context(), &req); err != nil {
		t.Fatal(err)
	}
	text := req.Instructions
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, implementation := range tools {
		definition := implementation.Definition()
		if !strings.Contains(text, "`"+definition.Name+"`") {
			t.Errorf("instructions omit %s", definition.Name)
		}
		var schema struct {
			Properties map[string]json.RawMessage
			Required   []string
		}
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, parameter := range schema.Required {
			if !strings.Contains(text, parameter+"=\"") {
				t.Errorf("instructions omit %s parameter %s", definition.Name, parameter)
			}
			if _, ok := schema.Properties[parameter]; !ok {
				t.Errorf("missing property %s", parameter)
			}
		}
	}
	if strings.Contains(text, "skill_name=") || strings.Contains(text, "resource_path=") {
		t.Fatal("instructions use obsolete parameter names")
	}
}

func TestEmptyCatalog(t *testing.T) {
	ts, err := skilltoolset.New(skilltoolset.Config{Source: skill.NewFileSystemSource(fstest.MapFS{})})
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"", "Keep these instructions."} {
		req := model.Request{Instructions: base}
		if err := ts.ProcessRequest(t.Context(), &req); err != nil || req.Instructions != base {
			t.Fatalf("empty catalog changed instructions: %q, %v", req.Instructions, err)
		}
	}
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || content != `{"skills":[]}` {
		t.Fatalf("empty list = %q, %v", content, err)
	}
}

func TestCustomConfiguration(t *testing.T) {
	const guidance = "Load a relevant skill before answering."
	ts, err := skilltoolset.New(skilltoolset.Config{
		Source:            memorySource{},
		Name:              "ProjectSkills",
		SystemInstruction: guidance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Name() != "ProjectSkills" {
		t.Fatalf("custom name = %q", ts.Name())
	}
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"list_skills", "load_skill", "load_skill_resource"} {
		if got := tools[i].Definition().Name; got != want {
			t.Fatalf("tool name = %q, want %q", got, want)
		}
	}
	var req model.Request
	if err := ts.ProcessRequest(t.Context(), &req); err != nil {
		t.Fatal(err)
	}
	text := req.Instructions
	prefix, catalog, found := strings.Cut(text, "<available_skills>")
	if !found || prefix != guidance+"\n" {
		t.Fatalf("custom guidance = %q", text)
	}
	var decoded struct {
		Skills []struct {
			Name        string `xml:"name"`
			Description string `xml:"description"`
		} `xml:"skill"`
	}
	if err := xml.Unmarshal([]byte("<available_skills>"+catalog), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Skills) != 1 || decoded.Skills[0].Name != "demo" || decoded.Skills[0].Description != "A <custom> skill" {
		t.Fatalf("custom guidance changed the catalog: %+v", decoded)
	}
	var again model.Request
	if err := ts.ProcessRequest(t.Context(), &again); err != nil || again.Instructions != text {
		t.Fatalf("custom instructions accumulated across calls: %v", err)
	}
	empty, err := skilltoolset.New(skilltoolset.Config{Source: skill.NewFileSystemSource(fstest.MapFS{}), SystemInstruction: guidance})
	if err != nil {
		t.Fatal(err)
	}
	emptyReq := model.Request{Instructions: "Keep these instructions."}
	if err := empty.ProcessRequest(t.Context(), &emptyReq); err != nil || emptyReq.Instructions != "Keep these instructions." {
		t.Fatalf("empty catalog with custom guidance = %q, %v", emptyReq.Instructions, err)
	}
}

func TestProcessRequestPreservesBaseRequest(t *testing.T) {
	ts := newToolset(t)
	tools, err := ts.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	maxOutputTokens := int64(128)
	base := model.Request{
		Instructions: "Answer briefly.",
		Messages:     []model.Message{{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("Welcome Maya.")}}},
		Tools:        []model.ToolDefinition{tools[0].Definition()},
		Config:       &model.GenerateConfig{MaxOutputTokens: &maxOutputTokens},
	}
	before, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for range 2 {
		req := base
		if err := ts.ProcessRequest(t.Context(), &req); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(req.Instructions, base.Instructions+"\n\n") || strings.Count(req.Instructions, "<available_skills>") != 1 {
			t.Fatalf("base instructions or catalog changed: %q", req.Instructions)
		}
		if first == "" {
			first = req.Instructions
		} else if req.Instructions != first {
			t.Fatal("instructions accumulated across requests")
		}
		req.Instructions = base.Instructions
		after, err := json.Marshal(req)
		if err != nil || string(after) != string(before) {
			t.Fatalf("request fields changed: %s, error = %v", after, err)
		}
	}
	after, err := json.Marshal(base)
	if err != nil || string(after) != string(before) {
		t.Fatalf("base request changed: %s, error = %v", after, err)
	}
}
