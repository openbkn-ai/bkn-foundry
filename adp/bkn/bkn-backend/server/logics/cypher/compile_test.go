// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strings"
	"testing"

	"bkn-backend/interfaces"
)

// A small model that carries the cases the compiler has to get right: columns
// renamed away from the property name, a direct relation with two key pairs,
// and a filtered cross join, which has no keys to join on at all.
func modelSchema(t *testing.T) *Schema {
	t.Helper()

	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"),
		dataProperty("previous_id", "f_prev"),
		dataProperty("customer_code", "f_cust_code"),
		dataProperty("region", "f_region"),
		dataProperty("amount", "f_total"),
	)
	order.PrimaryKeys = []string{"id"}

	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		dataProperty("code", "f_code"),
		dataProperty("region", "f_region"),
		dataProperty("name", "f_name"),
	)

	placedBy := relationType("rt_placed_by", "PLACED_BY")
	placedBy.SourceObjectTypeID = "ot_order"
	placedBy.TargetObjectTypeID = "ot_customer"
	// Two key pairs, so the generator has to emit a compound ON clause.
	placedBy.MappingRules = []interfaces.Mapping{
		{
			SourceProp: interfaces.SimpleProperty{Name: "customer_code"},
			TargetProp: interfaces.SimpleProperty{Name: "code"},
		},
		{
			SourceProp: interfaces.SimpleProperty{Name: "region"},
			TargetProp: interfaces.SimpleProperty{Name: "region"},
		},
	}

	item := objectType("ot_item", "Item", resource("res_item", "items"),
		dataProperty("id", "f_id"),
		dataProperty("order_id", "f_order"),
	)
	item.PrimaryKeys = []string{"id"}

	belongsTo := relationType("rt_belongs_to", "BELONGS_TO")
	belongsTo.SourceObjectTypeID = "ot_item"
	belongsTo.TargetObjectTypeID = "ot_order"
	belongsTo.MappingRules = []interfaces.Mapping{{
		SourceProp: interfaces.SimpleProperty{Name: "order_id"},
		TargetProp: interfaces.SimpleProperty{Name: "id"},
	}}

	// A relation from an object type to itself: an undirected pattern over it
	// cannot be settled by the object types, so both readings stay open.
	follows := relationType("rt_follows", "FOLLOWS")
	follows.SourceObjectTypeID = "ot_order"
	follows.TargetObjectTypeID = "ot_order"
	follows.MappingRules = []interfaces.Mapping{{
		SourceProp: interfaces.SimpleProperty{Name: "id"},
		TargetProp: interfaces.SimpleProperty{Name: "previous_id"},
	}}

	nearby := relationType("rt_nearby", "NEARBY")
	nearby.Type = interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN
	nearby.SourceObjectTypeID = "ot_order"
	nearby.TargetObjectTypeID = "ot_customer"
	nearby.MappingRules = &interfaces.FilteredCrossJoinMapping{}

	unmapped := relationType("rt_unmapped", "UNMAPPED")
	unmapped.SourceObjectTypeID = "ot_order"
	unmapped.TargetObjectTypeID = "ot_customer"

	return testSchema(t, &fakeSchemaSource{
		objectTypes:   []*interfaces.ObjectType{order, customer, item},
		relationTypes: []*interfaces.RelationType{placedBy, belongsTo, follows, nearby, unmapped},
	})
}

func compile(t *testing.T, query string, options GenerateOptions) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, modelSchema(t), CompileOptions{})
	if err != nil {
		return "", err
	}
	return Generate(plan, options)
}

func mustCompile(t *testing.T, query string) string {
	t.Helper()
	sql, err := compile(t, query, GenerateOptions{})
	if err != nil {
		t.Fatalf("compile(%q): %v", query, err)
	}
	return sql
}

func TestCompileSingleNode(t *testing.T) {
	// Properties resolve to their mapped columns, and the resource appears as
	// a placeholder rather than a table name.
	got := mustCompile(t, "MATCH (o:Order) RETURN o.id AS id, o.amount")
	want := "SELECT t0.`f_id` AS `id`, t0.`f_total` AS `o.amount` FROM {{.res_order}} t0"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCompileJoin(t *testing.T) {
	got := mustCompile(t, `MATCH (o:Order)-[:PLACED_BY]->(c:Customer)
		WHERE o.amount > 100
		RETURN o.id AS id, c.name AS customer`)
	want := "SELECT t0.`f_id` AS `id`, t1.`f_name` AS `customer` " +
		"FROM {{.res_order}} t0 " +
		"JOIN {{.res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
		"WHERE t0.`f_total` > 100"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Reading the same relation backwards must join the same columns, not mirror
// them: the relation's source stays the order side wherever it is written.
func TestCompileJoinIncoming(t *testing.T) {
	got := mustCompile(t, "MATCH (c:Customer)<-[:PLACED_BY]-(o:Order) RETURN o.id AS id")
	want := "SELECT t1.`f_id` AS `id` " +
		"FROM {{.res_customer}} t0 " +
		"JOIN {{.res_order}} t1 ON t0.`f_code` = t1.`f_cust_code` AND t0.`f_region` = t1.`f_region`"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Sorting by a returned value stays allowed under DISTINCT, whether it is
// named by its alias in RETURN or written out again in ORDER BY.
func TestCompileDistinctSortedByReturnedValue(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order) RETURN DISTINCT o.region AS r ORDER BY o.region DESC")
	want := "SELECT DISTINCT t0.`f_region` AS `r` FROM {{.res_order}} t0 ORDER BY t0.`f_region` DESC"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Without DISTINCT, sorting by a column that is not returned is ordinary SQL
// and stays allowed.
func TestCompileClauses(t *testing.T) {
	got := mustCompile(t, `MATCH (o:Order) RETURN o.region AS region
		ORDER BY o.region DESC, o.amount SKIP 20 LIMIT 10`)
	want := "SELECT t0.`f_region` AS `region` FROM {{.res_order}} t0 " +
		"ORDER BY t0.`f_region` DESC, t0.`f_total` LIMIT 10 OFFSET 20"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An unbounded result would stream through the whole path, so a query that
// does not ask for a limit gets one.
func TestCompileDefaultLimit(t *testing.T) {
	got, err := compile(t, "MATCH (o:Order) RETURN o.id", GenerateOptions{DefaultLimit: 500})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.HasSuffix(got, " LIMIT 500") {
		t.Fatalf("got %s, want a default limit", got)
	}

	// An explicit limit is the author's, and is left alone.
	got, err = compile(t, "MATCH (o:Order) RETURN o.id LIMIT 5", GenerateOptions{DefaultLimit: 500})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.HasSuffix(got, " LIMIT 5") {
		t.Fatalf("got %s, want the query's own limit", got)
	}
}

// Literals are the only caller-controlled text that reaches the statement.
// These are the payloads that were round-tripped against a live MariaDB.
func TestCompileLiteralEscaping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "quote",
			query: `MATCH (c:Customer) WHERE c.name = "O'Brien" RETURN c.code`,
			want:  "WHERE t0.`f_name` = 'O''Brien'",
		},
		{
			name:  "statement terminator and comment",
			query: `MATCH (c:Customer) WHERE c.name = "a'; DROP TABLE x --" RETURN c.code`,
			want:  "WHERE t0.`f_name` = 'a''; DROP TABLE x --'",
		},
		{
			name:  "trailing backslash",
			query: `MATCH (c:Customer) WHERE c.name = 'abc\\' RETURN c.code`,
			want:  "WHERE t0.`f_name` = 'abc\\\\'",
		},
		{
			name:  "escaped quote injection",
			query: `MATCH (c:Customer) WHERE c.name = '\\\' OR 1=1 --' RETURN c.code`,
			want:  "WHERE t0.`f_name` = '\\\\'' OR 1=1 --'",
		},
		{
			name:  "backtick",
			query: "MATCH (c:Customer) WHERE c.name = 'a`b' RETURN c.code",
			want:  "WHERE t0.`f_name` = 'a`b'",
		},
		{
			name:  "float",
			query: "MATCH (o:Order) WHERE o.amount <= 1.5 RETURN o.id",
			want:  "WHERE t0.`f_total` <= 1.5",
		},
		{
			name:  "negative integer",
			query: "MATCH (o:Order) WHERE o.amount <> -3 RETURN o.id",
			want:  "WHERE t0.`f_total` <> -3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustCompile(t, tc.query)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got  %s\nwant it to contain %s", got, tc.want)
			}
		})
	}
}

// A quote character inside an alias must not end the quoted name.
func TestCompileIdentifierQuoting(t *testing.T) {
	got := mustCompile(t, "MATCH (o:Order) RETURN o.id AS `a`` b`")
	if want := "AS `a`` b`"; !strings.Contains(got, want) {
		t.Fatalf("got %s, want it to contain %s", got, want)
	}
}

func TestGeneratePostgres(t *testing.T) {
	tree, err := Parse(`MATCH (c:Customer) WHERE c.name = 'a\\b\'c' RETURN c.name AS n`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	plan, err := Compile(analyzed, modelSchema(t), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := Generate(plan, GenerateOptions{Dialect: DialectPostgres})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Identifiers switch to double quotes, and a backslash stays one
	// backslash because it is not an escape character here.
	want := `SELECT t0."f_name" AS "n" FROM {{.res_customer}} t0 WHERE t0."f_name" = 'a\b''c'`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCompileRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "unknown label",
			query: "MATCH (i:Invoice) RETURN i.id",
			want:  `unknown label "Invoice"`,
		},
		{
			name:  "unknown property",
			query: "MATCH (o:Order) RETURN o.missing",
			want:  `has no property "missing"`,
		},
		{
			name:  "variable not in the pattern",
			query: "MATCH (o:Order) RETURN x.id",
			want:  `variable "x" is not defined`,
		},
		{
			name:  "variable reused",
			query: "MATCH (o:Order)-[:PLACED_BY]->(o:Customer) RETURN o.id",
			want:  "already bound",
		},
		{
			name:  "duplicate output column",
			query: "MATCH (o:Order) RETURN o.id AS x, o.amount AS x",
			want:  "returned twice",
		},
		{
			name:  "relationship written backwards",
			query: "MATCH (c:Customer)-[:PLACED_BY]->(o:Order) RETURN o.id",
			want:  "does not connect",
		},
		{
			name:  "filtered cross join",
			query: "MATCH (o:Order)-[:NEARBY]->(c:Customer) RETURN o.id",
			want:  "only direct relations",
		},
		{
			name:  "relation without key mapping",
			query: "MATCH (o:Order)-[:UNMAPPED]->(c:Customer) RETURN o.id",
			want:  "no key mapping",
		},
		{
			name:  "distinct sorted by something it does not return",
			query: "MATCH (o:Order) RETURN DISTINCT o.region AS r ORDER BY o.amount",
			want:  "DISTINCT can only be sorted by a returned value",
		},
		{
			name:  "unknown relation type",
			query: "MATCH (o:Order)-[:SHIPPED]->(c:Customer) RETURN o.id",
			want:  "unknown relationship type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile(t, tc.query, GenerateOptions{})
			if err == nil {
				t.Fatalf("compile(%q) succeeded, want a rejection mentioning %q", tc.query, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// A rejection from the planner has to point at the query too, not just say
// what the model does not have.
func TestCompileRejectionCarriesPosition(t *testing.T) {
	_, err := compile(t, "MATCH (o:Order)\nRETURN o.missing", GenerateOptions{})
	planError, ok := err.(*PlanError)
	if !ok {
		t.Fatalf("error = %T (%v), want *PlanError", err, err)
	}
	if planError.Pos.Line != 2 {
		t.Fatalf("line = %d, want 2", planError.Pos.Line)
	}
}

// An undirected relationship leaves open which node is the relation's source.
// Where the object types settle it, the join is the one reading that fits;
// where they do not, it is either.
func TestCompileUndirected(t *testing.T) {
	t.Run("object types settle the direction", func(t *testing.T) {
		got := mustCompile(t, "MATCH (o:Order)-[:PLACED_BY]-(c:Customer) RETURN o.id AS id")
		want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 " +
			"JOIN {{.res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code` AND t0.`f_region` = t1.`f_region`"
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("written the other way round is the same join", func(t *testing.T) {
		got := mustCompile(t, "MATCH (c:Customer)-[:PLACED_BY]-(o:Order) RETURN o.id AS id")
		want := "SELECT t1.`f_id` AS `id` FROM {{.res_customer}} t0 " +
			"JOIN {{.res_order}} t1 ON t0.`f_code` = t1.`f_cust_code` AND t0.`f_region` = t1.`f_region`"
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("a self relation matches either reading", func(t *testing.T) {
		got := mustCompile(t, "MATCH (a:Order)-[:FOLLOWS]-(b:Order) RETURN a.id AS id")
		want := "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 " +
			"JOIN {{.res_order}} t1 ON (t0.`f_id` = t1.`f_prev`) OR (t0.`f_prev` = t1.`f_id`)"
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})
}

// A path of several hops becomes a chain of joins in pattern order. The
// interesting part is not the chain but what Cypher requires on top of it: one
// pattern may not traverse the same relationship twice.
func TestCompileMultiHop(t *testing.T) {
	t.Run("two hops over different relations", func(t *testing.T) {
		got := mustCompile(t,
			"MATCH (i:Item)-[:BELONGS_TO]->(o:Order)-[:PLACED_BY]->(c:Customer) RETURN c.name AS customer")
		want := "SELECT t2.`f_name` AS `customer` FROM {{.res_item}} t0 " +
			"JOIN {{.res_order}} t1 ON t0.`f_order` = t1.`f_id` " +
			"JOIN {{.res_customer}} t2 ON t1.`f_cust_code` = t2.`f_code` AND t1.`f_region` = t2.`f_region`"
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("hops meeting a node from the same side cannot be the same edge", func(t *testing.T) {
		// Both hops arrive at the middle order, so the two outer items would
		// otherwise be free to be the same row -- which is the same
		// relationship walked out and back.
		got := mustCompile(t,
			"MATCH (a:Item)-[:BELONGS_TO]->(o:Order)<-[:BELONGS_TO]-(b:Item) RETURN a.id AS a, b.id AS b")
		want := "SELECT t0.`f_id` AS `a`, t2.`f_id` AS `b` FROM {{.res_item}} t0 " +
			"JOIN {{.res_order}} t1 ON t0.`f_order` = t1.`f_id` " +
			"JOIN {{.res_item}} t2 ON t1.`f_id` = t2.`f_order` " +
			"WHERE NOT t0.`f_id` = t2.`f_id`"
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	// Two hops in the same direction coincide on a row that points at itself,
	// so they need the condition as much as opposing hops do.
	t.Run("hops going the same way over a self relation", func(t *testing.T) {
		got := mustCompile(t,
			"MATCH (a:Order)-[:FOLLOWS]->(b:Order)-[:FOLLOWS]->(c:Order) RETURN a.id AS id")
		want := "WHERE NOT (t0.`f_id` = t1.`f_id` AND t1.`f_id` = t2.`f_id`)"
		if !strings.Contains(got, want) {
			t.Fatalf("got  %s\nwant it to contain %s", got, want)
		}
	})

	// Hops with another hop between them can be the same relationship too, so
	// every pair is compared rather than only neighbours.
	t.Run("hops that are not neighbours", func(t *testing.T) {
		got := mustCompile(t,
			"MATCH (a:Order)-[:FOLLOWS]->(b:Order)-[:FOLLOWS]->(c:Order)-[:FOLLOWS]->(d:Order) RETURN a.id AS id")
		for _, want := range []string{
			"NOT (t0.`f_id` = t1.`f_id` AND t1.`f_id` = t2.`f_id`)",
			"NOT (t0.`f_id` = t2.`f_id` AND t1.`f_id` = t3.`f_id`)",
			"NOT (t1.`f_id` = t2.`f_id` AND t2.`f_id` = t3.`f_id`)",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("got  %s\nwant it to contain %s", got, want)
			}
		}
	})

	t.Run("a distinctness condition joins the query's own", func(t *testing.T) {
		got := mustCompile(t,
			"MATCH (a:Item)-[:BELONGS_TO]->(o:Order)<-[:BELONGS_TO]-(b:Item) WHERE o.amount > 10 RETURN a.id AS a")
		if !strings.Contains(got, "WHERE NOT t0.`f_id` = t2.`f_id` AND t1.`f_total` > 10") {
			t.Fatalf("got %s", got)
		}
	})
}

func TestCompileMultiHopRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "an undirected hop beside one of the same type",
			query: "MATCH (a:Item)-[:BELONGS_TO]-(o:Order)-[:BELONGS_TO]-(b:Item) RETURN a.id",
			want:  "cannot have one of them undirected",
		},
		{
			name:  "a middle node the relations do not connect",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order)-[:BELONGS_TO]->(c:Customer) RETURN i.id",
			want:  "does not connect",
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

// Every relationship is a join, and the row limit bounds what comes back
// rather than what the database does to produce it.
func TestCompileRefusesAVeryLongPath(t *testing.T) {
	query := "MATCH (n0:Order)"
	for i := 1; i <= interfaces.CYPHER_MAX_PATH_LENGTH+1; i++ {
		query += fmt.Sprintf("-[:FOLLOWS]->(n%d:Order)", i)
	}
	query += " RETURN n0.id"

	_, err := compile(t, query, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "a path this long") {
		t.Fatalf("compile = %v, want a rejection naming the path length", err)
	}
}
