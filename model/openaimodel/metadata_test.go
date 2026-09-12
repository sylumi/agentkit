package openaimodel

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

func TestResponseMetadataAndUsageSnapshots(t *testing.T) {
	s := responseStream{}
	var created responses.ResponseStreamEventUnion
	if err := json.Unmarshal([]byte(`{"type":"response.created","response":{"id":"resp_1","model":"resolved","usage":{"input_tokens":12,"output_tokens":5,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":3}}}}`), &created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(created); err != nil {
		t.Fatal(err)
	}
	first := s.snapshot()
	if first.Metadata == nil || first.Metadata.ResponseID != "resp_1" || first.Metadata.Model != "resolved" || first.Metadata.Provider != "openai" {
		t.Fatal("missing early response metadata")
	}
	if first.Usage.CachedInputTokens == nil || *first.Usage.CachedInputTokens != 0 || first.Usage.ReasoningOutputTokens == nil || *first.Usage.ReasoningOutputTokens != 3 {
		t.Fatal("lost detail counts")
	}
	first.Metadata.ResponseID = "modified"
	*first.Usage.CachedInputTokens = 9
	*first.Usage.ReasoningOutputTokens = 1
	next := s.snapshot()
	if next.Metadata.ResponseID != "resp_1" || *next.Usage.CachedInputTokens != 0 || *next.Usage.ReasoningOutputTokens != 3 {
		t.Fatal("snapshot aliases response state")
	}
	var response responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp_1","status":"completed","output":[]}`), &response); err != nil {
		t.Fatal(err)
	}
	if err := s.completeResponse(response); err != nil {
		t.Fatal(err)
	}
	if s.result.Metadata.Model != "resolved" {
		t.Fatal("missing terminal model erased known metadata")
	}
	if s.result.Usage.CachedInputTokens != nil || s.result.Usage.ReasoningOutputTokens != nil {
		t.Fatal("invented unknown terminal usage details")
	}
	snapshot := s.snapshot()
	snapshot.Metadata.Model = "changed"
	if s.result.Metadata.Model != "resolved" {
		t.Fatal("snapshot aliases final metadata")
	}
}

func TestResponseMetadataRejectsChangedID(t *testing.T) {
	s := responseStream{}
	for i, raw := range []string{`{"id":"first","model":"resolved"}`, `{"id":"second","status":"completed","output":[]}`} {
		var r responses.Response
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		err := s.readMetadata(r)
		if i == 0 && err != nil || i == 1 && !errors.Is(err, model.ErrInvalidStream) {
			t.Fatalf("readMetadata() = %v", err)
		}
	}
	if s.snapshot().Metadata.ResponseID != "first" {
		t.Fatal("invalid metadata overwrote known ID")
	}
}

func TestResponseUsageDetailsPresenceAndValidation(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		cache, reasoning bool
		invalid          bool
	}{
		{`{}`, false, false, false},
		{`{"input_tokens_details":null,"output_tokens_details":{}}`, false, false, false},
		{`{"input_tokens_details":{"cached_tokens":null},"output_tokens_details":{"reasoning_tokens":null}}`, false, false, false},
		{`{"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`, true, true, false},
		{`{"input_tokens_details":{"cached_tokens":4}}`, true, false, false},
		{`{"output_tokens_details":{"reasoning_tokens":3}}`, false, true, false},
		{`{"input_tokens_details":{"cached_tokens":-1}}`, false, false, true},
		{`{"output_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}`, false, false, true},
		{`{"input_tokens":0,"input_tokens_details":{"cached_tokens":1}}`, false, false, true},
	} {
		var usage responses.ResponseUsage
		if err := json.Unmarshal([]byte(tc.raw), &usage); err != nil {
			t.Fatal(err)
		}
		s := responseStream{}
		err := s.readUsage(usage)
		if tc.invalid {
			if !errors.Is(err, model.ErrInvalidStream) {
				t.Fatalf("readUsage(%s) = %v", tc.raw, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if (s.usage.CachedInputTokens != nil) != tc.cache || (s.usage.ReasoningOutputTokens != nil) != tc.reasoning {
			t.Fatalf("wrong field presence for %s", tc.raw)
		}
	}
}
