package skill_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func TestParseFrontmatter(t *testing.T) {
	const input = "---\nname: report\ndescription: |\n  Prepare a report.\n  Include references.\nlicense: MIT\ncompatibility: Requires Go\nmetadata:\n  owner: team\nallowed-tools: Read Bash(git status)\nfuture-field: ignored\n---\n\n# Report\n\nPreserve this body.\n---\n"
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "LF", "\r\n": "CRLF"}[newline], func(t *testing.T) {
			got, err := skill.Parse([]byte(strings.ReplaceAll(input, "\n", newline)))
			if err != nil {
				t.Fatal(err)
			}
			want := skill.Frontmatter{
				Name: "report", Description: "Prepare a report.\nInclude references.\n",
				License: "MIT", Compatibility: "Requires Go", Metadata: map[string]string{"owner": "team"},
				AllowedTools: []string{"Read", "Bash(git status)"},
			}
			if !reflect.DeepEqual(got.Frontmatter, want) {
				t.Fatalf("frontmatter = %#v, want %#v", got.Frontmatter, want)
			}
			body := strings.ReplaceAll("\n# Report\n\nPreserve this body.\n---\n", "\n", newline)
			if got.Instructions != body || len(got.Resources) != 0 {
				t.Fatalf("body changed: %#v", got)
			}
		})
	}
}

func TestAllowedToolsFormats(t *testing.T) {
	for _, field := range []string{
		"allowed-tools: Read Bash(git status), Bash(jq:*)\n",
		"allowed-tools:\n  - Read\n  - Bash(git status)\n  - Bash(jq:*)\n",
	} {
		doc, err := skill.Parse([]byte("---\nname: demo\ndescription: Demo\n" + field + "---\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(doc.Frontmatter.AllowedTools, []string{"Read", "Bash(git status)", "Bash(jq:*)"}) {
			t.Fatalf("allowed tools = %v", doc.Frontmatter.AllowedTools)
		}
	}
}

func TestParseRejectsInvalidSkills(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"no frontmatter", "# Demo"},
		{"no closing delimiter", "---\nname: demo\ndescription: Demo\n"},
		{"missing name", "---\ndescription: Demo\n---\n"},
		{"missing description", "---\nname: demo\n---\n"},
		{"numeric name", "---\nname: 123\ndescription: Demo\n---\n"},
		{"boolean description", "---\nname: demo\ndescription: true\n---\n"},
		{"non-string metadata", "---\nname: demo\ndescription: Demo\nmetadata:\n  count: 1\n---\n"},
		{"trailing YAML", "---\nname: demo\ndescription: Demo\n...\nunexpected\n---\n"},
		{"blank description", "---\nname: demo\ndescription: ' '\n---\n"},
		{"duplicate name", "---\nname: demo\nname: other\ndescription: Demo\n---\n"},
		{"non-object YAML", "---\n- demo\n---\n"},
		{"bad YAML", "---\nname: [\n---\n"},
		{"bad allowed tools", "---\nname: demo\ndescription: Demo\nallowed-tools: Bash(git\n---\n"},
		{"non-string allowed tools", "---\nname: demo\ndescription: Demo\nallowed-tools: [1]\n---\n"},
		{"invalid UTF-8", "---\nname: demo\ndescription: Demo\n---\n\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := skill.Parse([]byte(tc.input)); !errors.Is(err, skill.ErrInvalidSkill) {
				t.Fatalf("error = %v, want invalid skill", err)
			}
		})
	}
	for _, name := range []string{"Demo", "../demo", "-demo", "demo-", "demo--name", strings.Repeat("a", 65)} {
		_, err := skill.Parse([]byte("---\nname: " + name + "\ndescription: Demo\n---\n"))
		if !errors.Is(err, skill.ErrInvalidSkill) {
			t.Fatalf("accepted invalid name %q: %v", name, err)
		}
	}
	if _, err := skill.Parse([]byte(strings.Repeat("x", (256<<10)+1))); !errors.Is(err, skill.ErrTooLarge) {
		t.Fatalf("oversized document: %v", err)
	}
}
