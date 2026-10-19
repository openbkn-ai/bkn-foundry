// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// graphService is the relation model with primary keys, which a node value
// needs for its identity, configured for the bind-join executor: a query
// returning graph values runs on the SQL path anyway.
func graphService(vega interfaces.VegaBackendAccess) *cypherQueryService {
	service := relationService(nil, &stubPermission{}, nil)
	service.vba = vega
	service.appSetting = &common.AppSetting{ServerSetting: common.ServerSetting{CypherExecutor: common.CypherExecutorBindJoin}}
	for _, objectType := range service.schema.(*fakeSchemaSource).objectTypes {
		switch objectType.OTID {
		case "ot_order":
			objectType.PrimaryKeys = []string{"id"}
		case "ot_customer":
			objectType.PrimaryKeys = []string{"code"}
		}
	}
	return service
}

// Without collect, graph values expand to their columns in the statement,
// which groups and counts; the service only builds the node from them.
func TestGraphQueryRunsOnTheSQLPathWhateverTheExecutor(t *testing.T) {
	spans := recordSpans(t)
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{
		{"orders": json.Number("2"), "_graph_0": "c-1", "_graph_1": "Ann"},
	}}}
	service := graphService(vega)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query:            "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c AS customer, count(*) AS orders",
		ObjectProperties: map[string][]string{"c": {"name"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil {
		t.Fatal("a graph query did not run on the SQL path")
	}
	for _, want := range []string{
		"SELECT COUNT(*) AS `orders`, t1.`f_code` AS `_graph_0`, t1.`f_name` AS `_graph_1`",
		"GROUP BY t1.`f_code`, t1.`f_name`",
	} {
		if !strings.Contains(vega.request.Query, want) {
			t.Errorf("statement lacks %q:\n%s", want, vega.request.Query)
		}
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %v, want one customer", result.Entries)
	}
	customer, ok := result.Entries[0]["customer"].(interfaces.CypherObjectValue)
	if !ok {
		t.Fatalf("customer = %T, want a node value", result.Entries[0]["customer"])
	}
	wantKey := map[string]interfaces.CypherKeyValue{"code": {Type: "string", Value: "c-1"}}
	if customer.ObjectTypeID != "ot_customer" || !reflect.DeepEqual(customer.Key, wantKey) ||
		customer.Properties["name"] != "Ann" {
		t.Fatalf("customer = %+v", customer)
	}
	if n, _ := strconv.Atoi(jsonText(result.Entries[0]["orders"])); n != 2 {
		t.Fatalf("orders = %v, want 2", result.Entries[0]["orders"])
	}
	if got := result.ValueTypes["customer"]; got == nil || got.Kind != interfaces.CYPHER_KIND_OBJECT ||
		got.ObjectTypeID != "ot_customer" {
		t.Fatalf("value_types = %+v", result.ValueTypes)
	}
	if result.Execution == nil || result.Execution.MatchRows != 1 || !result.Execution.MatchComplete ||
		result.Execution.ConclusionsLimited {
		t.Fatalf("execution = %+v", result.Execution)
	}
	if spans.attrs["cypher.executor"] != "sql" || spans.attrs["cypher.graph_values"] != "true" {
		t.Fatalf("span attrs = %v", spans.attrs)
	}
}

// With collect, LIMIT chooses conclusions in a derived table and the
// statement returns every matched row of those, which the service groups.
func TestGraphEvidenceLimitsConclusionsNotEvidence(t *testing.T) {
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{
		{"_graph_0": "c-1", "_graph_1": "o-1"},
		{"_graph_0": "c-1", "_graph_1": "o-2"},
		{"_graph_0": "c-2", "_graph_1": "o-3"},
	}}}
	service := graphService(vega)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c AS customer, collect(o.id) AS ids LIMIT 2",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	statement := vega.request.Query
	for _, want := range []string{
		"JOIN (SELECT DISTINCT t1.`f_code` AS `_key_0` FROM",
		"LIMIT 2) c0 ON (t1.`f_code` = c0.`_key_0` OR (t1.`f_code` IS NULL AND c0.`_key_0` IS NULL))",
	} {
		if !strings.Contains(statement, want) {
			t.Errorf("statement lacks %q:\n%s", want, statement)
		}
	}
	if !strings.HasSuffix(statement, " LIMIT 10000") || strings.Count(statement, "LIMIT") != 2 ||
		strings.Contains(statement, " IN (SELECT") || strings.Contains(statement, "GROUP BY") {
		t.Errorf("want LIMIT only on the conclusions and the row cap, no IN subquery or grouping:\n%s", statement)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %v, want two conclusions", result.Entries)
	}
	evidence := map[string][]any{}
	for _, entry := range result.Entries {
		customer := entry["customer"].(interfaces.CypherObjectValue)
		evidence[customer.Key["code"].Value] = entry["ids"].([]any)
	}
	if !reflect.DeepEqual(evidence, map[string][]any{"c-1": {"o-1", "o-2"}, "c-2": {"o-3"}}) {
		t.Fatalf("evidence = %v", evidence)
	}
	if result.Execution.MatchRows != 3 || !result.Execution.ConclusionsLimited {
		t.Fatalf("execution = %+v, want 3 rows and limited conclusions", result.Execution)
	}
}

// A graph answer is complete or refused: evidence reaching the row cap is
// not returned in part.
func TestGraphEvidenceAtTheRowCapIsRefused(t *testing.T) {
	rows := make([]map[string]any, graphRowCap)
	for i := range rows {
		rows[i] = map[string]any{"_graph_0": strconv.Itoa(i)}
	}
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: rows}}
	service := graphService(vega)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN collect(o.id) AS ids",
	})
	if err == nil {
		t.Fatal("Query succeeded at the row cap")
	}
	if statusOf(t, err) != http.StatusBadRequest || errorCodeOf(t, err) != berrors.BknBackend_Cypher_ReadBudgetExceeded {
		t.Fatalf("error = %v, want 400 %s", err, berrors.BknBackend_Cypher_ReadBudgetExceeded)
	}
	if strings.Contains(vega.request.Query, "c0") {
		t.Fatalf("nothing beside collect, so no conclusions to choose:\n%s", vega.request.Query)
	}
}

func TestGraphQueryRefusesObjectPropertiesForAnUnreturnedVariable(t *testing.T) {
	vega := &recordingVega{}
	service := graphService(vega)

	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query:            "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o AS ord",
		ObjectProperties: map[string][]string{"c": {"name"}},
	})
	if err == nil || statusOf(t, err) != http.StatusBadRequest {
		t.Fatalf("error = %v, want 400", err)
	}
	if vega.request != nil {
		t.Fatal("a refused query read data")
	}
}

func jsonText(value any) string {
	if n, ok := asNumber(value); ok {
		return keyText(n.value())
	}
	return ""
}

// Evidence past the default row limit of a scalar query comes back whole.
func TestGraphEvidencePastTheDefaultLimitIsComplete(t *testing.T) {
	rows := make([]map[string]any, interfaces.CYPHER_DEFAULT_LIMIT+500)
	for i := range rows {
		rows[i] = map[string]any{"_graph_0": "c-1", "_graph_1": "o-" + strconv.Itoa(i)}
	}
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: rows}}
	service := graphService(vega)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c AS customer, collect(o.id) AS ids",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request.Paging.Limit != graphRowCap {
		t.Fatalf("paging limit = %d, want the row cap", vega.request.Paging.Limit)
	}
	if len(result.Entries) != 1 || len(result.Entries[0]["ids"].([]any)) != len(rows) {
		t.Fatalf("want one conclusion with all %d rows of evidence", len(rows))
	}
}
