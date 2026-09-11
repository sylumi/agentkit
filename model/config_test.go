package model_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestGenerateConfigValidate(t *testing.T) {
	for _, tt := range []struct {
		name      string
		config    model.GenerateConfig
		wantField string
	}{
		{"defaults", model.GenerateConfig{}, ""},
		{"positive limit", model.GenerateConfig{MaxOutputTokens: tokenCount(1)}, ""},
		{"zero limit", model.GenerateConfig{MaxOutputTokens: tokenCount(0)}, "max_output_tokens"},
		{"negative limit", model.GenerateConfig{MaxOutputTokens: tokenCount(-1)}, "max_output_tokens"},
		{"empty choice", model.GenerateConfig{ToolChoice: &model.ToolChoice{}}, "tool_choice.mode"},
		{"unknown mode", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: "unknown"}}, "tool_choice.mode"},
		{"auto", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceAuto}}, ""},
		{"none", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNone}}, ""},
		{"required", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceRequired}}, ""},
		{"named", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "weather"}}, ""},
		{"empty name", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNamed}}, "tool_choice.name"},
		{"blank name", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNamed, Name: " \t"}}, "tool_choice.name"},
		{"auto with name", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceAuto, Name: "weather"}}, "tool_choice.name"},
		{"none with name", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceNone, Name: "weather"}}, "tool_choice.name"},
		{"required with name", model.GenerateConfig{ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceRequired, Name: "weather"}}, "tool_choice.name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.config)
			if err != nil {
				t.Fatal(err)
			}
			err = tt.config.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want field %s", err, tt.wantField)
			}
			after, err := json.Marshal(tt.config)
			if err != nil || string(before) != string(after) {
				t.Fatal("validation changed config")
			}
		})
	}
}

func TestRequestConfigToolAssociations(t *testing.T) {
	for _, tt := range []struct {
		name      string
		choice    model.ToolChoice
		withTools bool
		wantField string
	}{
		{"auto without tools", model.ToolChoice{Mode: model.ToolChoiceAuto}, false, ""},
		{"none without tools", model.ToolChoice{Mode: model.ToolChoiceNone}, false, ""},
		{"required without tools", model.ToolChoice{Mode: model.ToolChoiceRequired}, false, "config.tool_choice.mode"},
		{"required with tools", model.ToolChoice{Mode: model.ToolChoiceRequired}, true, ""},
		{"named without tools", model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "weather"}, false, "config.tool_choice.name"},
		{"declared name", model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "weather"}, true, ""},
		{"undeclared name", model.ToolChoice{Mode: model.ToolChoiceNamed, Name: "missing"}, true, "config.tool_choice.name"},
		{"exact name", model.ToolChoice{Mode: model.ToolChoiceNamed, Name: " weather "}, true, "config.tool_choice.name"},
		{"invalid choice", model.ToolChoice{}, true, "config.tool_choice.mode"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := model.Request{Instructions: "hello", Config: &model.GenerateConfig{ToolChoice: &tt.choice}}
			if tt.withTools {
				req.Tools = []model.ToolDefinition{toolDefinition("weather", objectSchema)}
			}
			before := cloneRequestForTest(req)
			err := req.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want field %s", err, tt.wantField)
			}
			if !reflect.DeepEqual(req, before) {
				t.Fatal("validation changed request")
			}
		})
	}
}
