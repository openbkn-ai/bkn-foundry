// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// _display carries the raw value of the object type's display property. bkn-backend accepts an
// integer, float, decimal, boolean or date/time display key as well as a string one, and
// ontology-query copies the value through untouched (null when the row's field is empty).
// MCP SDK clients validate structuredContent against output_schema and drop the whole result on
// the first mismatch, so pinning _display to string turned every numeric display key into a
// failed tool call.
var displayBearingTools = map[string]string{
	"query_object_instance":       "datas",
	"get_logic_properties_values": "datas",
	"query_instance_subgraph":     "entries",
	"run_sql":                     "entries",
}

func compileOutputSchema(t *testing.T, locale, tool string) *jsonschema.Schema {
	t.Helper()
	_, raw := loadMCPLocaleBundle(locale).ToolSchemas(tool)
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestDisplayOutputSchemaAcceptsEveryDisplayKeyType(t *testing.T) {
	accepted := map[string]any{
		"integer": float64(7), "float": 3.5, "boolean": true, "string": "Block 7", "empty": nil,
	}
	rejected := map[string]any{
		"object": map[string]any{"name": "x"}, "array": []any{"x"},
	}
	for tool, listKey := range displayBearingTools {
		for _, locale := range []string{"zh-CN", "en-US"} {
			schema := compileOutputSchema(t, locale, tool)
			for name, value := range accepted {
				t.Run(tool+"/"+locale+"/"+name, func(t *testing.T) {
					wire := map[string]any{listKey: []any{map[string]any{"_display": value, "_instance_id": "ot:1"}}}
					if err := schema.Validate(wire); err != nil {
						t.Errorf("a %s display value fails the published schema: %v", name, err)
					}
				})
			}
			for name, value := range rejected {
				t.Run(tool+"/"+locale+"/rejects-"+name, func(t *testing.T) {
					wire := map[string]any{listKey: []any{map[string]any{"_display": value}}}
					if err := schema.Validate(wire); err == nil {
						t.Errorf("schema accepted a %s _display; the loosening must stop at scalars", name)
					}
				})
			}
		}
	}
}

// The handler passes ontology-query's rows through untouched, so what the client validates is
// exactly the display property's own type.
func TestHandleQueryObjectInstanceNumericDisplayMatchesSchema(t *testing.T) {
	stub := &stubOntologyQuery{resp: &interfaces.QueryObjectInstancesResp{
		Data: []any{
			map[string]any{"seq": 0, "_display": 0, "_instance_id": "block:0", "_instance_identity": map[string]any{"id": "b0"}},
			map[string]any{"seq": 1, "_display": nil, "_instance_id": "block:1", "_instance_identity": map[string]any{"id": "b1"}},
		},
	}}
	handler := handleQueryObjectInstance(stub)
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn", "ot_id": "block", "response_format": "json",
	}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected result: err=%v result=%+v", err, result)
	}
	wire := resultToMap(t, result)
	rows := wire["datas"].([]any)
	if got := rows[0].(map[string]any)["_display"]; got != float64(0) {
		t.Fatalf("numeric _display must reach the client unchanged, got %#v", got)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		if err := compileOutputSchema(t, locale, "query_object_instance").Validate(wire); err != nil {
			t.Errorf("%s: structuredContent violates the published schema: %v", locale, err)
		}
	}
}
