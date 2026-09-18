// Run from the repository root with DEEPSEEK_API_KEY set:
//
//	go run ./examples/session
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
	"github.com/sylumi/agentkit/session"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	llm, err := openaimodel.NewModel(openaimodel.Config{
		Model:   modelcatalog.MustLookup("deepseek", "deepseek-v4-flash"),
		BaseURL: "https://api.deepseek.com/",
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	service := session.InMemoryService()
	created, err := service.Create(
		ctx,
		&session.CreateRequest{
			AppName: "session-example",
			UserID:  "user-1",
		})
	if err != nil {
		return err
	}
	sess := created.Session
	fmt.Printf("Session: %s\n", sess.ID())

	prompts := []string{
		"My name is Alex, and I am learning Go. Please remember this.",
		"What is my name, and what language am I learning?",
	}
	for i, prompt := range prompts {
		if err := runTurn(ctx, llm, service, sess, i+1, prompt); err != nil {
			return err
		}

		// Reload stored history for the next turn.
		loaded, err := service.Get(ctx, &session.GetRequest{
			AppName: sess.AppName(), UserID: sess.UserID(), SessionID: sess.ID(),
		})
		if err != nil {
			return err
		}
		sess = loaded.Session
	}

	fmt.Printf("\nStored events: %d\n", sess.Events().Len())
	for event := range sess.Events().All() {
		if event.Message != nil {
			fmt.Printf("  invocation=%s author=%s role=%s\n", event.InvocationID, event.Author, event.Message.Role)
		}
	}
	return nil
}

func runTurn(ctx context.Context, llm model.LLM, service session.Service, sess session.Session, turn int, prompt string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	input := session.NewEvent(fmt.Sprintf("run-%d", turn))
	input.Author = "user"
	input.Message = &model.Message{
		Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(prompt)},
	}
	if err := service.AppendEvent(ctx, sess, input); err != nil {
		return err
	}
	fmt.Printf("\nUser: %s\n", prompt)

	var messages []model.Message
	for event := range sess.Events().All() {
		if event.Message == nil {
			continue
		}
		// This text-only example omits thinking, which the adapter cannot replay.
		message := model.Message{Role: event.Message.Role}
		for _, part := range event.Message.Parts {
			if part.Kind == model.PartText {
				message.Parts = append(message.Parts, part)
			}
		}
		if len(message.Parts) > 0 {
			messages = append(messages, message)
		}
	}
	thinking := false
	maxOutputTokens := int64(1024)
	req := model.Request{
		Instructions: "Answer briefly in English using the conversation history.",
		Messages:     messages,
		Config: &model.GenerateConfig{
			MaxOutputTokens: &maxOutputTokens,
			Reasoning:       &model.ReasoningConfig{Enabled: &thinking},
		},
	}

	for output, err := range llm.Generate(ctx, req, false) {
		if err != nil {
			return err
		}
		result := output.(model.ResultEvent).Result
		reply := session.NewEvent(input.InvocationID)
		reply.Author = "chat-agent"
		reply.Message = result.Message
		reply.StopReason = result.StopReason
		reply.Usage = &result.Usage
		reply.Metadata = result.Metadata
		if err := service.AppendEvent(ctx, sess, reply); err != nil {
			return err
		}
		if result.Message != nil {
			fmt.Print("Assistant: ")
			for _, part := range result.Message.Parts {
				if part.Kind == model.PartText {
					fmt.Print(*part.Text)
				}
			}
			fmt.Println()
		}
		if result.StopReason != model.StopReasonStop {
			return fmt.Errorf("generation ended with %s", result.StopReason)
		}
	}
	return nil
}
