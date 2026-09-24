package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sylumi/agentkit/agent"
	"github.com/sylumi/agentkit/agent/llmagent"
	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/server"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/session/inmemory"
	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
)

type agentFunc func(context.Context, *agent.InvocationContext) iter.Seq2[*session.Event, error]

func (agentFunc) Name() string        { return "test-agent" }
func (agentFunc) Description() string { return "An independent agent." }
func (f agentFunc) Run(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return f(ctx, inv)
}

func echoAgent(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		e := session.NewEvent(inv.InvocationID)
		e.Author = "test-agent"
		e.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("hello")}}
		e.StopReason = model.StopReasonStop
		yield(e, nil)
	}
}

func newServer(t *testing.T, a agent.Agent) (*server.Server, session.Service) {
	t.Helper()
	store := inmemory.New()
	s, err := server.New(server.Config{AppName: "test", Agent: a, SessionService: store})
	if err != nil {
		t.Fatal(err)
	}
	return s, store
}

func request(t *testing.T, h http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, status, w.Body)
	}
	return w
}

type snapshot struct {
	ID      string           `json:"id"`
	Running bool             `json:"running"`
	Events  []*session.Event `json:"events"`
}

func decodeSnapshot(t *testing.T, w *httptest.ResponseRecorder) snapshot {
	t.Helper()
	var s snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func create(t *testing.T, h http.Handler) string {
	t.Helper()
	s := decodeSnapshot(t, request(t, h, "POST", "/api/sessions", "", 201))
	if s.ID == "" || s.Running || s.Events == nil || len(s.Events) != 0 {
		t.Fatalf("bad new session: %+v", s)
	}
	return s.ID
}

func TestConfigAndHTTPValidation(t *testing.T) {
	base := server.Config{AppName: "test", Agent: agentFunc(echoAgent), SessionService: inmemory.New()}
	for _, mutate := range []func(*server.Config){
		func(c *server.Config) { c.AppName = "" }, func(c *server.Config) { c.Agent = nil },
		func(c *server.Config) { c.SessionService = nil },
		func(c *server.Config) { c.UserID = " " },
	} {
		cfg := base
		mutate(&cfg)
		if _, err := server.New(cfg); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
	h, err := server.New(base)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/agent", "", 200)
	if !strings.Contains(w.Body.String(), `"name":"test-agent"`) || strings.Contains(w.Body.String(), `"model"`) || strings.Contains(w.Body.String(), `"display"`) {
		t.Fatal(w.Body)
	}
	id := create(t, h)
	for _, body := range []string{"", "null", "[]", `{"other":true}`, "{} {}", `{}`, `{"text":" "}`, `{"text":2}`, `{"text":"ok","model":"secret"}`, `{"text":"` + strings.Repeat("界", 4001) + `"}`} {
		request(t, h, "POST", "/api/sessions/"+id+"/run", body, 400)
	}
	request(t, h, "POST", "/api/sessions/"+id+"/run", `{"text":"`+strings.Repeat("a", 70000)+`"}`, 413)
	request(t, h, "GET", "/api/sessions/missing", "", 404)
	request(t, h, "POST", "/api/sessions/missing/run", `{"text":"hi"}`, 404)
	request(t, h, "GET", "/missing", "", 404)
	if w := request(t, h, "PUT", "/api/sessions", "{}", 405); w.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Fatal(w.Header())
	}
	r := httptest.NewRequest("POST", "/api/sessions/"+id+"/run", strings.NewReader(`{"text":"hi"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "/api/sessions", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Origin", "http://example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
}

func TestCRUDIsolationAndRunPersistence(t *testing.T) {
	h, store := newServer(t, agentFunc(echoAgent))
	_, err := store.Create(t.Context(), &session.CreateRequest{AppName: "other", UserID: "user", SessionID: "private"})
	if err != nil {
		t.Fatal(err)
	}
	first, second := create(t, h), create(t, h)
	for i := 0; i < 2; i++ {
		w := request(t, h, "POST", "/api/sessions/"+first+"/run", `{"text":"你好"}`, 200)
		if w.Header().Get("Content-Type") != "text/event-stream" || strings.Count(w.Body.String(), "event: event\n") != 1 || strings.Count(w.Body.String(), "event: done\n") != 1 || !strings.Contains(w.Body.String(), `"status":"finished"`) {
			t.Fatal(w.Body)
		}
		s := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+first, "", 200))
		if s.Running || len(s.Events) != (i+1)*2 {
			t.Fatalf("snapshot: %+v", s)
		}
		if s.Events[i*2].Author != "user" || *s.Events[i*2].Message.Parts[0].Text != "你好" {
			t.Fatal(s.Events)
		}
		if s.Events[i*2].InvocationID != s.Events[i*2+1].InvocationID {
			t.Fatal("invocation mismatch")
		}
		if !strings.Contains(w.Body.String(), s.Events[i*2+1].ID) {
			t.Fatal("SSE event differs from storage")
		}
	}
	w := request(t, h, "GET", "/api/sessions", "", 200)
	var list struct {
		Sessions []snapshot `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 2 || list.Sessions[0].ID != first || strings.Contains(w.Body.String(), "events") {
		t.Fatal(w.Body)
	}
	request(t, h, "GET", "/api/sessions/private", "", 404)
	request(t, h, "DELETE", "/api/sessions/"+second, "", 204)
	request(t, h, "DELETE", "/api/sessions/"+second, "", 204)
	request(t, h, "GET", "/api/sessions/"+second, "", 404)
}

func TestDisconnectCancelsAndSerializesSession(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	a := agentFunc(func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			close(started)
			<-ctx.Done()
			close(stopped)
			yield(nil, ctx.Err())
		}
	})
	h, _ := newServer(t, a)
	id := create(t, h)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/sessions/"+id+"/run", strings.NewReader(`{"text":"wait"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	if !decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200)).Running {
		t.Fatal("missing running flag")
	}
	request(t, h, "POST", "/api/sessions/"+id+"/run", `{"text":"duplicate"}`, 409)
	request(t, h, "DELETE", "/api/sessions/"+id, "", 409)
	// A different session is not blocked by this run.
	other := create(t, h)
	request(t, h, "DELETE", "/api/sessions/"+other, "", 204)
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel agent")
	}
	deadline := time.Now().Add(time.Second)
	for {
		s := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200))
		if !s.Running {
			if len(s.Events) != 1 {
				t.Fatal("duplicate user input")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session not released")
		}
		time.Sleep(time.Millisecond)
	}
	request(t, h, "DELETE", "/api/sessions/"+id, "", 204)
}

func TestTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{"error", "timeout", "length", "blocked", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			a := agentFunc(func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
				return func(yield func(*session.Event, error) bool) {
					switch outcome {
					case "error":
						yield(nil, errors.New("private provider detail"))
					case "timeout":
						yield(nil, context.DeadlineExceeded)
					case "cancelled":
						yield(nil, context.Canceled)
					default:
						e := session.NewEvent(inv.InvocationID)
						e.Author = "test-agent"
						e.StopReason = model.StopReason(outcome)
						yield(e, nil)
					}
				}
			})
			h, _ := newServer(t, a)
			id := create(t, h)
			w := request(t, h, "POST", "/api/sessions/"+id+"/run", `{"text":"hi"}`, 200)
			want := `"status":"finished"`
			if outcome == "error" {
				want = `"code":"run_failed"`
			}
			if outcome == "timeout" {
				want = `"code":"run_timeout"`
			}
			if outcome == "cancelled" {
				want = `"status":"cancelled"`
			}
			if !strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), "private provider detail") {
				t.Fatal(w.Body)
			}
			if outcome == "length" || outcome == "blocked" {
				if !strings.Contains(w.Body.String(), `"stop_reason":"`+outcome+`"`) {
					t.Fatal(w.Body)
				}
			}
			if decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200)).Running {
				t.Fatal("still running")
			}
		})
	}
}

type modelFunc func(context.Context, model.Request, bool) iter.Seq2[model.Event, error]

func (f modelFunc) Generate(ctx context.Context, r model.Request, stream bool) iter.Seq2[model.Event, error] {
	return f(ctx, r, stream)
}

func TestLLMAgentToolsAcrossTurns(t *testing.T) {
	for _, toolFails := range []bool{false, true} {
		t.Run(fmt.Sprint(toolFails), func(t *testing.T) {
			calls := 0
			add, err := functiontool.New(functiontool.Config{Name: "add"}, func(_ context.Context, args struct {
				A int `json:"a"`
				B int `json:"b"`
			}) (int, error) {
				calls++
				if toolFails {
					return 0, errors.New("addition unavailable")
				}
				return args.A + args.B, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			llm := modelFunc(func(_ context.Context, req model.Request, stream bool) iter.Seq2[model.Event, error] {
				if !stream {
					t.Error("web runs must request token streaming")
				}
				return func(yield func(model.Event, error) bool) {
					last := req.Messages[len(req.Messages)-1]
					result := model.Result{StopReason: model.StopReasonStop, Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("done")}}}
					if last.Role == model.RoleUser {
						result.StopReason = model.StopReasonToolCalls
						result.Message.Parts = []model.Part{{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{ID: "reused-id", Name: "add", Arguments: json.RawMessage(`{"a":12,"b":8}`)}}}
					} else if last.Role != model.RoleTool || last.Parts[0].ToolResult.IsError != toolFails || (!toolFails && last.Parts[0].ToolResult.Content != "20") {
						t.Error("tool result missing from model history")
					}
					yield(model.ResultEvent{Result: result}, nil)
				}
			})
			a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "calculator", Model: llm, Tools: []tool.Tool{add}})
			if err != nil {
				t.Fatal(err)
			}
			h, _ := newServer(t, a)
			id := create(t, h)
			for range 2 {
				w := request(t, h, "POST", "/api/sessions/"+id+"/run", `{"text":"add"}`, 200)
				if !strings.Contains(w.Body.String(), `"status":"finished"`) {
					t.Fatal(w.Body)
				}
			}
			s := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200))
			if calls != 2 || len(s.Events) != 8 {
				t.Fatalf("calls=%d events=%d", calls, len(s.Events))
			}
		})
	}
}

type brokenWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *brokenWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(b)
}

func TestWriteFailureStopsIterator(t *testing.T) {
	advanced := false
	a := agentFunc(func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			e := session.NewEvent(inv.InvocationID)
			if !yield(e, nil) {
				return
			}
			advanced = true
		}
	})
	h, _ := newServer(t, a)
	id := create(t, h)
	w := &brokenWriter{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest("POST", "/api/sessions/"+id+"/run", strings.NewReader(`{"text":"hi"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	if advanced {
		t.Fatal("advanced after failed event write")
	}
	request(t, h, "DELETE", "/api/sessions/"+id, "", 204)
}

func TestLiveStreamArrivesBeforeFinalCommit(t *testing.T) {
	finish := make(chan struct{})
	llm := modelFunc(func(ctx context.Context, _ model.Request, stream bool) iter.Seq2[model.Event, error] {
		if !stream {
			t.Error("server did not request streaming")
		}
		return func(yield func(model.Event, error) bool) {
			for _, update := range []model.Event{
				model.PartStart{Index: 0, Kind: model.PartThinking, ThinkingKind: model.ThinkingText},
				model.ThinkingDelta{Index: 0, Delta: "Let me check."}, model.PartEnd{Index: 0},
				model.PartStart{Index: 1, Kind: model.PartText}, model.TextDelta{Index: 1, Delta: "20"}, model.PartEnd{Index: 1},
			} {
				if !yield(update, nil) {
					return
				}
			}
			select {
			case <-finish:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
			yield(model.ResultEvent{Result: model.Result{StopReason: model.StopReasonStop, Message: &model.Message{Role: model.RoleAssistant, Parts: []model.Part{
				{Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingText, Text: "Let me check."}}, model.NewTextPart("20"),
			}}}}, nil)
		}
	})
	a, err := llmagent.New(llmagent.Config{MaxModelCalls: 10, Name: "live", Model: llm})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := newServer(t, a)
	id := create(t, h)
	srv := httptest.NewServer(h)
	defer srv.Close()
	r, err := http.NewRequestWithContext(t.Context(), "POST", srv.URL+"/api/sessions/"+id+"/run", strings.NewReader(`{"text":"calculate"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var update session.Event
	sawThinking := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &update); err != nil {
				t.Fatal(err)
			}
			if _, ok := update.Delta.(model.ThinkingDelta); ok && update.Partial {
				sawThinking = true
			}
			if _, ok := update.Delta.(model.TextDelta); ok {
				break
			}
		}
	}
	if _, ok := update.Delta.(model.TextDelta); !ok || !update.Partial || update.ID == "" || !sawThinking {
		t.Fatalf("did not receive live output: %v", scanner.Err())
	}
	before := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200))
	if !before.Running || len(before.Events) != 1 || before.Events[0].Message.Role != model.RoleUser {
		t.Fatal("unconfirmed output entered history")
	}
	close(finish)
	var rest strings.Builder
	for scanner.Scan() {
		rest.WriteString(scanner.Text())
		rest.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rest.String(), `"status":"finished"`) {
		t.Fatal(rest.String())
	}
	after := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200))
	if after.Running || len(after.Events) != 2 || after.Events[1].ID != update.ID || after.Events[1].Partial || after.Events[1].Delta != nil {
		t.Fatal("final output did not replace the preview")
	}
}

type snapshotStore struct {
	session.Service
	readContext context.Context
	read        chan struct{}
	resume      chan struct{}
}

func (s *snapshotStore) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	response, err := s.Service.Get(ctx, req)
	if ctx == s.readContext {
		close(s.read)
		<-s.resume
	}
	return response, err
}

func TestSessionSnapshotDuringRunCompletion(t *testing.T) {
	readContext, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &snapshotStore{
		Service: inmemory.New(), readContext: readContext,
		read: make(chan struct{}), resume: make(chan struct{}),
	}
	started, finish, committed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := agentFunc(func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			close(started)
			<-finish
			echoAgent(ctx, inv)(yield)
			close(committed)
		}
	})
	h, err := server.New(server.Config{AppName: "test", Agent: a, SessionService: store})
	if err != nil {
		t.Fatal(err)
	}
	id := create(t, h)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		request(t, h, "POST", "/api/sessions/"+id+"/run", `{"text":"hi"}`, 200)
	}()
	<-started

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/sessions/"+id, nil).WithContext(readContext)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		h.ServeHTTP(w, r)
	}()
	<-store.read // The stored snapshot contains only the user input.
	close(finish)
	<-committed // The final reply is now persisted, but absent from that snapshot.
	// Give completion a chance to release the run. A consistent snapshot keeps
	// the release blocked until its history and running state have been read.
	select {
	case <-runDone:
	case <-time.After(time.Second):
	}
	close(store.resume)
	<-readDone
	<-runDone

	during := decodeSnapshot(t, w)
	if !during.Running || len(during.Events) != 1 {
		t.Fatalf("snapshot with unfinished history must still be running: %+v", during)
	}
	after := decodeSnapshot(t, request(t, h, "GET", "/api/sessions/"+id, "", 200))
	if after.Running || len(after.Events) != 2 || after.Events[1].Message.Role != model.RoleAssistant {
		t.Fatalf("completed snapshot must include the final reply: %+v", after)
	}
}

func TestHTTP2StreamSurvivesIdle(t *testing.T) {
	for _, afterEvent := range []bool{false, true} {
		name := "after_connection"
		if afterEvent {
			name = "after_event"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := agentFunc(func(ctx context.Context, inv *agent.InvocationContext) iter.Seq2[*session.Event, error] {
				return func(yield func(*session.Event, error) bool) {
					if afterEvent {
						e := session.NewEvent(inv.InvocationID)
						e.Partial = true
						e.Delta = model.TextDelta{Index: 0, Delta: "thinking"}
						if !yield(e, nil) {
							return
						}
					}
					// Exceed the server's 10-second write timeout while no write
					// is pending, as a slow model or tool might do.
					timer := time.NewTimer(11 * time.Second)
					defer timer.Stop()
					select {
					case <-timer.C:
						echoAgent(ctx, inv)(yield)
					case <-ctx.Done():
					}
				}
			})
			h, _ := newServer(t, a)
			id := create(t, h)
			srv := httptest.NewUnstartedServer(h)
			srv.EnableHTTP2 = true
			srv.StartTLS()
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			r, err := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/sessions/"+id+"/run", strings.NewReader(`{"text":"wait"}`))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Content-Type", "application/json")
			resp, err := srv.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusOK {
				t.Fatalf("expected HTTP/2 SSE response, got %s %s", resp.Proto, resp.Status)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("stream closed during idle: %v; body: %s", err, body)
			}
			wantEvents := 1
			if afterEvent {
				wantEvents++
			}
			if strings.Count(string(body), "event: event\n") != wantEvents || !strings.Contains(string(body), "event: done\ndata: {\"status\":\"finished\"}") {
				t.Fatalf("stream did not finish after idle: %s", body)
			}
		})
	}
}
