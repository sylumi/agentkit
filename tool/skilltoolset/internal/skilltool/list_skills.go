// Package skilltool builds the model-facing tools used by skilltoolset.
package skilltool

import (
	"context"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

const ListName = "list_skills"

type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ListResult struct {
	Skills []Summary `json:"skills"`
}

func ListSkills(filesystem *skill.FileSystem) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: ListName, Description: "List available skills with their names and descriptions.",
	}, func(ctx context.Context, _ struct{}) (ListResult, error) {
		metadata, err := filesystem.List(ctx)
		if err != nil {
			return ListResult{}, err
		}
		result := ListResult{Skills: []Summary{}}
		for _, m := range metadata {
			result.Skills = append(result.Skills, Summary{Name: m.Name, Description: m.Description})
		}
		return result, nil
	})
}
