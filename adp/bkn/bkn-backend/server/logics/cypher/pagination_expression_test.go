// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPaginationExpressions(t *testing.T) {
	for _, query := range []string{
		"UNWIND [1,2,3,4] AS n RETURN n SKIP $offset LIMIT $take",
		"UNWIND [1,2,3,4] AS n RETURN n SKIP 1+1 LIMIT 1+1",
		"UNWIND [1,2,3,4] AS n WITH n ORDER BY n SKIP $offset LIMIT $take RETURN n",
	} {
		service, _, _ := graphService(t)
		request := graphRequest(query)
		request.ResultFormat = ""
		request.Parameters = map[string]any{"offset": json.Number("2"), "take": int64(2)}
		result, err := service.Query(context.Background(), request)
		if err != nil {
			t.Fatal(query, err)
		}
		assertExpressionRows(t, result.Entries, []map[string]any{{"n": int64(3)}, {"n": int64(4)}})
	}
}
func TestPaginationRejectsInvalidOrCorrelatedValues(t *testing.T) {
	for _, value := range []any{nil, true, "2", float64(2), int64(-1), json.Number("9007199254740993.0"), map[string]any{"n": 2}} {
		service, _, _ := graphService(t)
		request := graphRequest("RETURN 1 AS n LIMIT $take")
		request.Parameters = map[string]any{"take": value}
		if result, err := service.Query(context.Background(), request); err == nil || result != nil {
			t.Fatalf("invalid pagination value accepted: %#v", value)
		}
	}
	for _, query := range []string{"WITH 2 AS n RETURN n LIMIT n", "RETURN 1 AS n LIMIT count(*)", "RETURN 1 AS n LIMIT size([(:Person)-[:WORKS_AT]->(:Company) | 1])"} {
		service, vega, _ := graphService(t)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil || vega.request != nil {
			t.Fatal("pagination read query scope", query, err)
		}
	}
	service, _, _ := graphService(t)
	request := graphRequest("RETURN 1 AS n LIMIT $take")
	request.Parameters = map[string]any{"take": int64(10001)}
	if result, err := service.Query(context.Background(), request); err == nil || result != nil || !strings.Contains(err.Error(), "LimitExceeded") {
		t.Fatal("parameter limit ceiling not enforced", err)
	}
}
