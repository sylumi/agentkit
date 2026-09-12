package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
)

func main() {
	// 1. 创建 OpenAI 模型连接。
	m, err := openaimodel.NewModel(openaimodel.Config{
		Model:   os.Getenv("OPENAI_MODEL"),
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: "https://api.openai.com/v1/",
		// 可选配置通过 SDK 选项传入。
		Options: []option.RequestOption{
			option.WithOrganization(os.Getenv("OPENAI_ORG_ID")),
			option.WithProject(os.Getenv("OPENAI_PROJECT_ID")),
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// 2. 用公共 Request 构造一条用户消息。
	req := model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart("你好")}},
	}}

	// 3. 非流式生成，遍历事件并打印最终结果里的文本。
	for event, err := range m.Generate(context.Background(), req, false) {
		if err != nil {
			log.Fatal(err)
		}
		e, ok := event.(model.ResultEvent)
		if !ok || e.Result.Message == nil {
			continue
		}
		for _, part := range e.Result.Message.Parts {
			if part.Kind == model.PartText {
				fmt.Print(*part.Text)
			}
		}
	}
	fmt.Println()
}
