// Package skilltoolset provides skill tools and model instructions.
package skilltoolset

import (
	"context"
	"encoding/xml"
	"fmt"
	"slices"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset/internal/skilltool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

const defaultName = "SkillToolset"

// Config selects the skill source and toolset options.
type Config struct {
	Source skill.Source
	// Name defaults to "SkillToolset".
	Name string
	// SystemInstruction replaces the default guidance when non-empty.
	SystemInstruction string
}

// Toolset groups skill tools and instructions for model requests.
type Toolset struct {
	name              string
	source            skill.Source
	tools             []tool.Tool
	systemInstruction string
}

// New constructs the tools without reading skill content.
func New(cfg Config) (*Toolset, error) {
	if cfg.Source == nil {
		return nil, fmt.Errorf("skilltoolset: source is required")
	}
	if cfg.Name == "" {
		cfg.Name = defaultName
	}
	if cfg.SystemInstruction == "" {
		cfg.SystemInstruction = defaultSystemInstruction
	}
	constructors := []func(skill.Source) (tool.Tool, error){
		skilltool.ListSkills, skilltool.LoadSkill, skilltool.LoadSkillResource,
	}
	ts := &Toolset{
		name:              cfg.Name,
		source:            cfg.Source,
		systemInstruction: cfg.SystemInstruction,
	}
	for _, newTool := range constructors {
		t, err := newTool(cfg.Source)
		if err != nil {
			return nil, fmt.Errorf("skilltoolset: create tool: %w", err)
		}
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

const defaultSystemInstruction = "Skills provide instructions and reference material for specialized tasks.\n" +
	"The available_skills catalog below contains skill names and descriptions.\n" +
	"Use `" + skilltool.ListName + "` to list the available skills.\n" +
	"When a skill is relevant, call `" + skilltool.LoadName + "` with name=\"<skill name>\" to read its instructions before using it.\n" +
	"Follow the loaded instructions within the task and the tools provided by the application.\n" +
	"The load result includes resource paths. Read a needed resource using `" + skilltool.ResourceName + "` with name=\"<skill name>\" and path=\"<resource path>\".\n" +
	"Resources are returned as text. Reading a script does not run it, and skill metadata does not grant tools or permissions.\n"

// Instructions returns usage guidance and an escaped catalog of names and descriptions.
// Add it to each model request. An empty catalog returns an empty string.
func (ts *Toolset) Instructions(ctx context.Context) (string, error) {
	frontmatters, err := ts.source.ListFrontmatters(ctx)
	if err != nil {
		return "", err
	}
	if len(frontmatters) == 0 {
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
	for _, m := range frontmatters {
		catalog.Skills = append(catalog.Skills, entry{Name: m.Name, Description: m.Description})
	}
	data, err := xml.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("skilltoolset: encode catalog: %w", err)
	}
	return ts.systemInstruction + "\n" + string(data), nil
}

// Name returns the toolset name.
func (ts *Toolset) Name() string { return ts.name }

// Tools returns list_skills, load_skill, and load_skill_resource, in that order.
// The returned slice is a copy.
func (ts *Toolset) Tools() []tool.Tool { return slices.Clone(ts.tools) }
