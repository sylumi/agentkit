package model_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sylumi/agentkit/model"
)

const objectSchema = `{"type":"object"}`

func toolDefinition(name, schema string) model.ToolDefinition {
	return model.ToolDefinition{Name: name, InputSchema: json.RawMessage(schema)}
}

func TestToolDefinitionValidate(t *testing.T) {
	tests := []struct {
		name      string
		tool      model.ToolDefinition
		wantField string
	}{
		{"minimal declaration", toolDefinition("weather", objectSchema), ""},
		{"no parameters", toolDefinition("clock", `{"type":"object","properties":{},"additionalProperties":false}`), ""},
		{"non-ASCII name", toolDefinition("\u5929\u6c14", objectSchema), ""},
		{"unmodified name", toolDefinition(" weather ", objectSchema), ""},
		{"zero declaration", model.ToolDefinition{}, "name"},
		{"missing name", toolDefinition("", objectSchema), "name"},
		{"blank name", toolDefinition(" \t\n\u3000", objectSchema), "name"},
		{"nil schema", model.ToolDefinition{Name: "weather"}, "input_schema"},
		{"empty schema", toolDefinition("weather", ""), "input_schema"},
		{"blank schema", toolDefinition("weather", " \n\t"), "input_schema"},
		{"null schema", toolDefinition("weather", `null`), "input_schema"},
		{"boolean schema", toolDefinition("weather", `true`), "input_schema"},
		{"array schema", toolDefinition("weather", `[]`), "input_schema"},
		{"string schema", toolDefinition("weather", `"object"`), "input_schema"},
		{"number schema", toolDefinition("weather", `42`), "input_schema"},
		{"truncated schema", toolDefinition("weather", `{"type":`), "input_schema"},
		{"multiple values", toolDefinition("weather", objectSchema+` {}`), "input_schema"},
		{"missing root type", toolDefinition("weather", `{}`), "input_schema.type"},
		{"reference-only root", toolDefinition("weather", `{"$ref":"#/$defs/input"}`), "input_schema.type"},
		{"null type", toolDefinition("weather", `{"type":null}`), "input_schema.type"},
		{"empty type", toolDefinition("weather", `{"type":""}`), "input_schema.type"},
		{"array type", toolDefinition("weather", `{"type":["object"]}`), "input_schema.type"},
		{"numeric type", toolDefinition("weather", `{"type":42}`), "input_schema.type"},
		{"boolean type", toolDefinition("weather", `{"type":true}`), "input_schema.type"},
		{"object type value", toolDefinition("weather", `{"type":{}}`), "input_schema.type"},
		{"different root type", toolDefinition("weather", `{"type":"string"}`), "input_schema.type"},
		{"case-sensitive root type", toolDefinition("weather", `{"type":"Object"}`), "input_schema.type"},
		{"padded root type", toolDefinition("weather", `{"type":" object "}`), "input_schema.type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tool.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantField+":") {
				t.Fatalf("Validate() = %v, want error at %s", err, tt.wantField)
			}
		})
	}
}

func TestToolDefinitionValidateDoesNotMutate(t *testing.T) {
	tests := []struct {
		name    string
		schema  json.RawMessage
		wantErr bool
	}{
		{
			"valid schema with whitespace and precise numbers",
			json.RawMessage(" \n { \"type\": \"object\", \"properties\": { \"id\": { \"type\": \"integer\", \"minimum\": 9007199254740993 } } } \t"),
			false,
		},
		{"nil schema", nil, true},
		{"empty schema", json.RawMessage{}, true},
		{"truncated schema", json.RawMessage(`{"type":`), true},
		{"invalid root type", json.RawMessage(`{"type":null}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := model.ToolDefinition{
				Name: " weather ", Description: " local weather\n", InputSchema: tt.schema,
			}
			before := tool
			before.InputSchema = bytes.Clone(tool.InputSchema)
			if err := tool.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, want error: %t", err, tt.wantErr)
			}
			if !reflect.DeepEqual(tool, before) {
				t.Fatalf("Validate changed declaration: got %#v, want %#v", tool, before)
			}
		})
	}
}
