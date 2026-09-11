package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

func failureOutputFixture() model.PartialOutput {
	empty := ""
	return model.PartialOutput{
		Parts: []model.PartialPart{
			{Kind: model.PartText, Text: &empty, Ended: true},
			{Kind: model.PartToolCall, ToolCall: &model.PartialToolCall{
				ID: " call_1 ", Name: " weather ", Arguments: " \n {\"id\":9007199254740993,\"city\":",
			}},
		},
		Usage: model.Usage{InputTokens: tokenCount(math.MaxInt64), OutputTokens: tokenCount(0)},
	}
}

func TestCallErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *model.CallError
		want string
	}{
		{"nil receiver", nil, "<nil>"},
		{"missing cause", &model.CallError{}, "model: call failed"},
		{
			"cause without partial content",
			&model.CallError{Cause: errors.New("response decoding failed"), Partial: failureOutputFixture()},
			"model: call failed: response decoding failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCallErrorUnwrap(t *testing.T) {
	cause := errors.New("read failed")
	tests := []struct {
		name string
		err  *model.CallError
		want error
	}{
		{"nil receiver", nil, nil},
		{"missing cause", &model.CallError{}, nil},
		{"original cause", &model.CallError{Cause: cause}, cause},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Unwrap(tt.err); got != tt.want {
				t.Fatalf("Unwrap() = %v, want original cause %v", got, tt.want)
			}
		})
	}
}

func TestCallErrorIs(t *testing.T) {
	tests := []struct {
		name   string
		cause  error
		target error
	}{
		{"cancellation", fmt.Errorf("read response: %w", context.Canceled), context.Canceled},
		{"deadline", fmt.Errorf("read response: %w", context.DeadlineExceeded), context.DeadlineExceeded},
		{"transport", io.ErrUnexpectedEOF, io.ErrUnexpectedEOF},
		{"missing terminal", model.ErrIncompleteStream, model.ErrIncompleteStream},
		{"invalid stream", model.ErrInvalidStream, model.ErrInvalidStream},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callErr := &model.CallError{Cause: tt.cause, Partial: failureOutputFixture()}
			wrapped := fmt.Errorf("generate response: %w", callErr)
			if !errors.Is(wrapped, tt.target) {
				t.Fatalf("errors.Is(%v, %v) = false", wrapped, tt.target)
			}
			if errors.Is(wrapped, errors.New(tt.target.Error())) {
				t.Fatal("distinct errors with the same text must not match")
			}
			var got *model.CallError
			if !errors.As(wrapped, &got) || got != callErr {
				t.Fatalf("errors.As lost the original CallError: %v", wrapped)
			}
			if !reflect.DeepEqual(got.Partial, failureOutputFixture()) {
				t.Fatal("error wrapping lost partial content, ended state, or known counts")
			}
		})
	}
}

func TestCallErrorPreservesJSONCauseAndClassification(t *testing.T) {
	for _, arguments := range []string{`{"city":`, `[]`} {
		t.Run(arguments, func(t *testing.T) {
			message := &model.Message{Role: model.RoleAssistant, Parts: []model.Part{
				callPart("call_1", "weather", arguments),
			}}
			cause := message.Validate()
			if cause == nil {
				t.Fatal("fixture must produce an argument validation error")
			}
			callErr := &model.CallError{
				Cause:   errors.Join(model.ErrInvalidStream, cause),
				Partial: failureOutputFixture(),
			}
			wrapped := fmt.Errorf("generate response: %w", callErr)
			if !errors.Is(wrapped, model.ErrInvalidStream) || !errors.Is(wrapped, cause) {
				t.Fatalf("error chain lost stream classification or original error: %v", wrapped)
			}
			if errors.Is(wrapped, model.ErrIncompleteStream) {
				t.Fatal("invalid stream must not imply missing terminal")
			}
			if !strings.Contains(wrapped.Error(), "parts[0].tool_call.arguments:") {
				t.Fatalf("error text lost the nested field path: %v", wrapped)
			}
			if arguments == `[]` {
				var original, got *json.UnmarshalTypeError
				if !errors.As(cause, &original) || !errors.As(wrapped, &got) || got != original {
					t.Fatalf("error chain lost the original JSON type error: %v", wrapped)
				}
			} else {
				var original, got *json.SyntaxError
				if !errors.As(cause, &original) || !errors.As(wrapped, &got) || got != original {
					t.Fatalf("error chain lost the original JSON syntax error: %v", wrapped)
				}
			}
		})
	}
}

func TestCallErrorDoesNotMutatePartial(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.PartialOutput)
	}{
		{"mixed output", func(*model.PartialOutput) {}},
		{"zero snapshot", func(p *model.PartialOutput) { *p = model.PartialOutput{} }},
		{"empty parts", func(p *model.PartialOutput) { p.Parts = []model.PartialPart{} }},
		{"unknown usage", func(p *model.PartialOutput) { p.Usage = model.Usage{} }},
		{"unvalidated snapshot", func(p *model.PartialOutput) { *p.Usage.InputTokens = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			partial, before := failureOutputFixture(), failureOutputFixture()
			tt.change(&partial)
			tt.change(&before)
			cause := errors.New("response interrupted")
			callErr := &model.CallError{Cause: cause, Partial: partial}
			_ = callErr.Error()
			if callErr.Unwrap() != cause || callErr.Cause != cause {
				t.Fatal("error access changed the cause")
			}
			if !reflect.DeepEqual(callErr.Partial, before) || !reflect.DeepEqual(partial, before) {
				t.Fatal("error access changed partial data, original argument text, or nil/empty distinctions")
			}
		})
	}
}
