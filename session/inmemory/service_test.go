package inmemory_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
	"github.com/sylumi/agentkit/session/inmemory"
)

func createSession(t *testing.T, id string) (session.Service, session.Session) {
	t.Helper()
	service := inmemory.New()
	created, err := service.Create(t.Context(), &session.CreateRequest{AppName: "app", UserID: "user", SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	return service, created.Session
}

func getSession(t *testing.T, service session.Service, view session.Session) session.Session {
	t.Helper()
	got, err := service.Get(t.Context(), &session.GetRequest{AppName: view.AppName(), UserID: view.UserID(), SessionID: view.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return got.Session
}

func TestServiceLifecycleAndIdentityIsolation(t *testing.T) {
	service := inmemory.New()
	ctx := t.Context()
	request := &session.CreateRequest{AppName: "app", UserID: "user"}
	generated, err := service.Create(ctx, request)
	if err != nil || generated.Session.ID() == "" || request.SessionID != "" {
		t.Fatalf("generated session: %+v, %v", generated, err)
	}
	generatedID, err := uuid.Parse(generated.Session.ID())
	if err != nil || generatedID.Version() != 4 {
		t.Fatalf("expected a UUID v4 session ID, got %q: %v", generated.Session.ID(), err)
	}
	for _, key := range []struct{ app, user, id string }{
		{"scope", "one", "z"}, {"scope", "one", "a"}, {"scope", "two", "a"}, {"other", "one", "a"},
	} {
		created, err := service.Create(ctx, &session.CreateRequest{AppName: key.app, UserID: key.user, SessionID: key.id})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AppendEvent(ctx, created.Session, userEvent(key.app+key.user)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Create(ctx, &session.CreateRequest{AppName: "scope", UserID: "one", SessionID: "a"}); !errors.Is(err, session.ErrAlreadyExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	listed, err := service.List(ctx, &session.ListRequest{AppName: "scope", UserID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, view := range listed.Sessions {
		ids = append(ids, view.ID())
		if view.Events().Len() != 0 {
			t.Fatal("list included full history")
		}
		if err := service.AppendEvent(ctx, view, userEvent("from summary")); err != nil {
			t.Fatal(err)
		}
		if view.Events().Len() != 1 || getSession(t, service, view).Events().Len() != 2 {
			t.Fatal("append from a summary lost stored history or failed to update the view")
		}
	}
	if !reflect.DeepEqual(ids, []string{"a", "z"}) {
		t.Fatalf("listed IDs: %v", ids)
	}
	for range 2 {
		if err := service.Delete(ctx, &session.DeleteRequest{AppName: "scope", UserID: "two", SessionID: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Get(ctx, &session.GetRequest{AppName: "scope", UserID: "two", SessionID: "a"}); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
	for _, app := range []string{"scope", "other"} {
		got, err := service.Get(ctx, &session.GetRequest{AppName: app, UserID: "one", SessionID: "a"})
		if err != nil || *got.Session.Events().At(0).Message.Parts[0].Text != app+"one" {
			t.Fatalf("identity isolation failed for %s: %v", app, err)
		}
	}
	empty, err := service.List(ctx, &session.ListRequest{AppName: "missing", UserID: "user"})
	if err != nil || empty.Sessions == nil || len(empty.Sessions) != 0 {
		t.Fatalf("empty list: %+v, %v", empty, err)
	}
}

func TestConversationHistory(t *testing.T) {
	service, view := createSession(t, "conversation")
	other := getSession(t, service, view)
	snapshot := view.Events()
	first := userEvent("look up the item")
	call := session.NewEvent(first.InvocationID)
	call.Author, call.Message, call.StopReason = "assistant", toolCallMessage(), model.StopReasonToolCalls
	result := session.NewEvent(first.InvocationID)
	result.Author = "assistant"
	result.Message = &model.Message{Role: model.RoleTool, Parts: []model.Part{{
		Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call-1", Content: "found"},
	}}}
	answer := session.NewEvent(first.InvocationID)
	answer.Author, answer.StopReason = "assistant", model.StopReasonStop
	answer.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("The item is available.")}}
	answer.Timestamp = first.Timestamp.Add(-time.Hour)
	for _, event := range []*session.Event{first, call, result, answer} {
		if err := service.AppendEvent(t.Context(), view, event); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.Len() != 0 || other.Events().Len() != 0 {
		t.Fatal("an earlier snapshot or view refreshed automatically")
	}
	if view.Events().Len() != 4 || view.LastUpdateTime().Before(first.Timestamp) {
		t.Fatal("current view was not updated at commit")
	}
	fresh := getSession(t, service, view)
	var ids []string
	for event := range fresh.Events().All() {
		ids = append(ids, event.ID)
	}
	if !reflect.DeepEqual(ids, []string{first.ID, call.ID, result.ID, answer.ID}) {
		t.Fatalf("append order changed: %v", ids)
	}
	if fresh.Events().At(-1) != nil || fresh.Events().At(4) != nil {
		t.Fatal("out-of-range index did not return nil")
	}
	if !fresh.LastUpdateTime().Equal(view.LastUpdateTime()) {
		t.Fatal("stored and current view commit times differ")
	}

	// Appending from an older view preserves the stored history.
	update := userEvent("next question")
	update.InvocationID = "next-invocation"
	if err := service.AppendEvent(t.Context(), other, update); err != nil {
		t.Fatal(err)
	}
	fresh = getSession(t, service, view)
	if fresh.Events().Len() != 5 || other.Events().Len() != 1 || view.Events().Len() != 4 {
		t.Fatal("append from another view lost history or refreshed an existing view")
	}
}

func TestEventContentAndSnapshots(t *testing.T) {
	service, view := createSession(t, "content")
	event := session.NewEvent("invocation")
	event.Author, event.Message, event.StopReason = "assistant", toolCallMessage(), model.StopReasonToolCalls
	event.Message.Parts = append(event.Message.Parts, model.NewTextPart("text"), model.Part{
		Kind: model.PartThinking, Thinking: &model.ThinkingPart{Kind: model.ThinkingText, Text: "thinking"},
	})
	count := int64(7)
	event.Usage = &model.Usage{InputTokens: &count, OutputTokens: &count, CachedInputTokens: &count, ReasoningOutputTokens: &count}
	event.Metadata = &model.ResponseMetadata{Provider: "test", ResponseID: "response"}
	if err := service.AppendEvent(t.Context(), view, event); err != nil {
		t.Fatal(err)
	}
	for _, current := range []session.Session{view, getSession(t, service, view)} {
		if got := current.Events().At(0); !reflect.DeepEqual(got, event) {
			t.Fatalf("stored event changed: %+v", got)
		}
		if !bytes.Equal(current.Events().At(0).Message.Parts[0].ToolCall.Arguments, json.RawMessage(`{ "id": 9007199254740993 }`)) {
			t.Fatal("tool arguments lost their original representation")
		}
	}
	snapshot := view.Events()
	toolResult := session.NewEvent("invocation")
	toolResult.Author = "assistant"
	toolResult.Message = &model.Message{Role: model.RoleTool, Parts: []model.Part{{
		Kind: model.PartToolResult, ToolResult: &model.ToolResultPart{CallID: "call-1", Content: "original"},
	}}}
	if err := service.AppendEvent(t.Context(), view, toolResult); err != nil {
		t.Fatal(err)
	}
	if snapshot.Len() != 1 || view.Events().At(1).Message.Parts[0].ToolResult.Content != "original" {
		t.Fatal("snapshot or tool result content was not preserved")
	}
}

func TestAppendNilEventDoesNotChangeHistory(t *testing.T) {
	service, view := createSession(t, "atomic")
	original := userEvent("original")
	if err := service.AppendEvent(t.Context(), view, original); err != nil {
		t.Fatal(err)
	}
	updatedAt := view.LastUpdateTime()
	if err := service.AppendEvent(t.Context(), view, nil); err == nil {
		t.Fatal("nil event accepted")
	}
	for _, current := range []session.Session{view, getSession(t, service, view)} {
		if current.Events().Len() != 1 || !reflect.DeepEqual(current.Events().At(0), original) || !current.LastUpdateTime().Equal(updatedAt) {
			t.Fatal("failed append changed history or timestamp")
		}
	}
}

func TestAppendRejectsInvalidAndMissingSessions(t *testing.T) {
	service, view := createSession(t, "same")
	for _, invalid := range []session.Session{nil, struct{ session.Session }{view}} {
		if err := service.AppendEvent(t.Context(), invalid, userEvent("invalid")); !errors.Is(err, session.ErrInvalidSession) {
			t.Fatalf("invalid view: %v", err)
		}
	}
	if err := service.Delete(t.Context(), &session.DeleteRequest{AppName: "app", UserID: "user", SessionID: "same"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendEvent(t.Context(), view, userEvent("deleted")); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("deleted view: %v", err)
	}
	if view.Events().Len() != 0 {
		t.Fatal("invalid append changed a view")
	}
}

func TestServiceInputValidation(t *testing.T) {
	service := inmemory.New()
	_, createErr := service.Create(t.Context(), &session.CreateRequest{AppName: " ", UserID: "user"})
	_, getErr := service.Get(t.Context(), &session.GetRequest{AppName: "app", UserID: "user"})
	_, listErr := service.List(t.Context(), &session.ListRequest{AppName: "app"})
	deleteErr := service.Delete(t.Context(), &session.DeleteRequest{SessionID: "requests"})
	for _, err := range []error{createErr, getErr, listErr, deleteErr} {
		if err == nil {
			t.Fatal("incomplete identity accepted")
		}
	}
}

func TestServiceConcurrentAccess(t *testing.T) {
	service, view := createSession(t, "concurrent")
	const workers = 24
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			event := userEvent(fmt.Sprint(i))
			if err := service.AppendEvent(t.Context(), view, event); err != nil {
				errorsFound <- err
				return
			}
			for range view.Events().All() {
				break
			}
			_, err := service.Get(t.Context(), &session.GetRequest{AppName: "app", UserID: "user", SessionID: view.ID()})
			if err != nil {
				errorsFound <- err
			}
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	fresh := getSession(t, service, view)
	if view.Events().Len() != workers || fresh.Events().Len() != workers {
		t.Fatalf("lost events: current=%d stored=%d", view.Events().Len(), fresh.Events().Len())
	}
	seen := make(map[string]bool)
	for event := range fresh.Events().All() {
		seen[*event.Message.Parts[0].Text] = true
	}
	for i := range workers {
		if !seen[fmt.Sprint(i)] {
			t.Fatalf("lost message from worker %d", i)
		}
	}
}

func userEvent(text string) *session.Event {
	event := session.NewEvent("invocation")
	event.Author = "user"
	event.Message = &model.Message{Role: model.RoleUser, Parts: []model.Part{model.NewTextPart(text)}}
	return event
}

func toolCallMessage() *model.Message {
	return &model.Message{Role: model.RoleAssistant, Parts: []model.Part{{
		Kind: model.PartToolCall,
		ToolCall: &model.ToolCallPart{
			ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{ "id": 9007199254740993 }`),
		},
	}}}
}

func TestAppendEventPreservesPayloadWithoutValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event *session.Event
	}{
		{"empty record", &session.Event{}},
		{"message without parts", &session.Event{Message: &model.Message{Role: model.RoleUser}}},
		{"metadata without generation", &session.Event{Metadata: &model.ResponseMetadata{ResponseID: "response"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, view := createSession(t, "payload")
			if err := service.AppendEvent(t.Context(), view, tc.event); err != nil {
				t.Fatal(err)
			}
			if got := getSession(t, service, view).Events().At(0); !reflect.DeepEqual(got, tc.event) {
				t.Fatalf("event changed: got %+v, want %+v", got, tc.event)
			}
		})
	}
}

func TestAppendEventPreservesInput(t *testing.T) {
	service, view := createSession(t, "json")
	event := session.NewEvent("invocation")
	event.Author = "assistant"
	event.Message = &model.Message{Role: model.RoleAssistant, Parts: []model.Part{model.NewTextPart("answer")}}
	event.StopReason = model.StopReasonStop
	count := int64(0)
	event.Usage = &model.Usage{InputTokens: &count}
	event.Metadata = &model.ResponseMetadata{Provider: "test", ResponseID: "provider-response"}

	before, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AppendEvent(t.Context(), view, event); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(event)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("append changed event: %s, %v", after, err)
	}
	if got := getSession(t, service, view).Events().At(0); !reflect.DeepEqual(got, event) {
		t.Fatalf("stored event changed: got %+v, want %+v", got, event)
	}
}

func TestPartialEventsDoNotPersist(t *testing.T) {
	service, view := createSession(t, "stream")
	updated := view.LastUpdateTime()
	for _, delta := range []model.Event{
		model.PartStart{Index: 0, Kind: model.PartThinking, ThinkingKind: model.ThinkingSummary},
		model.ThinkingDelta{Index: 0, Delta: "Checking"},
		model.TextDelta{Index: 1, Delta: "Hello"},
		model.ToolCallDelta{Index: 2, ID: "call", Name: "lookup", Arguments: `{"city":`},
		model.PartEnd{Index: 0},
	} {
		event := session.NewEvent("run")
		event.Author, event.Partial, event.Delta = "agent", true, delta
		if err := service.AppendEvent(t.Context(), view, event); err != nil {
			t.Fatal(err)
		}
	}
	stored := getSession(t, service, view)
	if view.Events().Len() != 0 || stored.Events().Len() != 0 || !view.LastUpdateTime().Equal(updated) || !stored.LastUpdateTime().Equal(updated) {
		t.Fatal("partial events changed session history or update time")
	}
}
