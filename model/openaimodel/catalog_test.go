package openaimodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/modelcatalog"
)

func TestExplicitConnectionSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "official-test-key")
	t.Setenv("OPENAI_BASE_URL", "https://implicit.invalid/")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	t.Setenv("AGENTKIT_TEST_GATEWAY_KEY", "gateway-test-key")
	catalog, err := modelcatalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	builtin, ok := catalog.Lookup("openai", "gpt-4.1")
	if !ok {
		t.Fatal("missing builtin model")
	}
	custom := model.ModelInfo{ID: "custom-model", BaseURL: "https://model.invalid/v2", Provider: model.ProviderInfo{
		ID: "gateway", BaseURL: "https://provider.invalid/v1/",
		APIKeyEnv: []string{"AGENTKIT_TEST_GATEWAY_KEY"},
	}}
	for _, tc := range []struct {
		name                           string
		cfg                            openaimodel.Config
		wantURL, wantKey, wantProvider string
	}{
		{"builtin does not inherit SDK key", openaimodel.Config{Model: builtin, BaseURL: "https://api.openai.com/v1/"}, "https://api.openai.com/v1/responses", "", "openai"},
		{"manual OpenAI", openaimodel.Config{Model: model.ModelInfo{ID: "manual", Provider: model.ProviderInfo{ID: "openai"}}, BaseURL: "https://api.openai.com/v1/", APIKey: "explicit-test-key"}, "https://api.openai.com/v1/responses", "explicit-test-key", "openai"},
		{"metadata does not supply address or key", openaimodel.Config{Model: custom, BaseURL: "https://config.invalid/v3"}, "https://config.invalid/v3/responses", "", "gateway"},
		{"explicit Config", openaimodel.Config{Model: custom, BaseURL: "https://config.invalid/v3", APIKey: "explicit-test-key"}, "https://config.invalid/v3/responses", "explicit-test-key", "gateway"},
		{"Config URL overrides SDK option", openaimodel.Config{Model: custom, BaseURL: "https://config.invalid/v3", APIKey: "explicit-test-key", Options: []option.RequestOption{
			option.WithBaseURL("https://options.invalid/v4"), option.WithAPIKey("option-test-key"),
		}}, "https://config.invalid/v3/responses", "option-test-key", "gateway"},
		{"SDK option supplies key", openaimodel.Config{Model: custom, BaseURL: "https://config.invalid/v3", Options: []option.RequestOption{
			option.WithAPIKey("option-test-key"),
		}}, "https://config.invalid/v3/responses", "option-test-key", "gateway"},
		{"header auth", openaimodel.Config{Model: custom, BaseURL: "https://headers.invalid/", Options: []option.RequestOption{
			option.WithHeader("Authorization", "Bearer header-test-key"),
		}}, "https://headers.invalid/responses", "header-test-key", "gateway"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			cfg := tc.cfg
			cfg.HTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.wantURL {
					t.Error("URL precedence changed")
				}
				wantAuth := ""
				if tc.wantKey != "" {
					wantAuth = "Bearer " + tc.wantKey
				}
				if r.Header.Get("Authorization") != wantAuth {
					t.Error("credential precedence changed")
				}
				var request struct {
					Model           string
					Stream          bool
					MaxOutputTokens *int64 `json:"max_output_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.Model != tc.cfg.Model.ID || request.MaxOutputTokens != nil {
					t.Error("model ID or generation defaults changed")
				}
				response := fullResponse("completed", textItem("hello"))
				response["model"] = "resolved-model"
				body, contentType := responseBody(t, response), "application/json"
				if request.Stream {
					body = "data: " + responseBody(t, wire{"type": "response.completed", "response": response}) + "\n\n"
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			m, err := openaimodel.NewModel(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatal("constructor performed I/O")
			}
			// Environment edits after construction must not change this connection.
			t.Setenv("OPENAI_API_KEY", "later-official-key")
			t.Setenv("AGENTKIT_TEST_GATEWAY_KEY", "later-gateway-key")
			for _, streaming := range []bool{false, true} {
				results := 0
				for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
					if err != nil {
						t.Fatal(err)
					}
					if e, ok := event.(model.ResultEvent); ok {
						results++
						if e.Result.Metadata == nil || e.Result.Metadata.Provider != tc.wantProvider || e.Result.Metadata.Model != "resolved-model" {
							t.Error("lost selected provider or response model")
						}
					}
				}
				if results != 1 {
					t.Fatal("missing result")
				}
			}
			if calls != 2 {
				t.Fatalf("calls = %d", calls)
			}
		})
	}
}

func TestCustomProviderDoesNotInheritOpenAICredentialsOrRouting(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "unrelated-official-test-key")
	t.Setenv("OPENAI_BASE_URL", "https://implicit.invalid/")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, endpoint := range []string{"", "https://anonymous.invalid/"} {
		calls := 0
		m, err := openaimodel.NewModel(openaimodel.Config{
			Model:   model.ModelInfo{ID: "custom", Provider: model.ProviderInfo{ID: "custom"}},
			BaseURL: endpoint,
			HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://anonymous.invalid/responses" {
					t.Error("inherited an unrelated endpoint")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("inherited an unrelated credential")
				}
				return nil, errors.New("test transport stops here")
			})},
		})
		if endpoint == "" {
			if err == nil || calls != 0 {
				t.Fatal("missing URL must fail before I/O")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		failures := 0
		for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, false) {
			var callErr *model.CallError
			if event != nil || !errors.As(err, &callErr) {
				t.Fatal("expected a call error")
			}
			failures++
		}
		if failures != 1 || calls != 1 {
			t.Fatal("expected one connection failure without retries")
		}
	}
}

func TestCustomProviderFailureMetadata(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		m, err := openaimodel.NewModel(openaimodel.Config{
			Model:   model.ModelInfo{ID: "custom", Provider: model.ProviderInfo{ID: "gateway"}},
			BaseURL: "https://example.invalid/",
			APIKey:  "test-key",
			HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				r := wire{"id": "resp_failed", "model": "resolved-model", "status": "failed", "error": wire{"code": "server_error", "message": "failed"}}
				body, contentType := responseBody(t, r), "application/json"
				if streaming {
					body = "data: " + responseBody(t, wire{"type": "response.created", "response": wire{"id": "resp_failed", "model": "resolved-model"}}) + "\n\n"
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})},
		})
		if err != nil {
			t.Fatal(err)
		}
		var failure *model.CallError
		for _, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
			if !errors.As(err, &failure) {
				t.Fatal("expected failure")
			}
		}
		if failure == nil || failure.Partial.Metadata == nil || failure.Partial.Metadata.Provider != "gateway" || failure.Partial.Metadata.Model != "resolved-model" {
			t.Fatal("partial output lost selected provider or response model")
		}
	}
}
