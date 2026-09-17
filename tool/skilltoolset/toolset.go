// Package skilltoolset exposes skill discovery, instruction loading, and text
// resource reading as ordinary tools. Callers add Instructions to each model
// request and dispatch tool calls using the tools returned by Tools.
package skilltoolset

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"slices"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset/internal/skilltool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

// Config selects a filesystem whose immediate subdirectories contain skills.
// The caller owns its lifetime. For local files requiring filesystem confinement,
// pass an os.Root's FS and keep the root open while the toolset is in use.
type Config struct {
	FS fs.FS
}

// Toolset groups three tools and the instructions needed to discover skills.
// It has no per-session state and supports concurrent calls when its filesystem
// does. It does not register itself with a model or execute a skill's workflow.
type Toolset struct {
	filesystem *skill.FileSystem
	tools      []tool.Tool
}

// New constructs the tools without reading the filesystem or making model calls.
func New(cfg Config) (*Toolset, error) {
	filesystem, err := skill.NewFileSystem(cfg.FS)
	if err != nil {
		return nil, fmt.Errorf("skilltoolset: %w", err)
	}
	constructors := []func(*skill.FileSystem) (tool.Tool, error){
		skilltool.ListSkills, skilltool.LoadSkill, skilltool.LoadSkillResource,
	}
	ts := &Toolset{filesystem: filesystem}
	for _, newTool := range constructors {
		t, err := newTool(filesystem)
		if err != nil {
			return nil, fmt.Errorf("skilltoolset: create tool: %w", err)
		}
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

// Tools returns list_skills, load_skill, and load_skill_resource, in that order.
// The slice is independent; tool implementations are shared and immutable.
func (ts *Toolset) Tools() []tool.Tool { return slices.Clone(ts.tools) }

const skillInstructions = "Skills provide instructions and reference material for specialized tasks.\n" +
	"The available_skills catalog below contains skill names and descriptions.\n" +
	"Use `" + skilltool.ListName + "` to list the available skills.\n" +
	"When a skill is relevant, call `" + skilltool.LoadName + "` with name=\"<skill name>\" to read its instructions before using it.\n" +
	"Follow the loaded instructions within the task and the tools provided by the application.\n" +
	"The load result includes resource paths. Read a needed resource using `" + skilltool.ResourceName + "` with name=\"<skill name>\" and path=\"<resource path>\".\n" +
	"Resources are returned as text. Reading a script does not run it, and skill metadata does not grant tools or permissions.\n"

// Instructions returns usage guidance and an escaped, sorted catalog containing
// only names and descriptions. No skill bodies or resource contents are included.
// An empty catalog returns an empty string. Combine this with the application's
// base instructions when building each Request, before starting Generate.
func (ts *Toolset) Instructions(ctx context.Context) (string, error) {
	metadata, err := ts.filesystem.List(ctx)
	if err != nil {
		return "", err
	}
	if len(metadata) == 0 {
		return "", nil
	}
	type entry struct {
		Name        string `xml:"name"`
		Description string `xml:"description"`
	}
	catalog := struct {
		XMLName xml.Name `xml:"available_skills"`
		Skills  []entry  `xml:"skill"`
	}{}
	for _, m := range metadata {
		catalog.Skills = append(catalog.Skills, entry{Name: m.Name, Description: m.Description})
	}
	data, err := xml.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("skilltoolset: encode catalog: %w", err)
	}
	return skillInstructions + "\n" + string(data), nil
}
