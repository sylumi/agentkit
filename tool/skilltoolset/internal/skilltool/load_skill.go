package skilltool

import (
	"context"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

const LoadName = "load_skill"

type LoadArgs struct {
	Name string `json:"name" jsonschema:"The name of the skill to load."`
}

func LoadSkill(source skill.Source) (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        LoadName,
			Description: "Load a skill's instructions, frontmatter, and resource paths. Resource contents are loaded separately.",
		}, func(ctx context.Context, args LoadArgs) (skill.Skill, error) {
			return loadSkill(ctx, args, source)
		})
}

func loadSkill(ctx context.Context, args LoadArgs, source skill.Source) (skill.Skill, error) {
	frontmatter, err := source.LoadFrontmatter(ctx, args.Name)
	if err != nil {
		return skill.Skill{}, err
	}
	instructions, err := source.LoadInstructions(ctx, args.Name)
	if err != nil {
		return skill.Skill{}, err
	}
	resources, err := source.ListResources(ctx, args.Name, "")
	if err != nil {
		return skill.Skill{}, err
	}
	if resources == nil {
		resources = []string{}
	}
	return skill.Skill{Frontmatter: frontmatter, Instructions: instructions, Resources: resources}, nil
}
