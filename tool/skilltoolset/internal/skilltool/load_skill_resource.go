package skilltool

import (
	"context"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

const ResourceName = "load_skill_resource"

type ResourceArgs struct {
	Name string `json:"name" jsonschema:"The name of the skill that owns the resource."`
	Path string `json:"path" jsonschema:"The relative resource path returned by load_skill, such as references/guide.md."`
}

type ResourceResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func LoadSkillResource(filesystem *skill.FileSystem) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: ResourceName, Description: "Read a skill's UTF-8 text resource from references/, assets/, or scripts/. Reading a script does not execute it.",
	}, func(ctx context.Context, args ResourceArgs) (ResourceResult, error) {
		content, err := filesystem.ReadResource(ctx, args.Name, args.Path)
		if err != nil {
			return ResourceResult{}, err
		}
		return ResourceResult{Name: args.Name, Path: args.Path, Content: content}, nil
	})
}
