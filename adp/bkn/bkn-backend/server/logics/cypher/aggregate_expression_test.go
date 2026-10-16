// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"testing"
)

func TestAggregateExpressions(t *testing.T) {
	for _, test := range []struct {
		query string
		rows  []map[string]any
	}{
		{"UNWIND [1,2,3] AS x RETURN sum(x)+count(*) AS result", []map[string]any{{"result": int64(9)}}},
		{"UNWIND [1,2,3] AS x RETURN stdev(x) AS result", []map[string]any{{"result": float64(1)}}},
		{"UNWIND [1,3] AS x RETURN stdevp(x) AS result", []map[string]any{{"result": float64(1)}}},
		{"UNWIND [1,2,3,4] AS x RETURN [percentileCont(x,0.25),percentileDisc(x,0.75)] AS result", []map[string]any{{"result": []any{float64(1.75), float64(3)}}}},
		{"UNWIND [] AS x RETURN [stdev(x),percentileCont(x,0.5)] AS result", []map[string]any{{"result": []any{float64(0), nil}}}},
		{"UNWIND [1,2,3] AS x RETURN round(avg(x)) AS result", []map[string]any{{"result": float64(2)}}},
		{"UNWIND [1,2,3] AS x RETURN {count:count(*),total:sum(x)} AS result", []map[string]any{{"result": map[string]any{"count": int64(3), "total": int64(6)}}}},
		{"UNWIND [1,2,3] AS x RETURN [count(*),sum(x)] AS result", []map[string]any{{"result": []any{int64(3), int64(6)}}}},
		{"UNWIND [1,2,3] AS x RETURN CASE WHEN count(*)>1 THEN head(collect(x)) ELSE 0 END AS result", []map[string]any{{"result": int64(1)}}},
		{"UNWIND [1,1,2] AS x RETURN x+count(*) AS result ORDER BY result", []map[string]any{{"result": int64(3)}, {"result": int64(3)}}},
		{"UNWIND [1,1,2] AS x RETURN DISTINCT x+count(*) AS result ORDER BY result", []map[string]any{{"result": int64(3)}}},
		{"UNWIND [] AS x RETURN 7 AS constant,count(*) AS result", []map[string]any{{"constant": int64(7), "result": int64(0)}}},
		{"UNWIND [] AS x RETURN 1+count(*) AS result", []map[string]any{{"result": int64(1)}}},
		{"UNWIND [] AS x RETURN coalesce(avg(x),99) AS result", []map[string]any{{"result": int64(99)}}},
		{"UNWIND [] AS x RETURN [z IN [1,2] | z] AS values,count(*) AS result", []map[string]any{{"values": []any{int64(1), int64(2)}, "result": int64(0)}}},
		{"UNWIND [1,2,3] AS x WITH {total:sum(x)} AS total WHERE total.total>5 RETURN total.total AS result", []map[string]any{{"result": int64(6)}}},
		{"UNWIND [1,1,2] AS x RETURN x+count(*) AS result ORDER BY x+count(*) LIMIT 1", []map[string]any{{"result": int64(3)}}},
	} {
		t.Run(test.query, func(t *testing.T) {
			service, _, _ := graphService(t)
			request := graphRequest(test.query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, test.rows)
		})
	}
}
func TestAggregatesCannotEscapeTheirScope(t *testing.T) {
	for _, query := range []string{
		"RETURN sum(count(*)+1) AS result",
		"UNWIND [1] AS x RETURN stdev('a') AS result",
		"UNWIND [1,2] AS x RETURN percentileCont(x,x) AS result",
		"RETURN collect({n:count(*)}) AS result",
		"UNWIND [1] AS x WITH x WHERE sum(x)+1>0 RETURN x",
		"UNWIND count(*)+1 AS x RETURN x",
		"RETURN [x IN [1] | count(*)+1] AS result",
	} {
		service, _, _ := graphService(t)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil {
			t.Fatal("aggregate escaped scope", query, err)
		}
	}
}

func TestWithFilterDoesNotReuseVolatileProjection(t *testing.T) {
	for _, function := range []string{"rand()", "datetime.realtime()"} {
		service, _, _ := graphService(t)
		compiled, err := service.compile(context.Background(), graphRequest("WITH DISTINCT "+function+" AS value WHERE "+function+"=value RETURN value"))
		if err != nil {
			t.Fatal(err)
		}
		predicate := compiled.stages[0].plan.Result.PostWhere
		if predicate.Operation != "=" || len(predicate.Arguments) != 2 || !containsVolatileExpression(predicate.Arguments[0]) || predicate.Arguments[0].Binding != "" {
			t.Fatal("fresh filter call was replaced by the projection")
		}
	}
}
