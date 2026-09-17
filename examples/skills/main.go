// Run from the repository root with DEEPSEEK_API_KEY set:
//
//	go run ./examples/skills
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/modelcatalog"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	llm, err := openaimodel.NewModel(
		openaimodel.Config{
			Model:   modelcatalog.MustLookup("deepseek", "deepseek-v4-flash"),
			BaseURL: "https://api.deepseek.com/",
			APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		})
	if err != nil {
		return err
	}

	source := skill.NewFileSystemSource(os.DirFS("./examples/skills/skills"))
	skills, err := skilltoolset.New(skilltoolset.Config{Source: source})
	if err != nil {
		return err
	}

	interruptCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interruptCtx, 2*time.Minute)
	defer cancel()

	maxOutputTokens, thinking := int64(1024), false
	req := model.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			Parts: []model.Part{model.NewTextPart(
				"Use the welcome-message skill to welcome Maya to the Atlas engineering team.",
			)},
		}},
		Config: &model.GenerateConfig{
			MaxOutputTokens: &maxOutputTokens,
			Reasoning:       &model.ReasoningConfig{Enabled: &thinking},
		},
	}
	tools, err := skills.Tools(ctx)
	if err != nil {
		return err
	}
	registered := make(map[string]tool.Tool)
	for _, t := range tools {
		definition := t.Definition()
		req.Tools = append(req.Tools, definition)
		registered[definition.Name] = t
	}

	for turn := 1; turn <= 6; turn++ {
		instructions, err := skills.Instructions(ctx)
		if err != nil {
			return err
		}
		req.Instructions = "Answer in English and use relevant skills before answering.\n\n" + instructions

		// Non-streaming calls emit one complete ResultEvent.
		var result model.Result
		for event, err := range llm.Generate(ctx, req, false) {
			if err != nil {
				return err
			}
			if e, ok := event.(model.ResultEvent); ok {
				result = e.Result
			}
		}
		if err := result.Validate(); err != nil {
			return err
		}
		if result.StopReason != model.StopReasonStop && result.StopReason != model.StopReasonToolCalls {
			return fmt.Errorf("generation ended with %s", result.StopReason)
		}
		if result.Message == nil {
			return fmt.Errorf("model returned no message")
		}

		// The adapter supports replaying text and tool calls, but not thinking.
		assistant := model.Message{Role: model.RoleAssistant}
		for _, part := range result.Message.Parts {
			if part.Kind == model.PartText || part.Kind == model.PartToolCall {
				assistant.Parts = append(assistant.Parts, part)
			}
			if part.Kind == model.PartText {
				fmt.Println(*part.Text)
			}
		}
		if len(assistant.Parts) == 0 {
			return fmt.Errorf("model returned no text or tool calls")
		}
		if result.StopReason == model.StopReasonStop {
			return nil
		}
		req.Messages = append(req.Messages, assistant)

		for _, part := range assistant.Parts {
			if part.Kind != model.PartToolCall {
				continue
			}
			call := part.ToolCall
			t, ok := registered[call.Name]
			if !ok {
				return fmt.Errorf("unknown tool %q", call.Name)
			}
			fmt.Printf("[tool] %s %s\n", call.Name, call.Arguments)
			content, callErr := t.Execute(ctx, call.Arguments)
			if callErr != nil {
				content = callErr.Error()
			}
			fmt.Printf("[result] %s\n\n", content)
			req.Messages = append(req.Messages, model.Message{
				Role: model.RoleTool,
				Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{
					CallID: call.ID, Content: content, IsError: callErr != nil,
				}}},
			})
		}
	}
	return fmt.Errorf("model did not finish within 6 turns")
}
