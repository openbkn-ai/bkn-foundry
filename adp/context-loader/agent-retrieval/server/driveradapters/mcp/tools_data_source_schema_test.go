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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// A valid unbound object must not invalidate the entire MCP result (issue #1329).
func TestObjectDataSourceOutputSchema(t *testing.T) {
	for _, tool := range []string{"get_kn_detail", "get_object_types"} {
		for _, locale := range []string{"zh-CN", "en-US"} {
			_, rawSchema := loadMCPLocaleBundle(locale).ToolSchemas(tool)
			var document any
			if err := json.Unmarshal(rawSchema, &document); err != nil {
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
			levels := []string{"full"}
			if tool == "get_kn_detail" {
				levels = []string{"summary", "full"}
			}
			for _, level := range levels {
				for _, format := range []string{"json", "toon"} {
					t.Run(tool+"/"+locale+"/"+level+"/"+format, func(t *testing.T) {
						bkn := &stubMetricBknBackend{detail: &interfaces.KnowledgeNetworkDetail{
							ID: "kn", Name: "Network",
							ConceptGroups: []*interfaces.ConceptGroup{},
							RelationTypes: []*interfaces.RelationType{},
							ActionTypes:   []*interfaces.ActionType{},
							ObjectTypes: []*interfaces.ObjectType{
								{ID: "unbound", Name: "Unbound", PrimaryKeys: []string{"id"}},
								{ID: "bound", Name: "Bound", PrimaryKeys: []string{"id"},
									DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "view", Name: "View"}},
							},
						}}
						metrics := knmetrics.NewKnMetricsServiceWith(nil, bkn, nil)
						handler := handleGetKnDetail(bkn, metrics)
						if tool == "get_object_types" {
							handler = handleGetObjectTypes(bkn, metrics)
						}
						result, err := handler(context.Background(), mcpReq(map[string]any{
							"kn_id": "kn", "ids": []any{"unbound", "bound"},
							"detail_level": level, "response_format": format,
						}))
						if err != nil {
							t.Fatal(err)
						}
						if result.IsError {
							t.Fatalf("tool returned an error: %+v", result)
						}
						wire := resultToMap(t, result)
						objects := wire["object_types"].([]any)
						if len(objects) != 2 {
							t.Fatalf("expected both objects, got %d", len(objects))
						}
						unbound := objects[0].(map[string]any)
						if value, present := unbound["data_source"]; !present || value != nil {
							t.Fatalf("absent binding must remain JSON null: %v", unbound)
						}
						bound := objects[1].(map[string]any)["data_source"].(map[string]any)
						if bound["id"] != "view" || bound["type"] != "resource" || bound["name"] != "View" {
							t.Fatalf("binding changed: %v", bound)
						}
						if err := schema.Validate(wire); err != nil {
							t.Errorf("structuredContent violates published schema: %v", err)
						}
						// Accept null without weakening validation of invalid binding types.
						unbound["data_source"] = "invalid"
						if err := schema.Validate(wire); err == nil {
							t.Error("schema accepted a string data_source")
						}
					})
				}
			}
		}
	}
}
