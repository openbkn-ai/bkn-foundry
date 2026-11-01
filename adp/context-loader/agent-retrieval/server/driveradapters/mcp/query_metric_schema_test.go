// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestQueryMetricConditionSchemaMatchesRuntimeContract(t *testing.T) {
	raw, _ := loadToolSchemas(toolKeyQueryMetric)
	compiled, err := compileExecutableSchema(raw)
	if err != nil {
		t.Fatal(err)
	}

	valid := []string{
		`{"kn_id":"kn","branch":"release","metric_id":"metric","version":1,"query":{"filter":{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"sku_id"}},"comparison_operator":"eq","right":[{"source":"literal","value_type":"int64","value":1}]}}}`,
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"status"}},"comparison_operator":"in","right":[{"source":"literal","value_type":"string","value":"paid"},{"source":"literal","value_type":"string","value":"shipped"}]}}}`,
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"group","boolean_operator":"and","children":[{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"amount"}},"comparison_operator":"gte","right":[{"source":"literal","value_type":"decimal","value":100}]},{"node_type":"group","boolean_operator":"not","children":[{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"cancelled_at"}},"comparison_operator":"is_null"}]}]}}}`,
	}
	for _, document := range valid {
		if err := validateMetricSchemaDocument(compiled, document); err != nil {
			t.Errorf("valid condition was rejected: %v\n%s", err, document)
		}
	}

	invalid := []string{
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"comparison","comparison_operator":"eq"}}}`,
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"status"}},"comparison_operator":"eq","right":[]}}}`,
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"status"}},"comparison_operator":"is_null","right":[{"source":"literal","value_type":"string","value":"x"}]}}}`,
		`{"kn_id":"kn","metric_id":"metric","version":1,"query":{"filter":{"node_type":"group","boolean_operator":"and","children":[{"node_type":"predicate","left":{"source":"property","property_ref":{"object_type_id":"order","property_name":"status"}},"comparison_operator":"is_null"}]}}}`,
	}
	for _, document := range invalid {
		if err := validateMetricSchemaDocument(compiled, document); err == nil {
			t.Errorf("invalid condition was accepted: %s", document)
		}
	}
}

func TestQueryMetricOutputSchemaMatchesRuntimeContract(t *testing.T) {
	_, raw := loadToolSchemas(toolKeyQueryMetric)
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("query-metric-output.json", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("query-metric-output.json")
	if err != nil {
		t.Fatal(err)
	}

	valid, err := json.Marshal(interfaces.QueryMetricResp{
		KnID: "kn", MetricID: "metric", Version: 1, Model: map[string]any{}, Status: "ok",
		Entries: []interfaces.ObjectMetricDataEntry{{
			Status: "ok", TimeBucket: &interfaces.ObjectMetricTimeRange{Start: 1, End: 2},
			DimensionCells: []interfaces.ObjectMetricDimensionCell{{
				Ref: interfaces.ObjectMetricPropertyRef{ObjectTypeID: "order", PropertyName: "status"}, Value: "paid",
			}}, AvailableActions: []string{"filter", "drill"},
		}}, TotalCount: 1, OverallMs: 3, EvaluatedAt: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateJSONSchemaDocument(compiled, string(valid)); err != nil {
		t.Fatalf("valid metric output was rejected: %v", err)
	}

	missingEvaluatedAt := `{"kn_id":"kn","metric_id":"metric","version":1,"model":{},"entries":[],"status":"ok","total_count":0,"overall_ms":3}`
	if err := validateJSONSchemaDocument(compiled, missingEvaluatedAt); err == nil {
		t.Fatal("metric output without evaluated_at was accepted")
	}
	invalidAction := `{"kn_id":"kn","metric_id":"metric","version":1,"model":{},"entries":[{"status":"ok","available_actions":["invented"]}],"status":"ok","total_count":1,"overall_ms":3,"evaluated_at":4}`
	if err := validateJSONSchemaDocument(compiled, invalidAction); err == nil {
		t.Fatal("metric output with an unknown available action was accepted")
	}
}

func validateMetricSchemaDocument(schema *jsonschema.Schema, raw string) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(raw))
	if err != nil {
		return err
	}
	arguments, ok := document.(map[string]any)
	if ok {
		arguments["bkn_context"] = map[string]any{
			"conversation_id": "conv-contract",
			"interaction_id":  "int-contract",
		}
	}
	return schema.Validate(document)
}

func validateJSONSchemaDocument(schema *jsonschema.Schema, raw string) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(raw))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}
