package openaimodel

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

func TestFinalMessageOmitsTruncatedArguments(t *testing.T) {
	var response responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{"},{"id":"msg","type":"message","role":"assistant","content":[{"type":"output_text","text":"prefix"}]}]}`), &response); err != nil {
		t.Fatal(err)
	}
	s := responseStream{}
	if err := s.completeResponse(response); err != nil {
		t.Fatal(err)
	}
	if len(s.result.Message.Parts) != 1 {
		t.Fatal("incomplete call leaked into final message")
	}
	if s.result.StopReason != model.StopReasonLength || *s.result.Message.Parts[0].Text != "prefix" {
		t.Fatalf("unexpected final result: %+v", s.result)
	}
	partial := s.snapshot()
	if len(partial.Parts) != 2 || partial.Parts[0].Ended || partial.Parts[0].ToolCall == nil || partial.Parts[0].ToolCall.Arguments != "{" || !partial.Parts[1].Ended {
		t.Fatalf("incomplete arguments lost from snapshot: %+v", partial)
	}
}

func TestInterleavedStreamPreservesFinalItemOrder(t *testing.T) {
	var response responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp","status":"completed","output":[{"id":"a","type":"message","role":"assistant","content":[{"type":"output_text","text":"A"}]},{"id":"b","type":"message","role":"assistant","content":[{"type":"output_text","text":"B"}]}]}`), &response); err != nil {
		t.Fatal(err)
	}
	complete := responseStream{}
	if err := complete.completeResponse(response); err != nil {
		t.Fatal(err)
	}
	stream := responseStream{yield: func(model.Event) bool { return true }}
	for i, item := range response.Output {
		if err := stream.item(int64(i), item, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, i := range []int64{1, 0} {
		block, err := stream.open(blockKey{i, 0, "output_text"}, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.append(block, response.Output[i].Content[0].Text); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.completeResponse(response); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(complete.result, stream.result) {
		t.Fatal("stream arrival order changed final message")
	}
}
