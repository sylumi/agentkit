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

func LoadSkill(filesystem *skill.FileSystem) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: LoadName, Description: "Load a skill's instructions, metadata, and resource paths. Resource contents are loaded separately.",
	}, func(ctx context.Context, args LoadArgs) (skill.Skill, error) {
		return filesystem.Load(ctx, args.Name)
	})
}
