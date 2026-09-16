package openaimodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
)

func TestClientConfigurationPrecedence(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://environment.invalid/v1/")
	t.Setenv("OPENAI_ORG_ID", "env-org")
	t.Setenv("OPENAI_PROJECT_ID", "env-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Test-Default: env-value")

	for _, tc := range []struct {
		name                          string
		options                       []option.RequestOption
		useOptionClient               bool
		wantURL, wantKey              string
		wantOrganization, wantProject string
		wantHeader                    string
	}{
		{
			name:    "base fields and environment defaults",
			wantURL: "https://configured.invalid/v1/responses", wantKey: "config-key",
			wantOrganization: "env-org", wantProject: "env-project", wantHeader: "env-value",
		},
		{
			name: "Config fields override SDK options",
			options: []option.RequestOption{
				option.WithAPIKey("option-key"),
				option.WithBaseURL("https://options.invalid/v2/"),
				option.WithOrganization("first-org"),
				option.WithOrganization("option-org"),
				option.WithProject("option-project"),
				option.WithHeader("X-Test-Default", "option-value"),
			},
			wantURL: "https://configured.invalid/v1/responses", wantKey: "config-key",
			wantOrganization: "option-org", wantProject: "option-project", wantHeader: "option-value",
		},
		{
			name:            "nil Config client uses SDK option",
			useOptionClient: true,
			wantURL:         "https://configured.invalid/v1/responses", wantKey: "config-key",
			wantOrganization: "env-org", wantProject: "env-project", wantHeader: "env-value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.wantURL {
					t.Error("API root did not follow configuration precedence")
				}
				if r.Header.Get("Authorization") != "Bearer "+tc.wantKey {
					t.Error("API key did not follow configuration precedence")
				}
				if r.Header.Get("OpenAI-Organization") != tc.wantOrganization || r.Header.Get("OpenAI-Project") != tc.wantProject {
					t.Error("organization or project did not follow configuration precedence")
				}
				if r.Header.Get("X-Test-Default") != tc.wantHeader {
					t.Error("custom header did not follow configuration precedence")
				}
				var request struct {
					Model  string
					Stream bool
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.Model != "test-model" {
					t.Error("request did not use the configured model")
				}
				response := fullResponse("completed", textItem("hello"))
				body, contentType := responseBody(t, response), "application/json"
				if request.Stream {
					body = "data: " + responseBody(t, wire{"type": "response.completed", "response": response}) + "\n\n"
					contentType = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			configClient := client
			optionClient := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("SDK option HTTP client should have been overridden")
			})}
			if tc.useOptionClient {
				configClient = nil
				optionClient = client
			}
			options := append([]option.RequestOption{option.WithHTTPClient(optionClient)}, tc.options...)
			m, err := openaimodel.NewModel(openaimodel.Config{
				Model: model.ModelInfo{ID: "test-model", Provider: model.ProviderInfo{ID: "openai"}}, APIKey: "config-key",
				BaseURL:    "https://configured.invalid/v1",
				HTTPClient: configClient,
				Options:    options,
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatal("constructor sent a request")
			}
			for _, streaming := range []bool{false, true} {
				gotResult := false
				for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
					if err != nil {
						t.Fatal(err)
					}
					if e, ok := event.(model.ResultEvent); ok {
						gotResult = true
						if e.Result.Message == nil || len(e.Result.Message.Parts) != 1 || e.Result.Message.Parts[0].Text == nil || *e.Result.Message.Parts[0].Text != "hello" {
							t.Fatal("SDK response was not converted into the neutral result")
						}
					}
				}
				if !gotResult {
					t.Fatal("missing neutral result")
				}
			}
			if calls != 2 {
				t.Fatalf("HTTP calls = %d, want 2", calls)
			}
		})
	}
}

func TestClientOptionsCannotEnableRetries(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/stream=%t", status, streaming), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{
						StatusCode: status,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"try again","type":"server_error"}}`)),
					}, nil
				})}
				m, err := openaimodel.NewModel(openaimodel.Config{
					Model: model.ModelInfo{ID: "test-model", Provider: model.ProviderInfo{ID: "openai"}}, APIKey: "test-key", BaseURL: "https://example.invalid/", HTTPClient: client,
					Options: []option.RequestOption{
						option.WithMaxRetries(2),
						option.WithMaxRetryDelay(time.Millisecond),
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				failures := 0
				for event, err := range m.Generate(context.Background(), model.Request{Instructions: "hello"}, streaming) {
					var callErr *model.CallError
					if event != nil || !errors.As(err, &callErr) {
						t.Fatal("expected a call error")
					}
					failures++
				}
				if calls != 1 || failures != 1 {
					t.Fatalf("HTTP calls = %d, errors = %d; want one of each", calls, failures)
				}
			})
		}
	}
}
