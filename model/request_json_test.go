package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestDecodedRequestRequiresValidation(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"messages":[null]}`, `{"messages":[{}]}`,
		`{"instructions":"hello","config":{"max_output_tokens":0}}`,
		`{"instructions":"hello","config":{"tool_choice":{"mode":"named","name":"missing"}}}`,
	} {
		var req model.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if err := req.Validate(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`[]`, `{"messages":12}`, `{"config":12}`, `{"config":{"max_output_tokens":1.5}}`, `{"instructions":`} {
		var req model.Request
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			t.Fatalf("accepted malformed JSON: %s", raw)
		}
	}
}

func TestRequestConfigJSON(t *testing.T) {
	for _, raw := range []string{
		`{"instructions":"hello"}`, `{"instructions":"hello","config":{}}`,
		`{"instructions":"hello","config":{"max_output_tokens":1024}}`,
		`{"instructions":"hello","config":{"max_output_tokens":9007199254740993}}`,
		`{"instructions":"hello","config":{"tool_choice":{"mode":"auto"}}}`,
	} {
		var req model.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(req)
		if err != nil || string(data) != raw {
			t.Fatalf("round trip: %s, %v", data, err)
		}
	}
}

func TestResultMessageCanBeAddedToRequest(t *testing.T) {
	result := resultFixture()
	req := model.Request{Messages: []model.Message{*result.Message}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.Request
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	if restored.Messages[0].Role != model.RoleAssistant || !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("lost role or argument precision")
	}
}
