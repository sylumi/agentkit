// Run from the repository root with DEEPSEEK_API_KEY set:
//
//	go run ./examples/basic
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/modelcatalog"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// DeepSeek uses the Responses API adapter with its own address and key.
	llm, err := openaimodel.NewModel(openaimodel.Config{
		Model:   modelcatalog.MustLookup("deepseek", "deepseek-v4-flash"),
		BaseURL: "https://api.deepseek.com/",
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		return err
	}

	// Supply the message for this call.
	maxOutputTokens := int64(4096)
	req := model.Request{
		Instructions: "Answer clearly in English.",
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("Introduce Golang.")}},
		},
		Config: &model.GenerateConfig{
			MaxOutputTokens: &maxOutputTokens,
			Reasoning:       &model.ReasoningConfig{Effort: "low"},
		},
	}
	// Ctrl+C cancels the request so the adapter can return its partial output.
	interruptCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interruptCtx, time.Minute)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	// Print streamed content, then the complete final message, usage, and metadata.
	for event, err := range llm.Generate(ctx, req, true) {
		if err != nil {
			var callErr *model.CallError
			if errors.As(err, &callErr) {
				fmt.Print("\n\n========== [Partial] ==========\n\n")
				if encodeErr := encoder.Encode(callErr.Partial); encodeErr != nil {
					return errors.Join(err, encodeErr)
				}
			}
			return err
		}
		switch e := event.(type) {
		case model.PartStart:
			switch e.Kind {
			case model.PartThinking:
				fmt.Print("\n========== [Thinking] ==========\n\n")
			case model.PartText:
				fmt.Print("\n========== [Answer] ==========\n\n")
			}
		case model.ThinkingDelta:
			fmt.Print(e.Delta)
		case model.TextDelta:
			fmt.Print(e.Delta)
		case model.PartEnd:
			fmt.Println()
		case model.ResultEvent:
			fmt.Print("\n========== [Result] ==========\n\n")
			if err := encoder.Encode(e.Result); err != nil {
				return err
			}
			if e.Result.StopReason != model.StopReasonStop {
				return fmt.Errorf("generation ended with %s", e.Result.StopReason)
			}
		}
	}
	return nil
}
