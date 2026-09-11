package model_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestUsageDetailSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage model.Usage
		field string
	}{
		{"unknown totals", model.Usage{CachedInputTokens: tokenCount(3), ReasoningOutputTokens: tokenCount(4)}, ""},
		{"known zero", model.Usage{InputTokens: tokenCount(0), OutputTokens: tokenCount(0), CachedInputTokens: tokenCount(0), ReasoningOutputTokens: tokenCount(0)}, ""},
		{"subsets", model.Usage{InputTokens: tokenCount(10), OutputTokens: tokenCount(5), CachedInputTokens: tokenCount(10), ReasoningOutputTokens: tokenCount(3)}, ""},
		{"negative cache", model.Usage{CachedInputTokens: tokenCount(-1)}, "cached_input_tokens"},
		{"negative reasoning", model.Usage{ReasoningOutputTokens: tokenCount(-1)}, "reasoning_output_tokens"},
		{"cache exceeds input", model.Usage{InputTokens: tokenCount(2), CachedInputTokens: tokenCount(3)}, "cached_input_tokens"},
		{"reasoning exceeds output", model.Usage{OutputTokens: tokenCount(0), ReasoningOutputTokens: tokenCount(1)}, "reasoning_output_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.usage.Validate()
			if tc.field == "" && err != nil || tc.field != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.field+":")) {
				t.Fatalf("Validate() = %v, want field %q", err, tc.field)
			}
			data, err := json.Marshal(tc.usage)
			if err != nil {
				t.Fatal(err)
			}
			var restored model.Usage
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tc.usage, restored) {
				t.Fatal("usage JSON lost unknown/zero distinction")
			}
		})
	}
}

func TestResultMetadataJSON(t *testing.T) {
	message := &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("hello")}}
	result := model.Result{Message: message, StopReason: model.StopReasonStop,
		Metadata: &model.ResponseMetadata{Provider: "test", ResponseID: "resp_1", Model: "resolved"},
		Usage:    model.Usage{CachedInputTokens: tokenCount(0), ReasoningOutputTokens: tokenCount(3)}}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.Result
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, restored) {
		t.Fatal("result JSON lost metadata or usage")
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
}
