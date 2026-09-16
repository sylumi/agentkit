package functiontool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sylumi/agentkit/tool/functiontool"
)

type numberArgs struct {
	Value int64  `json:"value" jsonschema:"An exact integer value."`
	Label string `json:"label,omitempty"`
}

func TestTypedFunction(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	const value = int64(9007199254740993)
	calls := 0
	wrapped, err := functiontool.New(functiontool.Config{Name: "number", Description: "Return a number."},
		func(gotCtx context.Context, args numberArgs) (numberArgs, error) {
			calls++
			if gotCtx.Value(contextKey{}) != "request" || args.Value != value || args.Label != "" {
				t.Fatalf("context or typed arguments were lost: %+v", args)
			}
			return args, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	definition := wrapped.Definition()
	if definition.Name != "number" || definition.Description != "Return a number." {
		t.Fatalf("unexpected definition: %+v", definition)
	}
	var schema struct {
		Type       string
		Required   []string
		Properties map[string]struct{ Type, Description string }
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" || !slices.Contains(schema.Required, "value") || slices.Contains(schema.Required, "label") || schema.Properties["value"].Type != "integer" || schema.Properties["value"].Description != "An exact integer value." {
		t.Fatalf("schema does not describe the typed input: %+v", schema)
	}
	arguments := json.RawMessage(`{"value":9007199254740993}`)
	content, err := wrapped.Execute(ctx, arguments)
	if err != nil || content != string(arguments) || calls != 1 {
		t.Fatalf("content = %q, calls = %d, error = %v", content, calls, err)
	}
}

func TestInvalidArgumentsDoNotExecute(t *testing.T) {
	wrapped, err := functiontool.New(functiontool.Config{Name: "number"},
		func(context.Context, numberArgs) (string, error) {
			t.Fatal("invalid arguments reached the handler")
			return "", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"value":null}`, `{"value":"1"}`,
		`{"value":1.5}`, `{"value":9223372036854775808}`,
		`{"value":1,"extra":true}`, `{"value":1} {}`, `{"value":`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := wrapped.Execute(context.Background(), json.RawMessage(input)); err == nil {
				t.Fatal("expected an argument error")
			}
		})
	}
}

func TestExplicitSchema(t *testing.T) {
	type cityArgs struct {
		City string `json:"city"`
	}
	inputSchema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","enum":["Shanghai"]}},"required":["city"],"additionalProperties":false}`)
	wantSchema := string(inputSchema)
	calls := 0
	wrapped, err := functiontool.New(functiontool.Config{Name: "city", InputSchema: inputSchema},
		func(_ context.Context, args cityArgs) (cityArgs, error) {
			calls++
			return args, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	inputSchema[0] = '!'
	definition := wrapped.Definition()
	if string(definition.InputSchema) != wantSchema {
		t.Fatal("changing the constructor input changed the tool definition")
	}
	definition.InputSchema[0] = '!'
	if string(wrapped.Definition().InputSchema) != wantSchema {
		t.Fatal("changing a returned definition changed the tool")
	}
	if _, err := wrapped.Execute(context.Background(), json.RawMessage(`{"city":"London"}`)); err == nil {
		t.Fatal("explicit enum constraint was ignored")
	}
	content, err := wrapped.Execute(context.Background(), json.RawMessage(`{"city":"Shanghai"}`))
	if err != nil || content != `{"city":"Shanghai"}` || calls != 1 {
		t.Fatalf("content = %q, calls = %d, error = %v", content, calls, err)
	}
}

func TestHandlerErrorAndResultEncoding(t *testing.T) {
	wantErr := errors.New("operation failed")
	for _, tc := range []struct {
		name    string
		result  any
		err     error
		content string
	}{
		{"handler error", nil, fmt.Errorf("lookup: %w", wantErr), ""},
		{"string result", "hello", nil, `"hello"`},
		{"unencodable result", make(chan int), nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped, err := functiontool.New(functiontool.Config{Name: "result"},
				func(context.Context, struct{}) (any, error) { return tc.result, tc.err })
			if err != nil {
				t.Fatal(err)
			}
			content, err := wrapped.Execute(context.Background(), json.RawMessage(`{}`))
			switch tc.name {
			case "handler error":
				if !errors.Is(err, wantErr) {
					t.Fatalf("handler error was lost: %v", err)
				}
			case "unencodable result":
				var encodingErr *json.UnsupportedTypeError
				if !errors.As(err, &encodingErr) {
					t.Fatalf("expected encoding error, got %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if content != tc.content {
				t.Fatalf("content = %q, want %q", content, tc.content)
			}
		})
	}
}

func TestCanceledContextDoesNotExecute(t *testing.T) {
	wrapped, err := functiontool.New(functiontool.Config{Name: "cancellation"},
		func(context.Context, struct{}) (struct{}, error) {
			t.Fatal("canceled call reached the handler")
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()
	for _, ctx := range []context.Context{canceled, expired} {
		if _, err := wrapped.Execute(ctx, json.RawMessage(`{}`)); !errors.Is(err, ctx.Err()) {
			t.Fatalf("error = %v, want %v", err, ctx.Err())
		}
	}
}

func TestInvalidConfiguration(t *testing.T) {
	handler := func(context.Context, numberArgs) (string, error) { return "", nil }
	for _, cfg := range []functiontool.Config{
		{},
		{Name: "invalid", InputSchema: json.RawMessage(`{"type":"string"}`)},
		{Name: "invalid", InputSchema: json.RawMessage(`{"type":"object",`)},
		{Name: "invalid", InputSchema: json.RawMessage(`{"type":"object","$ref":"#/$defs/missing"}`)},
	} {
		if _, err := functiontool.New(cfg, handler); err == nil {
			t.Fatalf("expected configuration error for %+v", cfg)
		}
	}
	if _, err := functiontool.New[numberArgs, string](functiontool.Config{Name: "nil"}, nil); err == nil {
		t.Fatal("expected nil handler error")
	}
	if _, err := functiontool.New(functiontool.Config{Name: "scalar"},
		func(context.Context, string) (string, error) { return "", nil }); err == nil {
		t.Fatal("expected non-object input error")
	}
}
