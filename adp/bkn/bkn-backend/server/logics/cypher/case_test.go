// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestCaseAndCoalesceGenerate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "a conditional count",
			query: "MATCH (o:Order) RETURN sum(CASE WHEN o.region = 'east' THEN 1 ELSE 0 END) AS east, count(*) AS n",
			want:  []string{"SELECT SUM(CASE WHEN t0.`f_region` = 'east' THEN 1 ELSE 0 END) AS `east`, COUNT(*) AS `n`"},
		},
		{
			name:  "count skips the rows CASE gives null for",
			query: "MATCH (o:Order) RETURN count(CASE WHEN o.amount > 40 THEN 1 END) AS big",
			want:  []string{"COUNT(CASE WHEN t0.`f_amount` > 40 THEN 1 END) AS `big`"},
		},
		{
			name:  "the simple form compares its subject with each value",
			query: "MATCH (o:Order) RETURN o.id AS id, CASE o.region WHEN 'east' THEN 'E' WHEN $west THEN 'W' ELSE 'other' END AS r",
			want:  []string{"CASE WHEN t0.`f_region` = 'east' THEN 'E' WHEN t0.`f_region` = 'west' THEN 'W' ELSE 'other' END AS `r`"},
		},
		{
			name:  "a CASE that groups",
			query: "MATCH (o:Order) RETURN CASE WHEN o.amount > 40 THEN 'big' ELSE 'small' END AS size, count(*) AS n",
			want:  []string{"GROUP BY CASE WHEN t0.`f_amount` > 40 THEN 'big' ELSE 'small' END"},
		},
		{
			name:  "coalesce as a grouping key and in WHERE",
			query: "MATCH (o:Order) WHERE coalesce(o.amount, 0) > 30 RETURN coalesce(o.region, 'unknown') AS r, count(*) AS n",
			want:  []string{"WHERE COALESCE(t0.`f_amount`, 0) > 30", "GROUP BY COALESCE(t0.`f_region`, 'unknown')"},
		},
		{
			name:  "after a grouping WITH, a branch tests an aggregate by its name",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN r, CASE WHEN n > 1 THEN 'many' ELSE 'one' END AS k",
			want:  []string{"CASE WHEN COUNT(*) > 1 THEN 'many' ELSE 'one' END AS `k`", "GROUP BY t0.`f_region`"},
		},
		{
			name:  "a CASE in arithmetic and in ORDER BY",
			query: "MATCH (o:Order) RETURN o.id AS id ORDER BY CASE WHEN o.region = 'east' THEN 0 ELSE 1 END + o.amount, id",
			want:  []string{"ORDER BY CASE WHEN t0.`f_region` = 'east' THEN 0 ELSE 1 END + t0.`f_amount`, `id`"},
		},
		{
			name:  "a null branch says nothing about the kind",
			query: "MATCH (o:Order) RETURN CASE WHEN o.amount > 40 THEN o.region ELSE null END AS r",
			want:  []string{"CASE WHEN t0.`f_amount` > 40 THEN t0.`f_region` ELSE NULL END AS `r`"},
		},
		{
			name:  "a string coalesce against a string",
			query: "MATCH (o:Order) WHERE coalesce(o.region, o.customer_code) = 'east' OR coalesce(o.region, 'x') = $west RETURN o.id",
			want: []string{
				"COALESCE(t0.`f_region`, t0.`f_cust`) = 'east'",
				"COALESCE(t0.`f_region`, 'x') = 'west'",
			},
		},
		{
			name:  "a string coalesce against a string property",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE coalesce(o.region, 'x') = c.region RETURN o.id",
			want:  []string{"COALESCE(t0.`f_region`, 'x') = t1.`f_region`"},
		},
		{
			// The earlier stage is planned on its own; its CASE or coalesce
			// still makes the whole query SQL-only, which compileCase checks.
			name: "a coalesce in the stage before a semi-join",
			query: "MATCH (o:Order) WHERE coalesce(o.amount, 0) > 30 WITH DISTINCT o.customer_code AS c " +
				"MATCH (k:Customer) WHERE k.code = c RETURN k.name",
			want: []string{"COALESCE(t0.`f_amount`, 0) > 30"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileCase(t, tc.query)
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(sql, want) {
					t.Errorf("SQL lacks %q:\n%s", want, sql)
				}
			}
		})
	}
}

func TestCaseAndCoalesceRefuse(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"MATCH (o:Order) RETURN CASE WHEN o.amount > 40 THEN 'big' ELSE 1 END AS k", "mix numbers with other values"},
		{"MATCH (o:Order) RETURN coalesce(o.region, 0) AS k", "mix numbers with other values"},
		{"MATCH (o:Order) RETURN CASE WHEN o.amount > 40 THEN true ELSE false END AS k", "a boolean as the result of CASE"},
		{"MATCH (o:Order) RETURN coalesce(o.region, 'x') + 1 AS k", "arithmetic takes numbers"},
		{"MATCH (o:Order) RETURN sum(CASE WHEN o.amount > 1 THEN o.region END) AS k", "sum takes numbers"},
		{"MATCH (o:Order) RETURN CASE o.region WHEN null THEN 1 END AS k", "WHEN null in a simple CASE"},
		{"MATCH (o:Order) RETURN coalesce(o.region) AS k", "coalesce takes two or more values"},
		{"MATCH (o:Order) RETURN CASE WHEN count(*) > 1 THEN 1 END AS k", "count(*) is not supported"},
		{"MATCH (o:Order) RETURN CASE WHEN o.region = r THEN 1 END AS k", "referring to a node as a value"},
		{"MATCH (o:Order) RETURN CASE WHEN 1 = 1 THEN 1 ELSE 2 END AS k", "a comparison must read a property"},
		{"MATCH (o:Order) WHERE coalesce(o.region, 'unknown') = 0 RETURN o.id", "comparing a number with a value that is not one"},
		{"MATCH (o:Order) WHERE coalesce(o.amount, 0) = 'x' RETURN o.id", "comparing a computed number with string"},
		{"MATCH (o:Order) WHERE coalesce(o.region, 'x') = $flag RETURN o.id", "parameter \"flag\" is compared with a computed value"},
		{"MATCH (o:Order) WHERE coalesce(o.region, 'x') = o.rush RETURN o.id", "o.rush, which is boolean"},
		{"MATCH (o:Order) RETURN CASE WHEN o.amount > 1 THEN o.rush END AS k", "o.rush is boolean"},
	} {
		_, err := compileCase(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// compileCase compiles against the typed test model, with the parameters the
// cases above use.
func compileCase(t *testing.T, query string) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{Parameters: map[string]any{"west": "west", "flag": true}})
	if err != nil {
		return "", err
	}
	if !plan.SQLOnly {
		t.Errorf("%s: a plan with CASE or coalesce is not marked SQL-only", query)
	}
	return Generate(plan, GenerateOptions{})
}

// CASE and coalesce are the SQL path's alone, so they run there whichever
// executor is configured, and the trace names them by kind with the
// properties they read.
func TestCaseRunsOnTheSQLPathWhateverTheExecutor(t *testing.T) {
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{
		{"big": json.Number("2")},
	}}}
	service := graphService(vega)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN count(CASE WHEN o.amount > '40' THEN o.customer_code END) AS big, " +
			"coalesce(o.customer_code, 'none') AS c",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil || !strings.Contains(vega.request.Query, "COUNT(CASE WHEN t0.`f_total` > '40' THEN t0.`f_cust_code` END)") {
		t.Fatalf("CASE did not run on the SQL path: %+v", vega.request)
	}
	var descriptor SemanticQueryDescriptor
	if err := json.Unmarshal(result.TraceDescriptor, &descriptor); err != nil {
		t.Fatalf("trace descriptor: %v", err)
	}
	if len(descriptor.Projections) != 2 {
		t.Fatalf("projections = %+v", descriptor.Projections)
	}
	big, coalesced := descriptor.Projections[0], descriptor.Projections[1]
	if big.Aggregate != "count" || big.Expression != "arithmetic" ||
		strings.Join(big.PropertyRefs, ",") != "property:kn_1:ot_order:amount,property:kn_1:ot_order:customer_code" {
		t.Errorf("count(CASE ...) projection = %+v", big)
	}
	if coalesced.Expression != "coalesce" || strings.Join(coalesced.PropertyRefs, ",") != "property:kn_1:ot_order:customer_code" {
		t.Errorf("coalesce projection = %+v", coalesced)
	}
}
