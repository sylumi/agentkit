package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/runner"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/skilltoolset"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

func TestRunSkillsToolset(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%t", streaming), func(t *testing.T) {
			source := skill.NewFileSystemSource(fstest.MapFS{
				"greeting/SKILL.md":                {Data: []byte("---\nname: greeting\ndescription: Greet someone\n---\nBODY_MARKER: Read references/greeting.txt.\n")},
				"greeting/references/greeting.txt": {Data: []byte("RESOURCE_MARKER: Hello!")},
			})
			skills, err := skilltoolset.New(skilltoolset.Config{Source: source})
			if err != nil {
				t.Fatal(err)
			}
			s := createSession(t)
			calls := 0
			var firstInstructions string
			a, err := llmagent.New(llmagent.Config{
				MaxModelCalls: 10,
				Name:          "skills", Instruction: "Help the user.", Toolsets: []tool.Toolset{skills},
				Model: modelFunc(func(_ context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
					calls++
					if stream != streaming {
						t.Fatal("streaming flag lost")
					}
					if err := req.Validate(); err != nil {
						t.Fatal(err)
					}
					if calls == 1 {
						firstInstructions = req.Instructions
					}
					if req.Instructions != firstInstructions || !strings.HasPrefix(req.Instructions, "Help the user.") ||
						strings.Count(req.Instructions, "<available_skills>") != 1 || !strings.Contains(req.Instructions, "<name>greeting</name>") {
						t.Fatalf("missing or accumulated instructions: %q", req.Instructions)
					}
					if strings.Contains(req.Instructions, "BODY_MARKER") || strings.Contains(req.Instructions, "RESOURCE_MARKER") {
						t.Fatal("skill contents loaded into system instructions")
					}
					if len(req.Tools) != 3 || req.Tools[0].Name != "list_skills" || req.Tools[1].Name != "load_skill" || req.Tools[2].Name != "load_skill_resource" {
						t.Fatalf("skill tools missing: %+v", req.Tools)
					}
					if len(req.Messages) != calls*2-1 || history(t, s).Len() != len(req.Messages) {
						t.Fatal("model called before complete history was saved")
					}
					if calls >= 2 {
						loaded := req.Messages[2].Parts[0].ToolResult
						if loaded == nil || loaded.IsError || loaded.CallID != "load" || !strings.Contains(loaded.Content, "BODY_MARKER") || strings.Contains(loaded.Content, "RESOURCE_MARKER") {
							t.Fatalf("skill load result: %+v", loaded)
						}
					}
					if calls == 3 {
						resource := req.Messages[4].Parts[0].ToolResult
						if resource == nil || resource.IsError || resource.CallID != "resource" || !strings.Contains(resource.Content, "RESOURCE_MARKER") {
							t.Fatalf("resource result: %+v", resource)
						}
					}
					result := model.Result{StopReason: model.StopReasonStop, Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello!")}}}
					if calls < 3 {
						call := &model.ToolCallPart{ID: "load", Name: "load_skill", Arguments: json.RawMessage(`{"name":"greeting"}`)}
						if calls == 2 {
							call = &model.ToolCallPart{ID: "resource", Name: "load_skill_resource", Arguments: json.RawMessage(`{"name":"greeting","path":"references/greeting.txt"}`)}
						}
						result.StopReason = model.StopReasonToolCalls
						result.Message.Parts = []model.Part{{Kind: model.PartToolCall, ToolCall: call}}
					}
					return func(yield func(model.Event, error) bool) {
						if stream {
							part := result.Message.Parts[0]
							var delta model.Event = model.TextDelta{Delta: "Hello!"}
							if call := part.ToolCall; call != nil {
								delta = model.ToolCallDelta{ID: call.ID, Name: call.Name, Arguments: string(call.Arguments)}
							}
							for _, event := range []model.Event{model.PartStart{Kind: part.Kind}, delta, model.PartEnd{}} {
								if !yield(event, nil) {
									return
								}
							}
						}
						yield(model.ResultEvent{Result: result}, nil)
					}
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			r, err := runner.New(runner.Config{AppName: "app", Agent: a, SessionService: s})
			if err != nil {
				t.Fatal(err)
			}
			partials, complete := 0, 0
			for event, err := range r.Run(t.Context(), "user", "session", userMessage("Greet me."), runner.WithStreaming(streaming)) {
				if err != nil {
					t.Fatal(err)
				}
				if event.Partial {
					partials++
				} else {
					complete++
				}
				if history(t, s).Len() != 1+complete {
					t.Fatal("partial saved or complete event not committed")
				}
			}
			wantPartials := 0
			if streaming {
				wantPartials = 9
			}
			if calls != 3 || complete != 5 || partials != wantPartials {
				t.Fatalf("calls=%d complete=%d partials=%d", calls, complete, partials)
			}
			stored := history(t, s)
			if stored.At(5).Message.Parts[0].Text == nil || *stored.At(5).Message.Parts[0].Text != "Hello!" {
				t.Fatal("final answer not saved")
			}
		})
	}
}
