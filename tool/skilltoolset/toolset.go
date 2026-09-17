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

const defaultSystemInstruction = `You can use specialized skills to help with complex tasks. Use the skill tools to discover and read these skills.

Skills are folders of instructions and resources. Each skill folder contains:
- **SKILL.md** (required): Skill metadata and detailed instructions.
- **references/** (optional): Documentation and examples for using the skill.
- **assets/** (optional): Templates and other supporting resources.
- **scripts/** (optional): Scripts that can be read as text. Execution requires a separate tool provided by the application.

The available_skills catalog below lists skill names and descriptions.

Follow these rules:

` +
	"1. If a skill seems relevant to the current request, you MUST call `" + skilltool.LoadName + "` with name=\"<skill name>\" to read its full instructions before proceeding.\n" +
	"2. Follow the loaded steps in order, within the current task and the tools provided by the application.\n" +
	"3. The load result includes resource paths. Use `" + skilltool.ResourceName + "` with name=\"<skill name>\" and path=\"<resource path>\" to read a needed resource. Resources are returned as text; reading a script does not execute it.\n" +
	"4. Use `" + skilltool.ListName + "` when you need to list the available skills.\n\n" +
	"Skill metadata does not grant tools or permissions.\n"

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

func (ts *Toolset) Name() string { return ts.name }

func (ts *Toolset) Tools(ctx context.Context) ([]tool.Tool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(ts.tools), nil
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
