package llmagent

import (
	"fmt"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/tool"
)

func registerTools(tools []tool.Tool) ([]model.ToolDefinition, map[string]tool.Tool, error) {
	var definitions []model.ToolDefinition
	byName := make(map[string]tool.Tool, len(tools))
	for i, t := range tools {
		if t == nil {
			return nil, nil, fmt.Errorf("llmagent: tools[%d] must not be nil", i)
		}
		definition := t.Definition()
		if _, exists := byName[definition.Name]; exists {
			return nil, nil, fmt.Errorf("llmagent: duplicate tool name %q", definition.Name)
		}
		definitions = append(definitions, definition)
		byName[definition.Name] = t
	}
	return definitions, byName, nil
}
