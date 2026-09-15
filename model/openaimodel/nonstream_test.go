package openaimodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
)

type wire map[string]any
type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func protocolModel(t *testing.T, client *http.Client) model.LLM {
	t.Helper()
	m, err := openaimodel.NewModel(openaimodel.Config{
		Model: model.ModelInfo{ID: "test-model"}, APIKey: "test-key",
		BaseURL: "https://example.invalid/", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDecodedInvalidMessagesFailBeforeIO(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			calls := 0
			m := protocolModel(t, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected HTTP request")
			})})
			for _, raw := range []string{
				`{"messages":[null]}`,
				`{"messages":[{"role":"user","parts":[{"kind":"text"}]}]}`,
				`{"messages":[{"role":"assistant","parts":[{"kind":"tool_call","text":"mismatch"}]}]}`,
				`{"messages":[{"role":"user","parts":[{"kind":"text","text":"hello","thinking":{"text":"extra"}}]}]}`,
				`{"messages":[{"role":"user","parts":[{"kind":"tool_call","tool_call":{"id":"call","name":"tool","arguments":{}}}]}]}`,
				`{"messages":[{"role":"tool","parts":[{"kind":"text","text":"wrong role"}]}]}`,
			} {
				var req model.Request
				if err := json.Unmarshal([]byte(raw), &req); err != nil {
					t.Fatal(err)
				}
				yields := 0
				for event, err := range m.Generate(context.Background(), req, stream) {
					yields++
					var callErr *model.CallError
					if event != nil || !errors.As(err, &callErr) || !strings.Contains(err.Error(), "messages[0].") {
						t.Fatalf("unexpected validation result: %v, %v", event, err)
					}
				}
				if yields != 1 || calls != 0 {
					t.Fatalf("yields=%d, HTTP calls=%d", yields, calls)
				}
			}
		})
	}
}

func TestThinkingHistoryFailsBeforeIO(t *testing.T) {
	for _, kind := range []model.ThinkingKind{model.ThinkingUnknown, model.ThinkingText, model.ThinkingSummary} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, stream), func(t *testing.T) {
				calls := 0
				m := protocolModel(t, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return nil, errors.New("unexpected HTTP request")
				})})
				req := model.Request{Messages: []model.Message{{Role: model.RoleAssistant, Parts: []model.Part{
					model.NewTextPart("answer"),
					{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: kind, Text: "thinking"}},
				}}}}
				if err := req.Validate(); err != nil {
					t.Fatal(err)
				}
				var failure error
				yields := 0
				for event, err := range m.Generate(context.Background(), req, stream) {
					yields++
					if event != nil {
						t.Fatal("thinking history produced an event")
					}
					failure = err
				}
				if yields != 1 {
					t.Fatalf("yields=%d, want one error", yields)
				}
				var callErr *model.CallError
				if !errors.As(failure, &callErr) || !strings.Contains(failure.Error(), "messages[0].parts[1]: thinking history is not supported") || calls != 0 {
					t.Fatalf("error=%v HTTP calls=%d; expected history rejection before I/O", failure, calls)
				}
			})
		}
	}
}

func responseBody(t *testing.T, response wire) string {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fullResponse(status string, output ...wire) wire {
	return wire{"id": "resp_1", "status": status, "output": output,
		"usage": wire{"input_tokens": 12, "output_tokens": 5}}
}

func textItem(text string) wire {
	return wire{"id": "msg_1", "type": "message", "role": "assistant", "content": []wire{{"type": "output_text", "text": text}}}
}

func callItem(args string) wire {
	return wire{"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": args}
}

func TestRequestModesProduceEquivalentResults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response wire
		stop     model.StopReason
	}{
		{"text", fullResponse("completed", textItem("hello")), model.StopReasonStop},
		{"empty text", fullResponse("completed", textItem("")), model.StopReasonStop},
		{"summary", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "summary": []wire{{"type": "summary_text", "text": "Checking."}}}, textItem("hello")), model.StopReasonStop},
		{"reasoning", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "content": []wire{{"type": "reasoning_text", "text": "Checking."}}}, textItem("hello")), model.StopReasonStop},
		{"empty reasoning", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "content": []wire{{"type": "reasoning_text", "text": ""}}}, textItem("hello")), model.StopReasonStop},
		{"reasoning with encrypted data", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "encrypted_content": "opaque-reasoning-token", "content": []wire{{"type": "reasoning_text", "text": "Checking."}}}, textItem("hello")), model.StopReasonStop},
		{"summary with encrypted data", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "encrypted_content": "opaque-reasoning-token", "summary": []wire{{"type": "summary_text", "text": "Checking."}}}, textItem("hello")), model.StopReasonStop},
		{"encrypted data only", fullResponse("completed", wire{"id": "rs_1", "type": "reasoning", "encrypted_content": "opaque-reasoning-token", "content": []wire{}, "summary": []wire{}}, textItem("hello")), model.StopReasonStop},
		{"tool", fullResponse("completed", callItem(`{"id":9007199254740993}`)), model.StopReasonToolCalls},
		{"refusal", fullResponse("completed", wire{"id": "msg_1", "type": "message", "role": "assistant", "content": []wire{{"type": "refusal", "refusal": "Cannot comply."}}}), model.StopReasonBlocked},
		{"truncated tool", fullResponse("incomplete", textItem("prefix"), callItem(`{"id":`)), model.StopReasonLength},
		{"blocked tool", fullResponse("incomplete", textItem("prefix"), callItem(`{"id":`)), model.StopReasonBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stop == model.StopReasonLength {
				tc.response["incomplete_details"] = wire{"reason": "max_output_tokens"}
			} else if tc.name == "blocked tool" {
				tc.response["incomplete_details"] = wire{"reason": "content_filter"}
			}
			var modes []bool
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				var body struct {
					Stream bool   `json:"stream"`
					Model  string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Model != "test-model" {
					t.Errorf("model = %q", body.Model)
				}
				modes = append(modes, body.Stream)
				data, contentType := responseBody(t, tc.response), "application/json"
				if body.Stream {
					data = "data: " + responseBody(t, wire{"type": "response." + tc.response["status"].(string), "response": tc.response}) + "\n\n"
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})}
			m := protocolModel(t, client)
			req := model.Request{Instructions: "hello"}
			var results []*model.Result
			for _, streaming := range []bool{false, true} {
				count := 0
				var result *model.Result
				for event, err := range m.Generate(context.Background(), req, streaming) {
					if err != nil {
						t.Fatal(err)
					}
					if err := model.ValidateEvent(event); err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(event)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(data), "opaque-reasoning-token") {
						t.Fatal("encrypted data was exposed in a public event")
					}
					if result != nil {
						t.Fatal("event after final result")
					}
					count++
					if e, ok := event.(model.ResultEvent); ok {
						result = &e.Result
					}
				}
				if result == nil || (!streaming && count != 1) || (streaming && count <= 1) {
					t.Fatalf("stream=%t events=%d result=%v", streaming, count, result)
				}
				if result.StopReason != tc.stop {
					t.Fatalf("stop=%s, want %s", result.StopReason, tc.stop)
				}
				if tc.name == "tool" && string(result.Message.Parts[0].ToolCall.Arguments) != `{"id":9007199254740993}` {
					t.Fatal("tool arguments lost precision")
				}
				if tc.name == "summary" || tc.name == "summary with encrypted data" {
					want := &model.ThinkingPart{Kind: model.ThinkingSummary, Text: "Checking."}
					if !reflect.DeepEqual(result.Message.Parts[0].Thinking, want) {
						t.Fatalf("summary = %+v, want %+v", result.Message.Parts[0].Thinking, want)
					}
				}
				if tc.name == "reasoning" || tc.name == "empty reasoning" || tc.name == "reasoning with encrypted data" {
					wantText := "Checking."
					if tc.name == "empty reasoning" {
						wantText = ""
					}
					want := &model.ThinkingPart{Kind: model.ThinkingText, Text: wantText}
					if !reflect.DeepEqual(result.Message.Parts[0].Thinking, want) {
						t.Fatalf("reasoning = %+v, want %+v", result.Message.Parts[0].Thinking, want)
					}
				}
				if strings.Contains(tc.name, "encrypted data") {
					wantParts := 2
					if tc.name == "encrypted data only" {
						wantParts = 1
					}
					if result.Message == nil || len(result.Message.Parts) != wantParts ||
						!reflect.DeepEqual(result.Message.Parts[wantParts-1], model.NewTextPart("hello")) {
						t.Fatal("encrypted data created a visible part or hid the answer")
					}
				}
				results = append(results, result)
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatal("request modes produced different results")
			}
			if !reflect.DeepEqual(modes, []bool{false, true}) {
				t.Fatalf("request modes=%v", modes)
			}
		})
	}
}

func TestReasoningTextAndSummaryStream(t *testing.T) {
	response := fullResponse("completed", wire{
		"id": "rs_1", "type": "reasoning",
		"content": []wire{{"type": "reasoning_text", "text": "Checking."}},
		"summary": []wire{{"type": "summary_text", "text": "Check."}},
	}, textItem("hello"))
	streamEvents := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":""}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"rs_1","part":{"type":"reasoning_text","text":""}}`,
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_1","delta":"Check"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"item_id":"rs_1","delta":"Check."}`,
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"item_id":"rs_1","text":"Checking."}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"rs_1","part":{"type":"reasoning_text","text":"Checking."}}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"item_id":"rs_1","text":"Check."}`,
		`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":"Check."}}`,
		responseBody(t, wire{"type": "response.completed", "response": response}),
	}
	m := protocolModel(t, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var request struct{ Stream bool }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		body, contentType := responseBody(t, response), "application/json"
		if request.Stream {
			body = "data: " + strings.Join(streamEvents, "\n\ndata: ") + "\n\n"
			contentType = "text/event-stream"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	wantEvents := []model.Event{
		model.PartStart{Index: 0, Kind: model.PartThinking, ThinkingKind: model.ThinkingSummary},
		model.PartStart{Index: 1, Kind: model.PartThinking, ThinkingKind: model.ThinkingText},
		model.ThinkingDelta{Index: 1, Delta: "Check"},
		model.ThinkingDelta{Index: 0, Delta: "Check."},
		model.ThinkingDelta{Index: 1, Delta: "ing."},
		model.PartEnd{Index: 1},
		model.PartEnd{Index: 0},
		model.PartStart{Index: 2, Kind: model.PartText},
		model.TextDelta{Index: 2, Delta: "hello"},
		model.PartEnd{Index: 2},
	}
	wantMessage := &model.Message{Role: model.RoleAssistant, Parts: []model.Part{
		{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingText, Text: "Checking."}},
		{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingSummary, Text: "Check."}},
		model.NewTextPart("hello"),
	}}
	for _, streaming := range []bool{false, true} {
		var received []model.Event
		var result *model.Result
		for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
			if err != nil {
				t.Fatal(err)
			}
			if result != nil {
				t.Fatal("event after final result")
			}
			if e, ok := event.(model.ResultEvent); ok {
				result = &e.Result
			} else {
				received = append(received, event)
			}
		}
		if result == nil || !reflect.DeepEqual(result.Message, wantMessage) {
			t.Fatalf("stream=%t result=%+v; reasoning text and summary must remain separate", streaming, result)
		}
		if streaming && !reflect.DeepEqual(received, wantEvents) {
			t.Fatalf("events = %#v, want %#v", received, wantEvents)
		}
		if !streaming && len(received) != 0 {
			t.Fatal("non-streaming request emitted incremental events")
		}
	}
}

func TestNonStreamingFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		invalid    bool
	}{
		{"queued", `{"id":"r","status":"queued","output":[]}`, 200, true},
		{"in progress", `{"id":"r","status":"in_progress","output":[]}`, 200, true},
		{"unknown status", `{"id":"r","status":"unknown","output":[]}`, 200, true},
		{"failed", `{"id":"r","status":"failed","error":{"code":"server_error","message":"failed"}}`, 200, false},
		{"missing output", `{"id":"r","status":"completed"}`, 200, true},
		{"unknown truncation", `{"id":"r","status":"incomplete","output":[],"incomplete_details":{"reason":"unknown"}}`, 200, true},
		{"malformed JSON", `{"id":`, 200, false},
		{"invalid reasoning type", `{"id":"r","status":"completed","output":[{"id":"rs_1","type":"reasoning","content":[{"type":"output_text","text":"raw"}]}]}`, 200, true},
		{"invalid reasoning text", `{"id":"r","status":"completed","output":[{"id":"rs_1","type":"reasoning","content":[{"type":"reasoning_text","text":123}]}]}`, 200, true},
		{"missing reasoning text", `{"id":"r","status":"completed","output":[{"id":"rs_1","type":"reasoning","content":[{"type":"reasoning_text"}]}]}`, 200, true},
		{"invalid text", `{"id":"r","status":"completed","output":[{"id":"m","type":"message","role":"assistant","content":[{"type":"output_text","text":123}]}]}`, 200, true},
		{"unauthorized", `{"error":{"message":"invalid key","type":"authentication_error"}}`, 401, false},
		{"rate limit", `{"error":{"message":"rate limited","type":"rate_limit_error"}}`, 429, false},
		{"server error", `{"error":{"message":"server error","type":"server_error"}}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := protocolModel(t, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			var failure error
			for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, false) {
				if event != nil || err == nil || failure != nil {
					t.Fatalf("invalid failure event: %v, %v", event, err)
				}
				failure = err
			}
			var ce *model.CallError
			if !errors.As(failure, &ce) || calls != 1 || (tc.invalid && !errors.Is(failure, model.ErrInvalidStream)) {
				t.Fatalf("error=%v calls=%d", failure, calls)
			}
		})
	}
}

func TestNonStreamingLazySingleUseAndCleanup(t *testing.T) {
	calls, closes := 0, 0
	m := protocolModel(t, &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: &trackedBody{Reader: strings.NewReader(responseBody(t, fullResponse("completed", textItem("hello")))), close: func() { closes++ }}}, nil
	})})
	seq := m.Generate(context.Background(), model.Request{Instructions: "hello"}, false)
	if calls != 0 {
		t.Fatal("request started before iteration")
	}
	for event, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := event.(model.ResultEvent); !ok {
			t.Fatalf("unexpected event %T", event)
		}
		break
	}
	if calls != 1 || closes != 1 {
		t.Fatalf("calls=%d closes=%d", calls, closes)
	}
	for event, err := range seq {
		if event != nil || err == nil {
			t.Fatal("iterator reused")
		}
	}
	if calls != 1 {
		t.Fatal("iterator sent another request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	yields := 0
	for event, err := range m.Generate(ctx, model.Request{Instructions: "hello"}, false) {
		yields++
		if event != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled request: event=%v error=%v", event, err)
		}
	}
	if yields != 1 || calls != 1 {
		t.Fatalf("pre-canceled request: yields=%d HTTP calls=%d", yields, calls)
	}
}

type trackedBody struct {
	io.Reader
	close func()
}

func (b *trackedBody) Close() error { b.close(); return nil }

func TestNonStreamingCancellationWhileReading(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			closed := make(chan struct{})
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":`)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
				}
			}))
			defer server.Close()
			m, err := openaimodel.NewModel(openaimodel.Config{
				Model: model.ModelInfo{ID: "test-model"}, APIKey: "test-key",
				BaseURL: server.URL, HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var failure error
				for _, err := range m.Generate(ctx, model.Request{Instructions: "hello"}, false) {
					failure = err
				}
				done <- failure
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			want := context.DeadlineExceeded
			if !deadline {
				want = context.Canceled
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("cancel error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("request did not cancel")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("request body remained open")
			}
		})
	}
}
