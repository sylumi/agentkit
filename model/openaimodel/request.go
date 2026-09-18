package openaimodel

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/sylumi/agentkit/model"
)

// buildOpenAIParams converts a validated neutral request into independent SDK data.
func buildOpenAIParams(info model.ModelInfo, req model.Request) (responses.ResponseNewParams, error) {
	p := responses.ResponseNewParams{
		Model: shared.ResponsesModel(info.ID),
		Store: param.NewOpt(false),
	}
	if req.Instructions != "" {
		p.Instructions = param.NewOpt(req.Instructions)
	}
	if cfg := req.Config; cfg != nil {
		var err error
		p.Reasoning, err = mapReasoning(cfg.Reasoning, info)
		if err != nil {
			return p, fmt.Errorf("responses: model %q: %w", info.ID, err)
		}
		if cfg.MaxOutputTokens != nil {
			p.MaxOutputTokens = param.NewOpt(*cfg.MaxOutputTokens)
		}
		if choice := cfg.ToolChoice; choice != nil {
			switch choice.Mode {
			case model.ToolChoiceAuto, model.ToolChoiceNone, model.ToolChoiceRequired:
				p.ToolChoice.OfToolChoiceMode = param.NewOpt(responses.ToolChoiceOptions(choice.Mode))
			case model.ToolChoiceNamed:
				p.ToolChoice.OfFunctionTool = &responses.ToolChoiceFunctionParam{Name: choice.Name}
			default:
				return p, fmt.Errorf("responses: config.tool_choice.mode: unsupported mode %q", choice.Mode)
			}
		}
	}
	input := responses.ResponseInputParam{}
	for i, message := range req.Messages {
		items, err := mapMessageParts(message.Role, message.Parts)
		if err != nil {
			return p, fmt.Errorf("responses: messages[%d].%w", i, err)
		}
		input = append(input, items...)
	}
	p.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}
	for _, tool := range req.Tools {
		var schema map[string]any
		decoder := json.NewDecoder(bytes.NewReader(tool.InputSchema))
		decoder.UseNumber()
		if err := decoder.Decode(&schema); err != nil {
			return p, err
		}
		p.Tools = append(p.Tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name,
				Description: param.NewOpt(tool.Description),
				Parameters:  schema, Strict: param.NewOpt(false),
			}})
	}
	return p, nil
}

func mapMessageParts(role model.Role, parts []model.Part) (responses.ResponseInputParam, error) {
	var input responses.ResponseInputParam
	for j := 0; j < len(parts); j++ {
		switch part := parts[j]; part.Kind {
		case model.PartText, model.PartThinking:
			// Visible thinking is ordinary history text. Keep consecutive text and
			// thinking parts together without crossing tool or message boundaries.
			var content responses.ResponseInputMessageContentListParam
			for {
				text := ""
				if part.Kind == model.PartThinking {
					text = part.Thinking.Text
				} else {
					text = *part.Text
				}
				content = append(content, responses.ResponseInputContentParamOfInputText(text))
				if j+1 == len(parts) || (parts[j+1].Kind != model.PartText && parts[j+1].Kind != model.PartThinking) {
					break
				}
				j++
				part = parts[j]
			}
			input = append(input, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRole(role)))
		case model.PartToolCall:
			call := part.ToolCall
			input = append(input, responses.ResponseInputItemParamOfFunctionCall(string(call.Arguments), call.ID, call.Name))
		case model.PartToolResult:
			result := part.ToolResult
			output := result.Content
			if result.IsError {
				data, err := json.Marshal(struct {
					IsError bool   `json:"is_error"`
					Content string `json:"content"`
				}{true, result.Content})
				if err != nil {
					return nil, err
				}
				output = string(data)
			}
			item := responses.ResponseInputItemParamOfFunctionCallOutput(output)
			item.OfFunctionCallOutput.CallID = param.NewOpt(result.CallID)
			input = append(input, item)
		default:
			return nil, fmt.Errorf("parts[%d]: unsupported history kind %q", j, part.Kind)
		}
	}
	return input, nil
}
