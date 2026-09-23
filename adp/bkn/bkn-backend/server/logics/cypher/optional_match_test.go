// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"strings"
	"testing"

	"bkn-backend/interfaces"
)

// OPTIONAL MATCH is an outer join, and the whole point of it is which rows
// survive: a node that has no neighbour of the asked-for kind keeps its place
// and comes back with nothing attached. COUNT over a column of the missing
// side is then 0 rather than the row being gone, which is what makes an
// aggregate over an optional relationship a ranking instead of a filter.
func TestCompileOptionalMatchIsAnOuterJoin(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"RETURN o.id AS id, count(i.id) AS items ORDER BY items DESC, id")
	want := "SELECT t0.`f_id` AS `id`, COUNT(t2.`f_id`) AS `items` FROM {{.res_order}} t0 " +
		"JOIN {{.res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
		"LEFT JOIN {{.res_item}} t2 ON t0.`f_id` = t2.`f_order` " +
		"GROUP BY t0.`f_id` ORDER BY `items` DESC, `id`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// What the optional clause writes beside its relationship decides what the
// join matched, not which rows come back, so it goes into the ON clause. In
// WHERE it would turn the outer join back into an inner one.
func TestCompileOptionalMatchConditionsStayInTheJoin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{
			name: "a WHERE on the optional clause",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE i.id = 'x' " +
				"RETURN o.id AS id",
		},
		{
			name: "an inline property map on the optional node",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item {id: 'x'}) " +
				"RETURN o.id AS id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustCompile(t, tc.query)
			want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 " +
				"LEFT JOIN {{.res_item}} t1 ON t0.`f_id` = t1.`f_order` AND t1.`f_id` = 'x'"
			if got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
		})
	}
}

// A condition the query required is still required beside an optional one, and
// the two end up in different places.
func TestCompileOptionalMatchBesideARequiredWhere(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order) WHERE o.region = 'eu' "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"RETURN o.id AS id, count(i.id) AS items")
	want := "SELECT t0.`f_id` AS `id`, COUNT(t1.`f_id`) AS `items` FROM {{.res_order}} t0 " +
		"LEFT JOIN {{.res_item}} t1 ON t0.`f_id` = t1.`f_order` " +
		"WHERE t0.`f_region` = 'eu' GROUP BY t0.`f_id`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The key pairs of an undirected relationship are a disjunction, so anything
// ANDed onto them in the ON clause has to be parenthesised away from the OR.
func TestCompileOptionalMatchUndirected(t *testing.T) {
	got := mustCompile(t, "MATCH (a:Order) OPTIONAL MATCH (a)-[:FOLLOWS]-(b:Order) "+
		"WHERE b.region = 'eu' RETURN a.id AS id")
	want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 " +
		"LEFT JOIN {{.res_order}} t1 ON ((t0.`f_id` = t1.`f_prev`) OR (t0.`f_prev` = t1.`f_id`)) " +
		"AND t1.`f_region` = 'eu'"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An outer join cannot be reordered in among the inner ones: everything it
// hangs off has to be read before it. A required MATCH written after the
// optional clause is therefore still emitted before it, even though the table
// it adds came later in the pattern.
func TestCompileOptionalJoinsComeLast(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order) "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"MATCH (o)-[:PLACED_BY]->(c:Customer) "+
		"RETURN c.name AS customer, count(i.id) AS items")
	want := "SELECT t2.`f_name` AS `customer`, COUNT(t1.`f_id`) AS `items` FROM {{.res_order}} t0 " +
		"JOIN {{.res_customer}} t2 ON t0.`f_cust_code` = t2.`f_code` AND t0.`f_region` = t2.`f_region` " +
		"LEFT JOIN {{.res_item}} t1 ON t0.`f_id` = t1.`f_order` " +
		"GROUP BY t2.`f_name`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// One optional clause may hang off what another introduced. Each is its own
// all-or-nothing match, and chained outer joins say exactly that: when the
// first finds nothing the second has nothing to attach to and goes null too.
func TestCompileChainedOptionalMatches(t *testing.T) {
	got := mustCompile(t, "MATCH (c:Customer) "+
		"OPTIONAL MATCH (c)<-[:PLACED_BY]-(o:Order) "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"RETURN c.name AS customer, count(i.id) AS items")
	want := "SELECT t0.`f_name` AS `customer`, COUNT(t2.`f_id`) AS `items` FROM {{.res_customer}} t0 " +
		"LEFT JOIN {{.res_order}} t1 ON t0.`f_code` = t1.`f_cust_code` AND t0.`f_region` = t1.`f_region` " +
		"LEFT JOIN {{.res_item}} t2 ON t1.`f_id` = t2.`f_order` " +
		"GROUP BY t0.`f_name`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The rule that one MATCH may not traverse the same relationship twice is
// scoped to a clause, so an optional clause never pairs with the required one.
// A condition from that rule would sit in WHERE, where it would drop the rows
// the outer join kept.
func TestCompileOptionalMatchAddsNoDistinctnessCondition(t *testing.T) {
	got := mustCompile(t, "MATCH (a:Order)-[:FOLLOWS]->(b:Order) "+
		"OPTIONAL MATCH (b)-[:FOLLOWS]->(c:Order) RETURN a.id AS id")
	if strings.Contains(got, " WHERE ") {
		t.Fatalf("an optional clause produced a condition in WHERE: %s", got)
	}
	if !strings.Contains(got, "LEFT JOIN {{.res_order}} t2 ON t1.`f_id` = t2.`f_prev`") {
		t.Fatalf("got %s", got)
	}
}

func TestCompileOptionalMatchRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "a query that starts with it",
			query: "OPTIONAL MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id",
			want:  "a query that starts with OPTIONAL MATCH",
		},
		{
			name: "more than one relationship in it",
			query: "MATCH (i:Item) OPTIONAL MATCH (i)-[:BELONGS_TO]->(o:Order)-[:PLACED_BY]->(c:Customer) " +
				"RETURN i.id",
			want: "an OPTIONAL MATCH over anything but one relationship",
		},
		{
			name:  "no relationship in it",
			query: "MATCH (o:Order) OPTIONAL MATCH (c:Customer) RETURN o.id",
			want:  "an OPTIONAL MATCH over anything but one relationship",
		},
		{
			name: "both ends already matched",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) OPTIONAL MATCH (o)-[:PLACED_BY]->(c) " +
				"RETURN o.id",
			want: "an OPTIONAL MATCH that introduces no node",
		},
		{
			name:  "neither end already matched",
			query: "MATCH (o:Order) OPTIONAL MATCH (i:Item)-[:BELONGS_TO]->(o2:Order) RETURN o.id",
			want:  "an OPTIONAL MATCH that introduces more than one node",
		},
		{
			name: "a new node beside the relationship rather than on it",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) " +
				"OPTIONAL MATCH (o)-[:PLACED_BY]->(c), (i:Item) RETURN o.id",
			want: "an OPTIONAL MATCH whose new node is not on its relationship",
		},
		{
			// The shape that reads as a filter and is not one. Written after
			// the optional clause, the condition scopes to it, so it would
			// decide whether to look for an item rather than which orders come
			// back -- every order would, which is a wrong answer and a silent
			// one.
			name: "a condition that names nothing the clause introduces",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) " +
				"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE c.name = 'x' RETURN o.id",
			want: "a condition in an OPTIONAL MATCH that names nothing it introduces",
		},
		{
			name: "an inline map on the node it attaches to",
			query: "MATCH (o:Order) OPTIONAL MATCH (o {region: 'eu'})<-[:BELONGS_TO]-(i:Item) " +
				"RETURN o.id",
			want: "a condition in an OPTIONAL MATCH that names nothing it introduces",
		},
		{
			name: "a later MATCH walking through what it introduced",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
				"MATCH (i)-[:BELONGS_TO]->(o2:Order) RETURN o.id",
			want: "requiring what an OPTIONAL MATCH introduced",
		},
		{
			name: "the query's WHERE testing what it introduced",
			query: "MATCH (o:Order) WHERE i.id = 'x' OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
				"RETURN o.id",
			want: "requiring what an OPTIONAL MATCH introduced",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile(t, tc.query, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// A row filter on an optional table belongs in the join as well. In WHERE it
// would hide more than the rows it names: the outer join would collapse into an
// inner one, and a node whose only neighbours are invisible to this caller
// would vanish along with them instead of coming back with nothing attached.
func TestQueryRowFilterOnOptionalTableStaysInTheJoin(t *testing.T) {
	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"), dataProperty("region", "f_region"))
	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		dataProperty("id", "f_id"), dataProperty("region", "f_region"))
	relation := relationType("rt_belongs_to", "BELONGS_TO")
	relation.SourceObjectTypeID = order.OTID
	relation.TargetObjectTypeID = customer.OTID
	relation.MappingRules = []interfaces.Mapping{{
		SourceProp: interfaces.SimpleProperty{Name: "id"},
		TargetProp: interfaces.SimpleProperty{Name: "id"},
	}}

	east, cn := "east", "cn"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order": {
			Kind: "in", Property: "region",
			Values: []interfaces.RowFilterValue{{Type: "string", String: &east}},
		},
		"kn_1/ot_customer": {
			Kind: "in", Property: "region",
			Values: []interfaces.RowFilterValue{{Type: "string", String: &cn}},
		},
	}}
	vega := &recordingVega{}
	service := &cypherQueryService{
		ps: permission,
		schema: &fakeSchemaSource{
			objectTypes:   []*interfaces.ObjectType{order, customer},
			relationTypes: []*interfaces.RelationType{relation},
		},
		vba: vega,
	}

	if _, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main",
		Query: "MATCH (o:Order) OPTIONAL MATCH (o)-[:BELONGS_TO]->(c:Customer) " +
			"RETURN o.id AS id, count(c.id) AS customers",
	}); err != nil {
		t.Fatalf("Query: %v", err)
	}

	want := "SELECT t0.`f_id` AS `id`, COUNT(t1.`f_id`) AS `customers` FROM {{.res_order}} t0 " +
		"LEFT JOIN {{.res_customer}} t1 ON t0.`f_id` = t1.`f_id` AND t1.`f_region` IN ('cn') " +
		"WHERE t0.`f_region` IN ('east') GROUP BY t0.`f_id` LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}

// The descriptor is what a reader of the audit trail has instead of the SQL,
// so it has to say which relationship was optional and which conditions
// qualified it rather than the result.
func TestSemanticQueryDescriptorRecordsOptionalMatch(t *testing.T) {
	tree, err := Parse("MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
		"WHERE i.id = 'x' RETURN o.id AS id, count(i.id) AS items")
	if err != nil {
		t.Fatal(err)
	}
	query, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(query, modelSchema(t), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, tree.GetText())
	if err != nil {
		t.Fatal(err)
	}

	if len(descriptor.Relations) != 1 || !descriptor.Relations[0].Optional {
		t.Fatalf("relations = %+v, want one marked optional", descriptor.Relations)
	}
	if len(descriptor.Predicates) != 1 {
		t.Fatalf("predicates = %+v, want the condition the join carries", descriptor.Predicates)
	}
	predicate := descriptor.Predicates[0]
	if predicate.PropertyRef != "property:kn_test:ot_item:id" || predicate.Operator != "=" ||
		predicate.LogicalPath != "OPTIONAL[0].AND[0]" {
		t.Fatalf("predicate = %+v", predicate)
	}
}

// A query without an outer join writes the same bytes it wrote before there
// were any, so the evidence hashes of everything already recorded still match.
func TestSemanticQueryDescriptorOmitsOptionalWhenUnset(t *testing.T) {
	tree, err := Parse("MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id")
	if err != nil {
		t.Fatal(err)
	}
	query, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(query, modelSchema(t), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, tree.GetText())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "optional") {
		t.Fatalf("descriptor carries an optional marker it did not need: %s", raw)
	}
}

// The query the graph explorer needs: rank a node's neighbours by how many
// edges of another kind they carry, page through the ranking, and keep the
// neighbours that carry none. Written with the filter on the MATCH it filters,
// which is where it belongs and where the rejection above points.
func TestCompileNeighbourDegreeRanking(t *testing.T) {
	got, err := compileWith(t, "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE c.name = $customer "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"RETURN o.id AS id, count(DISTINCT i.id) AS items "+
		"ORDER BY items DESC, id SKIP 20 LIMIT 10", map[string]any{"customer": "name"})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT t0.`f_id` AS `id`, COUNT(DISTINCT t2.`f_id`) AS `items` FROM {{.res_order}} t0 " +
		"JOIN {{.res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
		"LEFT JOIN {{.res_item}} t2 ON t0.`f_id` = t2.`f_order` " +
		"WHERE t1.`f_name` = 'name' GROUP BY t0.`f_id` " +
		"ORDER BY `items` DESC, `id` LIMIT 10 OFFSET 20"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Degrees over several relation types come back in one statement rather than
// one per type. The optional sides multiply each other's rows, so each count
// has to be DISTINCT to mean what it says -- which is why the count is written
// over a property and not over rows.
func TestCompileDegreesOverSeveralRelationTypes(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order) "+
		"OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) "+
		"OPTIONAL MATCH (o)-[:PLACED_BY]->(c:Customer) "+
		"RETURN o.id AS id, count(DISTINCT i.id) AS items, count(DISTINCT c.name) AS customers")
	want := "SELECT t0.`f_id` AS `id`, COUNT(DISTINCT t1.`f_id`) AS `items`, " +
		"COUNT(DISTINCT t2.`f_name`) AS `customers` FROM {{.res_order}} t0 " +
		"LEFT JOIN {{.res_item}} t1 ON t0.`f_id` = t1.`f_order` " +
		"LEFT JOIN {{.res_customer}} t2 ON t0.`f_cust_code` = t2.`f_code` AND t0.`f_region` = t2.`f_region` " +
		"GROUP BY t0.`f_id`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// compileWith is compile with parameters, which the shared helper does not
// take.
func compileWith(t *testing.T, query string, parameters map[string]any) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		return "", err
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, modelSchema(t), CompileOptions{Parameters: parameters})
	if err != nil {
		return "", err
	}
	return Generate(plan, GenerateOptions{})
}
