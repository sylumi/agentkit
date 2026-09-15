package openaimodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/modelcatalog"
)

func TestReasoningRequests(t *testing.T) {
	catalog, err := modelcatalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	for _, tc := range []struct {
		name, provider, id string
		reasoning          *model.ReasoningConfig
		want, wantError    string
	}{
		{"omitted", "deepseek", "deepseek-flash", nil, "", ""},
		{"empty", "deepseek", "deepseek-flash", &model.ReasoningConfig{}, "", ""},
		{"Flash on needs effort", "deepseek", "deepseek-flash", &model.ReasoningConfig{Enabled: &on}, "", "set an explicit intensity"},
		{"Flash off", "deepseek", "deepseek-flash", &model.ReasoningConfig{Enabled: &off}, "none", ""},
		{"Flash low", "deepseek", "deepseek-flash", &model.ReasoningConfig{Effort: "low"}, "low", ""},
		{"V4 Flash low", "deepseek", "deepseek-v4-flash", &model.ReasoningConfig{Effort: "low"}, "low", ""},
		{"explicit effort with on", "deepseek", "deepseek-flash", &model.ReasoningConfig{Enabled: &on, Effort: "low"}, "low", ""},
		{"Pro max", "deepseek", "deepseek-v4-pro", &model.ReasoningConfig{Effort: "max"}, "max", ""},
		{"Pro rejects low", "deepseek", "deepseek-v4-pro", &model.ReasoningConfig{Effort: "low"}, "", "supported intensities: [high max]"},
		{"no automatic clamping", "deepseek", "deepseek-flash", &model.ReasoningConfig{Effort: "medium"}, "", "unsupported intensity"},
		{"MiMo default", "xiaomi", "mimo-v2.5", nil, "", ""},
		{"MiMo on needs effort", "xiaomi", "mimo-v2.5", &model.ReasoningConfig{Enabled: &on}, "", "set an explicit intensity"},
		{"MiMo off", "xiaomi", "mimo-v2.5-pro", &model.ReasoningConfig{Enabled: &off}, "none", ""},
		{"MiMo unknown efforts pass through", "xiaomi", "mimo-v2.5-pro", &model.ReasoningConfig{Effort: "high"}, "high", ""},
		{"GPT on needs effort", "openai", "gpt-5.4", &model.ReasoningConfig{Enabled: &on}, "", "set an explicit intensity"},
		{"GPT off", "openai", "gpt-5.4", &model.ReasoningConfig{Enabled: &off}, "none", ""},
		{"GPT xhigh", "openai", "gpt-5.4", &model.ReasoningConfig{Effort: "xhigh"}, "xhigh", ""},
		{"GPT max", "openai", "gpt-5.6", &model.ReasoningConfig{Effort: "max"}, "max", ""},
		{"Astra has no declared off control", "openai", "gpt-6-astra", &model.ReasoningConfig{Enabled: &off}, "", "not declared by model metadata"},
		{"Qwen rejects effort missing from metadata", "alibaba-cn", "qwen3.8-max", &model.ReasoningConfig{Effort: "high"}, "", "unsupported intensity"},
		{"Qwen preserves protocol restriction", "alibaba-cn", "qwen3.8-max", &model.ReasoningConfig{Effort: "xhigh"}, "", "current Qwen Responses rule"},
		{"Qwen shared effort", "alibaba", "qwen3.8-max", &model.ReasoningConfig{Effort: "medium"}, "medium", ""},
		{"Qwen international on needs effort", "alibaba", "qwen3.8-max", &model.ReasoningConfig{Enabled: &on}, "", "set an explicit intensity"},
		{"Qwen 3.5 off", "alibaba-cn", "qwen3.5-plus", &model.ReasoningConfig{Enabled: &off}, "none", ""},
		{"non-reasoning model on", "openai", "gpt-4.1", &model.ReasoningConfig{Enabled: &on}, "", "reasoning is not supported"},
		{"non-reasoning model effort", "openai", "gpt-4.1", &model.ReasoningConfig{Effort: "high"}, "", "reasoning is not supported"},
		{"non-reasoning model already off", "openai", "gpt-4.1", &model.ReasoningConfig{Enabled: &off}, "", ""},
		{"neutral conflict before HTTP", "deepseek", "deepseek-flash", &model.ReasoningConfig{Enabled: &off, Effort: "high"}, "", "config.reasoning.effort:"},
		{"neutral off spelling", "deepseek", "deepseek-flash", &model.ReasoningConfig{Effort: "none"}, "", "config.reasoning.effort:"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				info, ok := catalog.Lookup(tc.provider, tc.id)
				if !ok {
					t.Fatal("missing catalog fixture")
				}
				before, err := json.Marshal(info)
				if err != nil {
					t.Fatal(err)
				}
				m, requests := newReasoningModel(t, openaimodel.Config{Model: info})
				checkReasoningCall(t, m, requests, tc.reasoning, stream, tc.want, tc.wantError)
				// Per-call choices and failures must not change subsequent defaults.
				checkReasoningCall(t, m, requests, nil, stream, "", "")
				after, err := json.Marshal(info)
				if err != nil || string(after) != string(before) {
					t.Fatal("adapter modified the catalog description")
				}
			})
		}
	}
}

func TestReasoningUnknownControls(t *testing.T) {
	on, off := true, false
	info := model.ModelInfo{ID: "deepseek-flash", Provider: model.ProviderInfo{ID: "custom"}}
	// A model ID or URL cannot supply missing capability declarations.
	m, requests := newReasoningModel(t, openaimodel.Config{Model: info, BaseURL: "https://api.deepseek.com/"})
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "custom-effort"}, false, "custom-effort", "")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &on}, false, "", "set an explicit intensity")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, false, "", "not declared by model metadata")
	info.ReasoningOptions.Toggle = &on
	m, requests = newReasoningModel(t, openaimodel.Config{Model: info})
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &on}, true, "", "set an explicit intensity")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, true, "none", "")
	info.ReasoningOptions.Toggle = &off
	info.ReasoningOptions.Efforts = []string{"none", "high"}
	m, requests = newReasoningModel(t, openaimodel.Config{Model: info})
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, true, "", "disabling reasoning is not supported")
}

func TestReasoningToggleMetadata(t *testing.T) {
	on, off := true, false
	info := model.ModelInfo{
		ID:               "new-xiaomi-model",
		Provider:         model.ProviderInfo{ID: "xiaomi"},
		ReasoningOptions: model.ReasoningOptions{Toggle: &on},
	}
	m, requests := newReasoningModel(t, openaimodel.Config{Model: info})
	checkReasoningCall(t, m, requests, nil, true, "", "")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{}, false, "", "")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &on}, true, "", "set an explicit intensity")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, false, "none", "")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "high"}, true, "high", "")

	// Explicit efforts follow metadata, independently of the toggle declaration.
	info.ReasoningOptions.Efforts = []string{}
	m, requests = newReasoningModel(t, openaimodel.Config{Model: info})
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "high"}, true, "", "unsupported intensity")
	checkReasoningCall(t, m, requests, nil, false, "", "")
	info.ReasoningOptions.Efforts = []string{"low"}
	m, requests = newReasoningModel(t, openaimodel.Config{Model: info})
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &on}, true, "", "set an explicit intensity")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "low"}, false, "low", "")
}

func TestReasoningCatalogSnapshot(t *testing.T) {
	supported, toggle := true, true
	efforts := []string{"none", "low", "high"}
	info := model.ModelInfo{
		ID: "custom", Provider: model.ProviderInfo{ID: "custom"},
		Capabilities:     model.Capabilities{Reasoning: &supported},
		ReasoningOptions: model.ReasoningOptions{Toggle: &toggle, Efforts: efforts},
	}
	m, requests := newReasoningModel(t, openaimodel.Config{Model: info})
	supported, toggle = false, false
	for i := range efforts {
		efforts[i] = "changed"
	}
	off := false
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "low"}, false, "low", "")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "max"}, true, "", "unsupported intensity")
	checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, true, "none", "")
}

func TestReasoningModelEfforts(t *testing.T) {
	for _, selected := range []struct{ provider, id string }{
		{"custom", "new-model"},
		{"deepseek", "new-deepseek-model"},
		{"openai", "new-openai-model"},
		{"deepseek", "deepseek-flash"},
		{"deepseek", "deepseek-v4-flash"},
		{"deepseek", "deepseek-v4-pro"},
		{"openai", "gpt-5.4"},
		{"openai", "gpt-5.6"},
		{"openai", "gpt-6-astra"},
	} {
		for _, tc := range []struct {
			name      string
			efforts   []string
			wantError string
		}{
			{"listed", []string{"model-effort"}, ""},
			{"unlisted", []string{"high"}, "unsupported intensity"},
			{"unknown", nil, ""},
			{"no intensities", []string{}, "unsupported intensity"},
		} {
			t.Run(selected.provider+"/"+selected.id+"/"+tc.name, func(t *testing.T) {
				info := model.ModelInfo{
					ID:               selected.id,
					Provider:         model.ProviderInfo{ID: selected.provider},
					ReasoningOptions: model.ReasoningOptions{Efforts: tc.efforts},
				}
				m, requests := newReasoningModel(t, openaimodel.Config{Model: info})
				for _, stream := range []bool{false, true} {
					checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "model-effort"}, stream, "model-effort", tc.wantError)
				}
			})
		}
	}
}

func TestReasoningModelUnsupported(t *testing.T) {
	on, off := true, false
	for _, selected := range []struct{ provider, id string }{
		{"deepseek", "deepseek-flash"},
		{"openai", "gpt-5.4"},
		{"xiaomi", "mimo-v2.5"},
		{"alibaba-cn", "qwen3.8-max"},
	} {
		t.Run(selected.provider, func(t *testing.T) {
			info := model.ModelInfo{
				ID:           selected.id,
				Provider:     model.ProviderInfo{ID: selected.provider},
				Capabilities: model.Capabilities{Reasoning: &off},
			}
			m, requests := newReasoningModel(t, openaimodel.Config{Model: info})
			checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &on}, false, "", "reasoning is not supported")
			checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "high"}, true, "", "reasoning is not supported")
			checkReasoningCall(t, m, requests, &model.ReasoningConfig{Enabled: &off}, false, "", "")

			// A metadata edit applies to a new instance, not the existing model.
			info.Capabilities.Reasoning = &on
			info.ReasoningOptions.Efforts = []string{"high"}
			updated, updatedRequests := newReasoningModel(t, openaimodel.Config{Model: info})
			checkReasoningCall(t, updated, updatedRequests, &model.ReasoningConfig{Effort: "high"}, true, "high", "")
			checkReasoningCall(t, m, requests, &model.ReasoningConfig{Effort: "high"}, true, "", "reasoning is not supported")
		})
	}
}

func newReasoningModel(t *testing.T, cfg openaimodel.Config) (model.LLM, *[]wire) {
	t.Helper()
	requests := []wire{}
	cfg.APIKey = "test-key"
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://example.invalid/v1/"
	}
	cfg.HTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var request wire
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		response := fullResponse("completed", textItem("hello"))
		body, contentType := responseBody(t, response), "application/json"
		if request["stream"] == true {
			body = "data: " + responseBody(t, wire{"type": "response.completed", "response": response}) + "\n\n"
			contentType = "text/event-stream"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	m, err := openaimodel.NewModel(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatal("constructor sent an HTTP request")
	}
	return m, &requests
}

func checkReasoningCall(t *testing.T, m model.LLM, requests *[]wire, reasoning *model.ReasoningConfig, stream bool, want, wantError string) {
	t.Helper()
	before := len(*requests)
	limit := int64(1234)
	req := model.Request{Instructions: "hello", Config: &model.GenerateConfig{Reasoning: reasoning, MaxOutputTokens: &limit}}
	results, failures := 0, 0
	for event, err := range m.Generate(context.Background(), req, stream) {
		if err != nil {
			failures++
			var callErr *model.CallError
			if wantError == "" || !errors.As(err, &callErr) || !strings.Contains(err.Error(), wantError) || event != nil {
				t.Fatalf("Generate() error = %v, want %q", err, wantError)
			}
		} else if _, ok := event.(model.ResultEvent); ok {
			results++
		}
	}
	if wantError != "" {
		if failures != 1 || results != 0 || len(*requests) != before {
			t.Fatal("invalid reasoning must fail once before HTTP")
		}
		return
	}
	if failures != 0 || results != 1 || len(*requests) != before+1 {
		t.Fatal("expected one HTTP request and one final result")
	}
	request := (*requests)[before]
	got, present := request["reasoning"]
	if want == "" {
		if present {
			t.Fatal("unspecified reasoning must be omitted, not sent as null or an empty object")
		}
	} else if !reflect.DeepEqual(got, map[string]any{"effort": want}) {
		t.Fatalf("reasoning = %v, want effort %q", got, want)
	}
	if request["max_output_tokens"] != float64(limit) {
		t.Fatal("reasoning changed the total output limit")
	}
	for _, field := range []string{"thinking", "enable_thinking", "reasoning_effort", "thinking_budget", "thinking_token_budget"} {
		if _, ok := request[field]; ok {
			t.Fatalf("unexpected non-Responses field %q", field)
		}
	}
}
