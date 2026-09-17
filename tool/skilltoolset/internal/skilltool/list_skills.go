// Package skilltool builds the model-facing tools used by skilltoolset.
package skilltool

import (
	"context"
	"encoding/xml"

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

func ListSkills(source skill.Source) (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        ListName,
			Description: "List available skills with their names and descriptions.",
		}, func(ctx context.Context, _ struct{}) (ListResult, error) {
			return listSkills(ctx, source)
		})
}

func listSkills(ctx context.Context, source skill.Source) (ListResult, error) {
	frontmatters, err := source.ListFrontmatters(ctx)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Skills: []Summary{}}
	for _, m := range frontmatters {
		result.Skills = append(result.Skills, Summary{Name: m.Name, Description: m.Description})
	}
	return result, nil
}

// SkillsToXML formats skill names and descriptions as an escaped catalog.
func SkillsToXML(frontmatters []skill.Frontmatter) (string, error) {
	type entry struct {
		Name        string `xml:"name"`
		Description string `xml:"description"`
	}
	catalog := struct {
		XMLName xml.Name `xml:"available_skills"`
		Skills  []entry  `xml:"skill"`
	}{}
	for _, fm := range frontmatters {
		catalog.Skills = append(catalog.Skills, entry{Name: fm.Name, Description: fm.Description})
	}
	data, err := xml.MarshalIndent(catalog, "", "  ")
	return string(data), err
}
