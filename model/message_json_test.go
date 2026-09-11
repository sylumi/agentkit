package model_test

import (
	"encoding/json"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestMessageJSONPreservesExistingFormat(t *testing.T) {
	for _, raw := range []string{
		`{"role":"user","parts":[{"kind":"text","text":""}]}`,
		`{"role":"assistant","parts":[{"kind":"thinking","thinking":{"text":"legacy"}},{"kind":"thinking","thinking":{"kind":"summary","text":"summary"}},{"kind":"text","text":"answer"}]}`,
		`{"role":"assistant","parts":[{"kind":"tool_call","tool_call":{"id":"call","name":"tool","arguments":{"id":9007199254740993,"decimal":1.234567890123456789,"exponent":1e1000}}}]}`,
		`{"role":"tool","parts":[{"kind":"tool_result","tool_result":{"call_id":"call","content":"","is_error":true}}]}`,
	} {
		var message model.Message
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		if err := message.Validate(); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(message)
		if err != nil || string(data) != raw {
			t.Fatalf("changed history: %s, %v", data, err)
		}
	}
}

func TestDecodedMessageRequiresValidation(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"role":"system","parts":[{"kind":"text","text":"x"}]}`,
		`{"role":"user","parts":null}`, `{"role":"assistant","parts":[]}`,
		`{"role":"user","parts":[null]}`, `{"role":"user","parts":[{}]}`,
		`{"role":"user","parts":[{"kind":"text","text":null}]}`,
		`{"role":"user","parts":[{"kind":"thinking","text":"wrong payload"}]}`,
		`{"role":"assistant","parts":[{"kind":"text","text":"x","thinking":{"text":"y"}}]}`,
		`{"role":"user","parts":[{"kind":"tool_call","tool_call":{"id":"call","name":"tool","arguments":{}}}]}`,
		`{"role":"tool","parts":[{"kind":"text","text":"x"}]}`,
		`{"role":"assistant","parts":[{"kind":"tool_call","tool_call":{"id":"call","name":"tool","arguments":[]}}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var message model.Message
			if err := json.Unmarshal([]byte(raw), &message); err != nil {
				t.Fatalf("structural JSON should decode: %v", err)
			}
			if err := message.Validate(); err == nil {
				t.Fatal("accepted invalid decoded message")
			}
		})
	}
}

func TestMessageJSONUsesStandardMergeSemantics(t *testing.T) {
	original := model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("hello")}}
	var got model.Message
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"parts":[{"kind":"text","text":"updated"}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Role != original.Role || *got.Parts[0].Text != "updated" {
		t.Fatal("missing fields should retain their existing values under standard decoding")
	}
	var fresh model.Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","parts":[{"kind":"text","text":"updated"}]}`), &fresh); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Validate(); err != nil {
		t.Fatalf("fresh decode: %v", err)
	}
}
