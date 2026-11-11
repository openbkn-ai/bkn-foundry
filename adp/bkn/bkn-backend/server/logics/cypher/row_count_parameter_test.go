// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// SKIP and LIMIT may take a parameter, so a statement saved as a function can
// let its caller ask for the first N rows or a page. The value is bound before
// the query is planned, under the same rules as one written in the query.
func TestRowCountParameters(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       [][]any
	}{
		{"limit", "MATCH (o:Order) RETURN o.id AS id ORDER BY id LIMIT $n",
			map[string]any{"n": 2}, [][]any{{1}, {2}}},
		{"skip and limit", "MATCH (o:Order) RETURN o.id AS id ORDER BY id SKIP $from LIMIT $n",
			map[string]any{"from": 1, "n": 2}, [][]any{{2}, {3}}},
		{"a whole number sent as a float", "MATCH (o:Order) RETURN o.id AS id ORDER BY id LIMIT $n",
			map[string]any{"n": 1.0}, [][]any{{1}}},
		{"zero", "MATCH (o:Order) RETURN o.id AS id LIMIT $n",
			map[string]any{"n": 0}, [][]any{}},
		{"after a WITH", "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n RETURN o.id AS id, n ORDER BY id LIMIT $top",
			map[string]any{"top": 1}, [][]any{{1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, tc.parameters)
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
	sql, err := compileComparison(t, "MATCH (o:Order) RETURN o.id ORDER BY o.id SKIP $from LIMIT $n",
		map[string]any{"from": 5, "n": 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "LIMIT 3 OFFSET 5") {
		t.Fatalf("SQL lacks the bound page:\n%s", sql)
	}
}

func TestRowCountParameterRejections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{"not supplied", "MATCH (o:Order) RETURN o.id LIMIT $n", nil, `parameter "n" was not supplied`},
		{"a fraction", "MATCH (o:Order) RETURN o.id LIMIT $n", map[string]any{"n": 2.5}, `parameter "n" is used as LIMIT`},
		{"negative", "MATCH (o:Order) RETURN o.id SKIP $n", map[string]any{"n": -1}, `parameter "n" is used as SKIP`},
		{"a string", "MATCH (o:Order) RETURN o.id LIMIT $n", map[string]any{"n": "3"}, "got string"},
		{"null", "MATCH (o:Order) RETURN o.id LIMIT $n", map[string]any{"n": nil}, `parameter "n"`},
		{"a list", "MATCH (o:Order) RETURN o.id LIMIT $n", map[string]any{"n": []any{1}}, `parameter "n"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileComparison(t, tc.query, tc.parameters)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A bound LIMIT meets the same ceiling as a written one, in the query and in
// each part of a UNION, and is refused before anything is read.
func TestRowCountParameterCeiling(t *testing.T) {
	over := interfaces.CYPHER_MAX_LIMIT + 1
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"query", "MATCH (o:Order) RETURN o.id LIMIT $n"},
		{"UNION part", "MATCH (o:Order) RETURN o.id AS id LIMIT $n UNION MATCH (o:Order) RETURN o.id AS id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{}
			service := testService(t, vega, &stubPermission{})
			_, err := service.Query(context.Background(), interfaces.CypherQuery{
				KNID: "kn_1", Query: tc.query, Parameters: map[string]any{"n": over},
			})
			if got := statusOf(t, err); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", got)
			}
			if code := errorCodeOf(t, err); code != "BknBackend.Cypher.LimitExceeded" {
				t.Fatalf("error code = %s, want the one a written LIMIT gets", code)
			}
			if vega.request != nil {
				t.Fatalf("a refused query still reached vega-backend: %s", vega.request.Query)
			}
		})
	}
	// A bound value that is not a row count is the caller's input, not the query's syntax.
	vega := &recordingVega{}
	service := testService(t, vega, &stubPermission{})
	_, err := service.Query(context.Background(), interfaces.CypherQuery{
		KNID: "kn_1", Query: "MATCH (o:Order) RETURN o.id LIMIT $n", Parameters: map[string]any{"n": 2.5},
	})
	if got := statusOf(t, err); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}
