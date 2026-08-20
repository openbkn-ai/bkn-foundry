// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// bigIntegerBoundaries are the values that used to be rounded on the way through
// this adapter. They are, in order: the largest int64, the first value past it
// (the one reported in openbkn-ai/bkn-studio#464), the largest uint64, an ID card
// number (openbkn-ai/bkn-studio#439), and the smallest int64.
var bigIntegerBoundaries = []string{
	"9223372036854775807",
	"9223372036854775808",
	"18446744073709551615",
	"110101199001152345",
	"-9223372036854775808",
}

// rawBodyClient builds a client whose transport hands back body verbatim. The
// raw text matters: a fixture built from a Go map would already have lost the
// precision this test is about.
func rawBodyClient(t *testing.T, body string) *ontologyQueryClient {
	t.Helper()
	return &ontologyQueryClient{
		logger:  &mockLogger{},
		baseURL: "http://ontology.example.com",
		httpClient: &mockHTTPClient{
			bytesFunc: func(_ context.Context, _, _ string, _ map[string]string, _ interface{}) (int, []byte, error) {
				return 200, []byte(body), nil
			},
		},
	}
}

func wantNumber(t *testing.T, value any, want string) {
	t.Helper()
	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value type = %T (%v), want json.Number", value, value)
	}
	if num.String() != want {
		t.Errorf("value = %s, want %s", num.String(), want)
	}
}

func TestQueryObjectInstancesPreservesBigIntegerProperties(t *testing.T) {
	props := make([]string, 0, len(bigIntegerBoundaries))
	for i, literal := range bigIntegerBoundaries {
		props = append(props, `"p`+string(rune('0'+i))+`":`+literal)
	}
	body := `{"datas":[{` + strings.Join(props, ",") + `,"safe":42,"ratio":1.5,"name":"S003"}],"total_count":1}`

	resp, err := rawBodyClient(t, body).QueryObjectInstances(context.Background(),
		&interfaces.QueryObjectInstancesReq{KnID: "kn-1", OtID: "ot-1", Limit: 10})
	if err != nil {
		t.Fatalf("QueryObjectInstances() error = %v", err)
	}

	instance, ok := resp.Data[0].(map[string]any)
	if !ok {
		t.Fatalf("instance type = %T, want map[string]any", resp.Data[0])
	}
	for i, want := range bigIntegerBoundaries {
		wantNumber(t, instance["p"+string(rune('0'+i))], want)
	}
	wantNumber(t, instance["safe"], "42")
	wantNumber(t, instance["ratio"], "1.5")
	if instance["name"] != "S003" {
		t.Errorf("name = %v, want S003", instance["name"])
	}

	// Re-serializing is what the REST and MCP handlers do, and it is where the
	// rounded value used to surface as 9.223372036854776e+18.
	encoded, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range bigIntegerBoundaries {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("re-encoded instance %s is missing %s", encoded, want)
		}
	}
	if strings.Contains(string(encoded), "e+") {
		t.Errorf("re-encoded instance %s fell back to scientific notation", encoded)
	}
}

func TestQueryObjectInstancesKeepsTotalCountTyped(t *testing.T) {
	resp, err := rawBodyClient(t, `{"datas":[],"total_count":7}`).QueryObjectInstances(context.Background(),
		&interfaces.QueryObjectInstancesReq{KnID: "kn-1", OtID: "ot-1", Limit: 10})
	if err != nil {
		t.Fatalf("QueryObjectInstances() error = %v", err)
	}
	if resp.TotalCount == nil || *resp.TotalCount != 7 {
		t.Fatalf("TotalCount = %v, want 7", resp.TotalCount)
	}
}

func TestQueryLogicPropertiesPreservesBigIntegers(t *testing.T) {
	resp, err := rawBodyClient(t, `{"datas":[{"id":18446744073709551615}]}`).QueryLogicProperties(context.Background(),
		&interfaces.QueryLogicPropertiesReq{KnID: "kn-1", OtID: "ot-1"})
	if err != nil {
		t.Fatalf("QueryLogicProperties() error = %v", err)
	}
	wantNumber(t, resp.Datas[0]["id"], "18446744073709551615")
}

func TestQueryMetricDataPreservesBigIntegers(t *testing.T) {
	resp, err := rawBodyClient(t, `{"datas":[{"values":[9223372036854775808]}]}`).QueryMetricData(
		context.Background(), "kn-1", "m-1", false, &interfaces.MetricQueryDownstreamReq{})
	if err != nil {
		t.Fatalf("QueryMetricData() error = %v", err)
	}
	wantNumber(t, resp.Datas[0].Values[0], "9223372036854775808")
}

func TestExploreSubgraphPreservesBigIntegers(t *testing.T) {
	resp, err := rawBodyClient(t, `{"objects":{"o1":{"id":9223372036854775808}},"relation_paths":[]}`).ExploreSubgraph(
		context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn-1", SourceObjectTypeID: "ot-1", Direction: "forward", PathLength: 1, Limit: 10,
		})
	if err != nil {
		t.Fatalf("ExploreSubgraph() error = %v", err)
	}
	object, ok := resp.Objects["o1"].(map[string]any)
	if !ok {
		t.Fatalf("object type = %T, want map[string]any", resp.Objects["o1"])
	}
	wantNumber(t, object["id"], "9223372036854775808")
}

func TestGetActionExecutionPreservesBigIntegers(t *testing.T) {
	resp, err := rawBodyClient(t, `{"result":{"affected_id":18446744073709551615}}`).GetActionExecution(
		context.Background(), &interfaces.GetActionExecutionRequest{KnID: "kn-1", ExecutionID: "e-1"})
	if err != nil {
		t.Fatalf("GetActionExecution() error = %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", resp["result"])
	}
	wantNumber(t, result["affected_id"], "18446744073709551615")
}

// An MCP tool's output is arbitrary business data — a SQL-backed tool returns
// whatever the customer's columns hold — so it goes through the same decoder.
func TestCallMCPToolPreservesBigIntegers(t *testing.T) {
	client := &operatorIntegrationClient{
		logger:  &mockLogger{},
		baseURL: "http://operator.example.com",
		httpClient: &mockHTTPClient{
			bytesFunc: func(_ context.Context, _, _ string, _ map[string]string, _ interface{}) (int, []byte, error) {
				return 200, []byte(`{"rows":[{"id":18446744073709551615}]}`), nil
			},
		},
	}

	result, err := client.CallMCPTool(context.Background(), &interfaces.CallMCPToolRequest{
		McpID: "mcp-1", ToolName: "run_sql",
	})
	if err != nil {
		t.Fatalf("CallMCPTool() error = %v", err)
	}
	rows, ok := result["rows"].([]any)
	if !ok {
		t.Fatalf("rows type = %T, want []any", result["rows"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("row type = %T, want map[string]any", rows[0])
	}
	wantNumber(t, row["id"], "18446744073709551615")
}
