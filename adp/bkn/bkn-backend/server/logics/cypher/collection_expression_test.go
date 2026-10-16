// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCollectionExpressions(t *testing.T) {
	for _, test := range []struct {
		query string
		want  any
	}{
		{"RETURN [x IN [1,2,3] WHERE x > 1 | x*2] AS value", []any{int64(4), int64(6)}},
		{"RETURN [x IN [1,null,3]] AS value", []any{int64(1), nil, int64(3)}},
		{"RETURN [x IN [1,null,3] WHERE x > 1] AS value", []any{int64(3)}},
		{"RETURN [x IN null | x] AS value", nil},
		{"WITH 7 AS x RETURN [x IN [1,2] | [x IN [x+1] | x]] AS value", []any{[]any{int64(2)}, []any{int64(3)}}},
		{"WITH 7 AS x RETURN [x IN [1,2] | x] AS values,x AS value", int64(7)},
		{"RETURN all(x IN [] WHERE x) AS value", true},
		{"RETURN any(x IN [] WHERE x) AS value", false},
		{"RETURN none(x IN [] WHERE x) AS value", true},
		{"RETURN single(x IN [] WHERE x) AS value", false},
		{"RETURN all(x IN [true,null] WHERE x) AS value", nil},
		{"RETURN all(x IN [false,null] WHERE x) AS value", false},
		{"RETURN any(x IN [true,null] WHERE x) AS value", true},
		{"RETURN any(x IN [false,null] WHERE x) AS value", nil},
		{"RETURN none(x IN [false,null] WHERE x) AS value", nil},
		{"RETURN single(x IN [true,null] WHERE x) AS value", nil},
		{"RETURN single(x IN [true,true,null] WHERE x) AS value", false},
		{"RETURN any(x IN null WHERE x) AS value", nil},
	} {
		t.Run(test.query, func(t *testing.T) {
			service, _, _ := graphService(t)
			request := graphRequest(test.query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != 1 || !reflect.DeepEqual(result.Entries[0]["value"], test.want) {
				t.Fatalf("got %+v, want %#v", result.Entries, test.want)
			}
		})
	}
}

func TestCollectionScopeAndTypeFailures(t *testing.T) {
	for _, query := range []string{
		"RETURN [x IN [1,2] | x] AS values,x AS leaked",
		"RETURN [x IN [1,2] WHERE count(*)>0 | x] AS values",
		"RETURN [x IN [1,2] | sum(x)] AS values",
		"RETURN [x IN 3 | x] AS values",
		"RETURN any(x IN [1,2] WHERE x) AS values",
	} {
		service, _, _ := graphService(t)
		result, err := service.Query(context.Background(), graphRequest(query))
		if err == nil || result != nil {
			t.Fatalf("invalid comprehension accepted: %s", query)
		}
	}
}

func TestCollectionGraphOriginsAndAuthorization(t *testing.T) {
	queries := []string{
		"MATCH (p:Person) WITH collect(p) AS people RETURN [q IN people | q.name] AS names",
		"MATCH (p:Person) WITH [p] AS people RETURN head(people).name AS name",
		"MATCH (p:Person) WITH [p] AS people RETURN last(people).name AS name",
		"MATCH (p:Person) WITH CASE WHEN true THEN p ELSE null END AS q RETURN q.name AS name",
		"MATCH (p:Person) WITH coalesce(null,p) AS q RETURN q.name AS name",
		"MATCH (p:Person) WITH [p] AS people UNWIND people[0..1] AS q RETURN q.name AS name",
	}
	for _, query := range queries {
		request := graphRequest(query)
		service, _ := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1", "p.name": "Alice"}}})
		result, err := service.Query(context.Background(), request)
		if err != nil {
			t.Fatal(query, err)
		}
		if len(result.Entries) != 1 || (!reflect.DeepEqual(result.Entries[0]["names"], []any{"Alice"}) && result.Entries[0]["name"] != "Alice") {
			t.Fatalf("lost graph origin: %+v", result.Entries)
		}
		denied, vega, permission := graphService(t)
		permission.levels = map[string]string{"kn_1/person.name": "masked"}
		if result, err := denied.Query(context.Background(), request); err == nil || result != nil || vega.request != nil {
			t.Fatal("comprehension/function bypassed authorization", query, err)
		}
	}
}

func TestCollectionIterationBudget(t *testing.T) {
	service, _, _ := graphService(t)
	result, err := service.Query(context.Background(), graphRequest("RETURN [x IN range(1,100) | size([y IN range(1,1000) | y])] AS values"))
	if err == nil || result != nil || !strings.Contains(err.Error(), "LimitExceeded") {
		t.Fatalf("nested iteration was not bounded: %+v %v", result, err)
	}
}
