// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func compileWithClause(t *testing.T, query string) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
	if err != nil {
		return "", err
	}
	return Generate(plan, GenerateOptions{})
}

func TestWithGenerates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name: "a node groups by its key, and the aggregate is tested per group",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(*) AS n WHERE n > 1 " +
				"RETURN o.region AS region, n ORDER BY n DESC",
			want: []string{"SELECT t1.`f_region` AS `region`, COUNT(*) AS `n`", "GROUP BY t1.`f_id`, t1.`f_region`",
				"HAVING COUNT(*) > 1", "ORDER BY `n` DESC"},
		},
		{
			name:  "a condition on a grouped value filters rows before grouping",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE r = 'east' AND n >= 1 RETURN r, n",
			want:  []string{"WHERE t0.`f_region` = 'east'", "GROUP BY t0.`f_region`", "HAVING COUNT(*) >= 1"},
		},
		{
			name:  "a condition on an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, sum(o.amount) AS total WHERE total > 60 RETURN r",
			want:  []string{"SELECT t0.`f_region` AS `r`", "GROUP BY t0.`f_region`", "HAVING SUM(t0.`f_amount`) > 60"},
		},
		{
			name:  "a property tested beside an aggregate is grouped by too",
			query: "MATCH (o:Order) WITH o, count(*) AS n WHERE n > 1 OR o.amount > 60 RETURN o.id AS id",
			want:  []string{"GROUP BY t0.`f_id`, t0.`f_amount`", "HAVING COUNT(*) > 1 OR t0.`f_amount` > 60"},
		},
		{
			name:  "a WITH that only renames",
			query: "MATCH (o:Order) WITH o.amount AS a WHERE a > 30 RETURN a ORDER BY a",
			want:  []string{"SELECT t0.`f_amount` AS `a`", "WHERE t0.`f_amount` > 30", "ORDER BY `a`"},
		},
		{
			name: "a second WITH computes from the first, and its condition on an aggregate tests each group",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o.region AS r, o.amount AS a, count(*) AS n " +
				"WITH r, a * 2 AS doubled, n - a AS gap WHERE gap < 0 AND doubled > 10 RETURN r, doubled, gap",
			want: []string{"SELECT t1.`f_region` AS `r`, t1.`f_amount` * 2 AS `doubled`, COUNT(*) - t1.`f_amount` AS `gap`",
				"WHERE t1.`f_amount` * 2 > 10", "GROUP BY t1.`f_region`, t1.`f_amount`", "HAVING COUNT(*) - t1.`f_amount` < 0"},
		},
		{
			name: "sorting by a computed name that is not returned",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o.region AS r, o.amount AS a, count(*) AS n " +
				"WITH r, a, n - a AS gap RETURN r ORDER BY gap, a",
			want: []string{"GROUP BY t1.`f_region`, t1.`f_amount`", "ORDER BY COUNT(*) - t1.`f_amount`, t1.`f_amount`"},
		},
		{
			name:  "arithmetic in the WHERE of a WITH",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n * 2 > 1 RETURN r",
			want:  []string{"HAVING COUNT(*) * 2 > 1"},
		},
		{
			name:  "the size of a collected list is the count of what it collected",
			query: "MATCH (o:Order) WITH o.customer_code AS c, collect(DISTINCT o.region) AS regions WHERE size(regions) >= 2 RETURN c",
			want:  []string{"GROUP BY t0.`f_cust`", "HAVING COUNT(DISTINCT t0.`f_region`) >= 2"},
		},
		{
			name:  "RETURN counts the groups through a derived table",
			query: "MATCH (o:Order) WITH o.customer_code AS c, count(*) AS n WHERE n > 1 RETURN count(*) AS customers",
			want: []string{"SELECT COUNT(*) AS `customers` FROM (SELECT COUNT(*) AS `group_rows` FROM {{.res_order}} t0 " +
				"GROUP BY t0.`f_cust` HAVING COUNT(*) > 1) g"},
		},
		{
			name:  "RETURN aggregates a value each group yields",
			query: "MATCH (o:Order) WITH o.region AS r, sum(o.amount) AS total RETURN max(total) AS top, count(DISTINCT total) AS kinds LIMIT 5",
			want: []string{"SELECT MAX(g.`total`) AS `top`, COUNT(DISTINCT g.`total`) AS `kinds` FROM (SELECT SUM(t0.`f_amount`) AS `total` " +
				"FROM {{.res_order}} t0 GROUP BY t0.`f_region`) g LIMIT 5"},
		},
		{
			name:  "an opening WITH names a list that IN uses",
			query: "WITH ['east', 'west'] AS regions, 50 AS floor MATCH (o:Order) WHERE o.region IN regions AND o.amount >= floor RETURN o.id",
			want:  []string{"WHERE t0.`f_region` IN ('east', 'west') AND t0.`f_amount` >= 50"},
		},
		{
			name: "an opening WITH before a semi-join",
			query: "WITH ['c1'] AS codes MATCH (o:Order) WHERE o.customer_code IN codes WITH DISTINCT o.region AS r " +
				"MATCH (c:Customer) WHERE c.region = r RETURN c.name",
			want: []string{"WHERE t0.`f_cust` IN ('c1')", "s0 ON t0.`f_region` = s0.`value`"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileWithClause(t, tc.query)
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

// The executor answers a WITH the way the SQL path does: groups by what the
// WITH grouped by, then keeps the groups its condition holds for.
func TestWithEvaluates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			name: "orders with more than one item",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(*) AS n WHERE n > 1 " +
				"RETURN o.region AS region, n",
			want: [][]any{{"east", 2}},
		},
		{
			name:  "two objects sharing a property stay two groups",
			query: "MATCH (o:Order) WITH o, count(*) AS n RETURN o.region AS r, n ORDER BY r",
			want:  [][]any{{nil, 1}, {"east", 1}, {"east", 1}, {"west", 1}},
		},
		{
			name:  "a condition on a grouped value",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE r = 'east' RETURN r, n",
			want:  [][]any{{"east", 2}},
		},
		{
			name:  "a condition on an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n >= 2 RETURN r",
			want:  [][]any{{"east"}},
		},
		{
			name:  "sorting by an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN r ORDER BY n DESC, r",
			want:  [][]any{{"east"}, {nil}, {"west"}},
		},
		{
			name:  "sum tested per group",
			query: "MATCH (o:Order) WITH o.region AS r, sum(o.amount) AS total WHERE total > 60 RETURN r, total ORDER BY r",
			want:  [][]any{{"east", 70}, {"west", 70}},
		},
		{
			name:  "a LIMIT does not cut the rows a group is counted over",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n >= 2 RETURN r LIMIT 1",
			want:  [][]any{{"east"}},
		},
		{
			name: "OR mixing an aggregate with a property of the grouped node",
			query: "MATCH (o:Order) WITH o, count(*) AS n WHERE n > 1 OR o.amount > 60 " +
				"RETURN o.id AS id ORDER BY id",
			want: [][]any{{2}},
		},
		{
			name: "a date in the per-group condition compares by wall clock",
			query: "MATCH (o:Order) WITH o, count(*) AS n WHERE o.placed_at > '2024-01-31 23:59:59' OR n > 5 " +
				"RETURN o.id AS id ORDER BY id",
			want: [][]any{{3}},
		},
		{
			name:  "one group, condition fails",
			query: "MATCH (o:Order) WITH count(*) AS n WHERE n > 10 RETURN n",
			want:  [][]any{},
		},
		{
			name:  "one group, condition holds",
			query: "MATCH (o:Order) WITH count(*) AS n WHERE n > 1 RETURN n",
			want:  [][]any{{4}},
		},
		{
			name:  "a WITH that only renames, counted",
			query: "MATCH (o:Order) WITH o.amount AS a WHERE a > 30 RETURN count(*) AS n",
			want:  [][]any{{2}},
		},
		{
			name:  "a renamed node",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o AS ord, count(i) AS n RETURN ord.id AS id, n ORDER BY id",
			want:  [][]any{{1, 2}, {2, 1}},
		},
		{
			name: "a second WITH computes from the first and tests each group",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o.region AS r, o.amount AS a, count(*) AS n " +
				"WITH r, a * 2 AS doubled, n - a AS gap WHERE gap < 0 RETURN r, doubled, gap ORDER BY r",
			want: [][]any{{"east", 100, -48}, {"west", 140, -69}},
		},
		{
			name: "the size of a collected list skips nulls as collect does",
			query: "MATCH (o:Order) WITH o.customer_code AS c, collect(DISTINCT o.region) AS regions " +
				"WHERE size(regions) >= 2 RETURN c",
			want: [][]any{{"c1"}},
		},
		{
			name:  "an opening WITH names a list",
			query: "WITH ['east'] AS rs MATCH (o:Order) WHERE o.region IN rs RETURN o.id AS id ORDER BY id",
			want:  [][]any{{1}, {3}},
		},
		{
			name: "an optional node counted per group",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WITH o, count(i) AS items WHERE items = 0 " +
				"RETURN o.id AS id ORDER BY id",
			want: [][]any{{3}, {4}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, nil)
			if len(rows) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

// A parameter an opening WITH names is a list where IN uses it and one value
// where a comparison does.
func TestWithConstantParameter(t *testing.T) {
	for query, want := range map[string]string{
		"WITH $regions AS regions MATCH (o:Order) WHERE NOT o.region IN regions RETURN o.id": "WHERE NOT t0.`f_region` IN ('east', 'west')",
		"WITH $floor AS floor MATCH (o:Order) WHERE o.amount > floor RETURN o.id":            "WHERE t0.`f_amount` > 30",
	} {
		tree, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse(%q): %v", query, err)
		}
		analyzed, err := Analyze(tree)
		if err != nil {
			t.Fatalf("Analyze(%q): %v", query, err)
		}
		plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{
			Parameters: map[string]any{"regions": []any{"east", "west"}, "floor": 30},
		})
		if err != nil {
			t.Fatalf("Compile(%q): %v", query, err)
		}
		sql, err := Generate(plan, GenerateOptions{})
		if err != nil {
			t.Fatalf("Generate(%q): %v", query, err)
		}
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q:\n%s", want, sql)
		}
	}
}

func TestWithRefuses(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"MATCH (o:Order) WITH DISTINCT o.region AS r RETURN r", "WITH DISTINCT"},
		{"MATCH (o:Order) WITH * RETURN o.id", "WITH *"},
		{"MATCH (o:Order) WITH o.region AS r ORDER BY r RETURN r", "ORDER BY, SKIP or LIMIT on WITH"},
		{"MATCH (o:Order) WITH o.region RETURN o.region", "an unnamed value in WITH"},
		{"MATCH (o:Order) WITH o MATCH (o)-[:PLACED_BY]->(c:Customer) RETURN c.name", "pass one property to the next MATCH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WITH r, count(*) AS m RETURN r", "an aggregate after WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WITH r, 2 AS k RETURN r", "a constant in WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN r, sum(n)", "this RETURN over a WITH's groups"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN sum(n) ORDER BY r", "DISTINCT or ORDER BY on a RETURN"},
		{"MATCH (o:Order) WITH o, count(*) AS n RETURN count(o)", "aggregating o over the groups"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN count(*), count(*)", "column name \"count(*)\" is returned twice"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN count(*) AS x, sum(n) AS x", "column name \"x\" is returned twice"},
		{"MATCH (o:Order) WITH o.region AS r RETURN sum(r)", "an aggregate after WITH"},
		{"MATCH (o:Order) WITH o.region AS r, collect(o.id) AS ids RETURN r, ids", "using a list a WITH collected"},
		{"MATCH (o:Order) WITH o.region AS r, collect(o.id) AS ids WHERE ids > 1 RETURN r", "using a list a WITH collected"},
		{"MATCH (o:Order) WITH o.region AS r, collect(o) AS os RETURN r", "this collect in WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE size(r) > 1 RETURN r", "this size()"},
		{"WITH ['a'] AS codes WITH codes MATCH (o:Order) RETURN o.id", "a WITH before any MATCH"},
		{"WITH 1 + 1 AS two MATCH (o:Order) WHERE o.id = two RETURN o.id", "open it with a WITH that only names values"},
		{"WITH ['a'] AS codes MATCH (o:Order) WHERE o.region = codes RETURN o.id", "comparing with a list"},
		{"WITH 'a' AS code MATCH (o:Order) WHERE o.region IN code RETURN o.id", "IN over a single value"},
		{"WITH null AS code MATCH (o:Order) WHERE o.region = code RETURN o.id", "naming null"},
		{"WITH ['a'] AS codes MATCH (o:Order) WITH o.region AS r WHERE r = 'a' RETURN codes", "not visible after the WITH"},
		{"MATCH (o:Order) WITH o.region AS r RETURN o.amount", "not visible after the WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE o.amount > 1 RETURN r", "not visible after the WITH"},
		{"MATCH (o:Order) WITH o RETURN o", "returning a whole node"},
		{"MATCH (o:Order) WITH x RETURN x.id", "not a node of the MATCH pattern"},
		{"MATCH (o:Order) WITH o.region AS r WHERE r IN ['east'] RETURN r", "IN, STARTS WITH"},
		{"MATCH (o:Order) WITH o.region AS r, o.amount + 1 AS a, count(*) AS n RETURN r", "grouping by a computed value"},
		{"MATCH (o:Order) WITH o.region AS r, o.region AS r RETURN r", "a name WITH uses twice"},
		{"MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WITH i.id AS item WHERE item > 0 RETURN item",
			"requiring what an OPTIONAL MATCH introduced"},
	} {
		_, err := compileWithClause(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

func TestWithDescriptor(t *testing.T) {
	query := "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n > 1 RETURN r, n"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	var having bool
	for _, predicate := range descriptor.Predicates {
		if strings.HasPrefix(predicate.LogicalPath, "having") && predicate.Operator == ">" {
			having = true
		}
	}
	if !having || len(descriptor.Grouping) != 1 {
		t.Fatalf("predicates = %+v, grouping = %v", descriptor.Predicates, descriptor.Grouping)
	}
}

// A condition returned as a value records the properties it reads, a property
// comparison included.
func TestReturnedPropertyComparisonDescriptor(t *testing.T) {
	query := "MATCH (o:Order) RETURN o.previous_id < o.id AS later"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	if refs := descriptor.Projections[0].PropertyRefs; len(refs) != 2 {
		t.Fatalf("property refs = %v, want both properties", refs)
	}
}

// Totals over a WITH's groups nest one aggregation in another, which only the
// SQL path writes; they go there whichever executor is configured, and the
// trace records the totals rather than the group columns behind them.
func TestWithTotalsRunOnTheSQLPathWhateverTheExecutor(t *testing.T) {
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{
		{"customers": json.Number("2"), "top": "c9"},
	}}}
	service := graphService(vega)

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) WITH o.amount AS a, max(o.customer_code) AS last, count(*) AS n WHERE n > 1 " +
			"RETURN count(*) AS customers, max(last) AS top",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil {
		t.Fatal("totals did not run on the SQL path")
	}
	want := "SELECT COUNT(*) AS `customers`, MAX(g.`last`) AS `top` FROM (SELECT MAX(t0.`f_cust_code`) AS `last` " +
		"FROM {{.res_order}} t0 GROUP BY t0.`f_total` HAVING COUNT(*) > 1) g LIMIT"
	if !strings.Contains(vega.request.Query, want) {
		t.Fatalf("statement = %s, want %s", vega.request.Query, want)
	}
	var descriptor SemanticQueryDescriptor
	if err := json.Unmarshal(result.TraceDescriptor, &descriptor); err != nil {
		t.Fatalf("trace descriptor: %v", err)
	}
	if len(descriptor.Projections) != 2 {
		t.Fatalf("projections = %+v, want the two totals", descriptor.Projections)
	}
	count, top := descriptor.Projections[0], descriptor.Projections[1]
	if count.Alias != "customers" || count.Aggregate != "count" || count.Expression != "groups" || len(count.PropertyRefs) != 0 {
		t.Errorf("count projection = %+v", count)
	}
	if top.Alias != "top" || top.Aggregate != "max" || top.Expression != "groups" ||
		strings.Join(top.PropertyRefs, ",") != "property:kn_1:ot_order:customer_code" {
		t.Errorf("max projection = %+v", top)
	}
	if strings.Join(descriptor.Grouping, ",") != "property:kn_1:ot_order:amount" || len(descriptor.Predicates) != 1 {
		t.Errorf("grouping = %v, predicates = %+v", descriptor.Grouping, descriptor.Predicates)
	}
}
