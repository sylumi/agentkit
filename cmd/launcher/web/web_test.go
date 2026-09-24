package web

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/model/openaimodel"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/session/inmemory"
	webui "github.com/sylumi/agentkit/web"
)

type testAgent struct {
	run func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error]
}

func (testAgent) Name() string        { return "chat" }
func (testAgent) Description() string { return "Test chat" }
func (a testAgent) Run(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return a.run(ctx, inv)
}

func request(t *testing.T, handler http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}

func TestWebServesBundledUIAndStreamingAPI(t *testing.T) {
	a := testAgent{run: func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			if !inv.Streaming {
				t.Fatal("web did not request streaming")
			}
			e := session.NewEvent(inv.InvocationID)
			e.Author = "chat"
			for _, part := range []model.Event{model.PartStart{Kind: model.PartText}, model.TextDelta{Delta: "Hello"}, model.PartEnd{}} {
				partial := *e
				partial.Partial, partial.Delta = true, part
				if !yield(&partial, nil) {
					return
				}
			}
			e.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello")}}
			yield(e, nil)
		}
	}}
	h, err := NewHandler(launcher.Config{Agent: a}, webui.Handler())
	if err != nil {
		t.Fatal(err)
	}
	// Serving assets must work independently of the source checkout's location.
	t.Chdir(t.TempDir())
	page := request(t, h, "GET", "/", "", 200)
	if !strings.Contains(page.Body.String(), `<div id="root">`) {
		t.Fatal("missing built application")
	}
	assets := regexp.MustCompile(`(?:src|href)="\./(assets/[^"]+)"`).FindAllStringSubmatch(page.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("missing JavaScript or CSS entry")
	}
	for _, asset := range assets {
		w := request(t, h, "GET", "/"+asset[1], "", 200)
		if w.Body.Len() == 0 || strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Fatal("asset served as an empty file or HTML", asset[1])
		}
	}
	request(t, h, "GET", "/src/main.tsx", "", 404)
	for _, path := range []string{"/api", "/api/missing"} {
		w := request(t, h, "GET", path, "", 404)
		if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatal("unexpected response for unknown API path", w.Body.String())
		}
	}
	request(t, h, "GET", "/api/agent", "", 200)
	created := request(t, h, "POST", "/api/sessions", "", 201)
	var snapshot struct {
		ID     string          `json:"id"`
		Events []session.Event `json:"events"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &snapshot); err != nil || snapshot.ID == "" {
		t.Fatalf("create: %v", err)
	}
	path := "/api/sessions/" + snapshot.ID
	run := request(t, h, "POST", path+"/run", `{"text":"hi"}`, 200)
	body := run.Body.String()
	if !strings.Contains(body, `"type":"text_delta"`) || !strings.Contains(body, `"status":"finished"`) || strings.Count(body, "event: event\n") != 4 || strings.Count(body, `"partial":true`) != 3 || strings.Index(body, `"partial":true`) > strings.Index(body, `"message":`) {
		t.Fatal("missing or misordered streaming output", body)
	}
	if err := json.Unmarshal(request(t, h, "GET", path, "", 200).Body.Bytes(), &snapshot); err != nil || len(snapshot.Events) != 2 {
		t.Fatalf("history: %v, events=%d", err, len(snapshot.Events))
	}
	r := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://other.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("same-origin protection was lost")
	}
}

func TestWebInfersPublicModelLabel(t *testing.T) {
	llm, err := openaimodel.NewModel(openaimodel.Config{
		Model:   model.ModelInfo{ID: "model-id", Name: "Example model"},
		BaseURL: "https://example.invalid", APIKey: "private-test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "chat", Model: llm})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(launcher.Config{Agent: a}, webui.Handler())
	if err != nil {
		t.Fatal(err)
	}
	info := request(t, h, "GET", "/api/agent", "", 200).Body.String()
	var metadata struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(info), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Model != "model-id" || strings.Contains(info, `"display"`) || strings.Contains(info, "private-test-key") || strings.Contains(info, "example.invalid") {
		t.Fatal("incorrect public model metadata", info)
	}
}

func TestWebUsesConfiguredServiceWithoutUI(t *testing.T) {
	a := testAgent{run: func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("web added a run deadline")
			}
			e := session.NewEvent(inv.InvocationID)
			e.Author = "chat"
			e.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("Hello")}}
			yield(e, nil)
		}
	}}
	store := inmemory.New()
	cfg := launcher.Config{Agent: a, SessionService: store, AppName: "custom-app", UserID: "alice"}
	h, err := NewHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	request(t, h, "GET", "/", "", 404)
	created := request(t, h, "POST", "/api/sessions", `{}`, 201)
	var snapshot struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &snapshot); err != nil || snapshot.ID == "" {
		t.Fatalf("create: %v", err)
	}
	w := request(t, h, "POST", "/api/sessions/"+snapshot.ID+"/run", `{"text":"hi"}`, 200)
	if !strings.Contains(w.Body.String(), `"status":"finished"`) {
		t.Fatal("run did not finish", w.Body.String())
	}
	loaded, err := store.Get(t.Context(), &session.GetRequest{AppName: cfg.AppName, UserID: cfg.UserID, SessionID: snapshot.ID})
	if err != nil || loaded.Session.Events().Len() != 2 {
		t.Fatalf("configured service was not used: %v", err)
	}
	second, err := NewHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	request(t, second, "GET", "/api/sessions/"+snapshot.ID, "", 200)
}

func TestShutdownCancelsActiveRun(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	a := testAgent{run: func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			close(started)
			<-ctx.Done()
			close(stopped)
			yield(nil, ctx.Err())
		}
	}}
	h, err := NewHandler(launcher.Config{Agent: a}, webui.Handler())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer listener.Close()
	finished := make(chan error, 1)
	go func() { finished <- serveWeb(ctx, listener, h) }()
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + listener.Addr().String()
	created, err := client.Post(base+"/api/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ ID string }
	err = json.NewDecoder(created.Body).Decode(&snapshot)
	created.Body.Close()
	if err != nil || snapshot.ID == "" {
		t.Fatalf("create: %v", err)
	}
	response, err := client.Post(base+"/api/sessions/"+snapshot.ID+"/run", "application/json", strings.NewReader(`{"text":"wait"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Agent was not canceled")
	}
}
