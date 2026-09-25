// Run with DEEPSEEK_API_KEY exported: go run ./examples/assistant web
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/cmd/launcher/full"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/modelcatalog"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
)

func main() {
	ctx := context.Background()
	llm, err := openaimodel.NewModel(openaimodel.Config{
		Model:   modelcatalog.MustLookup("deepseek", "deepseek-v4-flash"),
		BaseURL: "https://api.deepseek.com/",
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	currentTime, err := functiontool.New(functiontool.Config{
		Name:        "get_current_time",
		Description: "Get the current date and time on the server, including its UTC offset.",
	}, func(context.Context, struct{}) (string, error) {
		return time.Now().Format(time.RFC3339), nil
	})
	if err != nil {
		log.Fatal(err)
	}

	thinking, maxOutput := true, int64(4096)
	a, err := llmagent.New(llmagent.Config{
		MaxModelCalls: 10,
		Name:          "assistant",
		Model:         llm,
		Instruction:   "You are a helpful assistant. Answer in the user's language. Use get_current_time when you need the current date or time.",
		Tools:         []tool.Tool{currentTime},
		GenerateConfig: &model.GenerateConfig{
			MaxOutputTokens: &maxOutput,
			Reasoning:       &model.ReasoningConfig{Enabled: &thinking, Effort: "high"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := full.Run(ctx, launcher.Config{Agent: a}, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
