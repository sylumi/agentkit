package skilltoolset_test

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/tool/skilltoolset"
)

func TestInstructionsCatalogEscapingAndOrdering(t *testing.T) {
	const description = `Compare <one> & "two"; </description><skill> is literal text.`
	ts, err := skilltoolset.New(skilltoolset.Config{FS: fstest.MapFS{
		"zeta/SKILL.md":  {Data: []byte("---\nname: zeta\ndescription: Zeta\n---\nHidden body")},
		"alpha/SKILL.md": {Data: []byte("---\nname: alpha\ndescription: '" + description + "'\n---\nHidden body")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := ts.Instructions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
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
	again, err := ts.Instructions(t.Context())
	if err != nil || again != text {
		t.Fatalf("instructions accumulated across calls: %v", err)
	}
}

func TestInstructionsMatchToolParameters(t *testing.T) {
	ts := newToolset(t)
	text, err := ts.Instructions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, implementation := range ts.Tools() {
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
	ts, err := skilltoolset.New(skilltoolset.Config{FS: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := ts.Instructions(t.Context()); err != nil || text != "" {
		t.Fatalf("empty instructions = %q, %v", text, err)
	}
	content, err := ts.Tools()[0].Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || content != `{"skills":[]}` {
		t.Fatalf("empty list = %q, %v", content, err)
	}
}
