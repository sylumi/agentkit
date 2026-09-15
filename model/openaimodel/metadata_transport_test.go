package openaimodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func TestMetadataAndTextToolHistoryAcrossRequestModes(t *testing.T) {
	first, last := textItem("Checking."), textItem("Done.")
	first["phase"], last["phase"], last["id"] = "commentary", "final_answer", "msg_2"
	response := fullResponse("completed", first, callItem(`{"id":9007199254740993}`), last)
	response["model"] = "resolved-model"
	response["usage"] = wire{"input_tokens": 12, "output_tokens": 5,
		"input_tokens_details": wire{"cached_tokens": 0}, "output_tokens_details": wire{"reasoning_tokens": 3}}
	calls := 0
	var input []json.RawMessage
	m := protocolModel(t, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var request struct {
			Stream bool
			Input  []json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		input = request.Input
		body, ct := responseBody(t, response), "application/json"
		if request.Stream {
			body = "data: " + responseBody(t, wire{"type": "response.created", "response": wire{"id": "resp_1", "model": "resolved-model"}}) + "\n\n" +
				"data: " + responseBody(t, wire{"type": "response.completed", "response": response}) + "\n\n"
			ct = "text/event-stream"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	var previous *model.Result
	for _, streaming := range []bool{false, true} {
		var result *model.Result
		for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
			if err != nil {
				t.Fatal(err)
			}
			if e, ok := event.(model.ResultEvent); ok {
				result = &e.Result
			}
		}
		if result == nil {
			t.Fatal("missing result")
		}
		if result.Metadata == nil || *result.Metadata != (model.ResponseMetadata{Provider: "openai", ResponseID: "resp_1", Model: "resolved-model"}) {
			t.Fatal("missing response metadata")
		}
		if result.Usage.CachedInputTokens == nil || *result.Usage.CachedInputTokens != 0 || result.Usage.ReasoningOutputTokens == nil || *result.Usage.ReasoningOutputTokens != 3 {
			t.Fatal("lost usage details")
		}
		if previous != nil && !reflect.DeepEqual(previous, result) {
			t.Fatal("streaming mode changed final content or metadata")
		}
		previous = result
		data, err := json.Marshal(model.Request{Messages: []model.Message{
			*result.Message,
			{Role: model.RoleTool, Parts: []model.Part{{Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call_1", Content: "record"}}}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		var restored model.Request
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		for _, err := range m.Generate(context.Background(), restored, false) {
			if err != nil {
				t.Fatal(err)
			}
		}
		// History contains public content and call IDs, without provider item metadata.
		want := `[
					{"role":"assistant","content":[{"type":"input_text","text":"Checking."}]},
					{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"id\":9007199254740993}"},
					{"role":"assistant","content":[{"type":"input_text","text":"Done."}]},
					{"type":"function_call_output","call_id":"call_1","output":"record"}
				]`
		data, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var gotInput, wantInput any
		if err := json.Unmarshal(data, &gotInput); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &wantInput); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotInput, wantInput) {
			t.Fatalf("history content or tool association changed: %s", data)
		}
		message := &restored.Messages[0]
		message.Parts[0] = model.NewTextPart("edited")
		before := calls
		for _, err := range m.Generate(context.Background(), restored, false) {
			if err != nil {
				t.Fatalf("edited text history: %v", err)
			}
		}
		if calls != before+1 || !strings.Contains(string(input[0]), `"text":"edited"`) {
			t.Fatal("edited history did not reach HTTP")
		}
	}
}

func TestFailuresPreserveResponseMetadata(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		response := wire{"id": "resp_failure", "model": "resolved-model", "status": "failed", "error": wire{"code": "server_error", "message": "failed"},
			"usage": wire{"input_tokens": 12, "output_tokens": 5, "input_tokens_details": wire{"cached_tokens": 2}, "output_tokens_details": wire{"reasoning_tokens": 3}}}
		m := protocolModel(t, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			body, ct := responseBody(t, response), "application/json"
			if streaming {
				// EOF after response.created is a failure even if metadata arrived.
				response["status"] = "in_progress"
				body = "data: " + responseBody(t, wire{"type": "response.created", "response": response}) + "\n\n"
				ct = "text/event-stream"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		var failure *model.CallError
		for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
			if event != nil || !errors.As(err, &failure) {
				t.Fatalf("event=%T error=%v", event, err)
			}
		}
		if failure == nil || failure.Partial.Metadata == nil || failure.Partial.Metadata.ResponseID != "resp_failure" || failure.Partial.Metadata.Model != "resolved-model" {
			t.Fatal("failed call lost response metadata")
		}
		if failure.Partial.Usage.CachedInputTokens == nil || *failure.Partial.Usage.CachedInputTokens != 2 || failure.Partial.Usage.ReasoningOutputTokens == nil || *failure.Partial.Usage.ReasoningOutputTokens != 3 {
			t.Fatal("failed call lost usage details")
		}
	}
}
