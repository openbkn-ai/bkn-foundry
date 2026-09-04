// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"
	"testing"
)

func analyze(t *testing.T, query string) (*Query, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	return Analyze(tree)
}

func mustAnalyze(t *testing.T, query string) *Query {
	t.Helper()
	analyzed, err := analyze(t, query)
	if err != nil {
		t.Fatalf("Analyze(%q): %v", query, err)
	}
	return analyzed
}

func TestAnalyzeSingleNode(t *testing.T) {
	query := mustAnalyze(t, "MATCH (a:Order) RETURN a.id")

	if got := len(query.Pattern.Nodes); got != 1 {
		t.Fatalf("nodes = %d, want 1", got)
	}
	if node := query.Pattern.Nodes[0]; node.Variable != "a" || node.Label != "Order" {
		t.Fatalf("node = %+v, want variable a label Order", node)
	}
	if len(query.Pattern.Edges) != 0 {
		t.Fatalf("edges = %d, want 0", len(query.Pattern.Edges))
	}
	// Without AS, the column keeps the source text of the reference, the way
	// Cypher names an unaliased projection.
	if item := query.Return[0]; item.Alias != "a.id" || item.Property.Property != "id" {
		t.Fatalf("projection = %+v, want alias a.id", item)
	}
}

func TestAnalyzeFullQuery(t *testing.T) {
	query := mustAnalyze(t, `MATCH (a:Order)-[:PLACED_BY]->(b:Customer)
		WHERE a.amount > 100 AND b.name = 'Acme'
		RETURN DISTINCT a.id AS order_id, b.name
		ORDER BY a.amount DESC, b.name
		SKIP 5 LIMIT 10`)

	if len(query.Pattern.Nodes) != 2 || len(query.Pattern.Edges) != 1 {
		t.Fatalf("pattern = %+v, want two nodes and one edge", query.Pattern)
	}
	if edge := query.Pattern.Edges[0]; edge.Type != "PLACED_BY" || edge.Direction != Outgoing {
		t.Fatalf("edge = %+v, want PLACED_BY outgoing", edge)
	}
	if !query.Distinct {
		t.Fatal("DISTINCT was dropped")
	}

	if len(query.Where) != 2 {
		t.Fatalf("predicates = %d, want 2", len(query.Where))
	}
	if p := query.Where[0]; p.Left.String() != "a.amount" || p.Operator != ">" || p.Right.Integer != 100 {
		t.Fatalf("first predicate = %+v", p)
	}
	if p := query.Where[1]; p.Left.String() != "b.name" || p.Operator != "=" || p.Right.String != "Acme" {
		t.Fatalf("second predicate = %+v", p)
	}

	if query.Return[0].Alias != "order_id" || query.Return[1].Alias != "b.name" {
		t.Fatalf("projections = %+v", query.Return)
	}
	if len(query.OrderBy) != 2 || !query.OrderBy[0].Descending || query.OrderBy[1].Descending {
		t.Fatalf("order by = %+v", query.OrderBy)
	}
	if query.Skip == nil || *query.Skip != 5 || query.Limit == nil || *query.Limit != 10 {
		t.Fatalf("skip = %v, limit = %v", query.Skip, query.Limit)
	}
}

func TestAnalyzeIncomingDirection(t *testing.T) {
	query := mustAnalyze(t, "MATCH (a:Customer)<-[:PLACED_BY]-(b:Order) RETURN a.id")
	if edge := query.Pattern.Edges[0]; edge.Direction != Incoming {
		t.Fatalf("direction = %v, want Incoming", edge.Direction)
	}
}

func TestAnalyzeAnonymousNode(t *testing.T) {
	query := mustAnalyze(t, "MATCH (a:Order)-[:PLACED_BY]->(:Customer) RETURN a.id")
	if variable := query.Pattern.Nodes[1].Variable; variable != "" {
		t.Fatalf("variable = %q, want empty", variable)
	}
}

func TestAnalyzeParenthesizedPattern(t *testing.T) {
	query := mustAnalyze(t, "MATCH ((a:Order)) RETURN a.id")
	if node := query.Pattern.Nodes[0]; node.Label != "Order" {
		t.Fatalf("node = %+v", node)
	}
}

// Backticked names are how a model whose names carry spaces or non-ASCII
// characters is written in Cypher, and those names are common here.
func TestAnalyzeEscapedNames(t *testing.T) {
	query := mustAnalyze(t, "MATCH (`the order`:`Order Type`) RETURN `the order`.`unit price`")
	if node := query.Pattern.Nodes[0]; node.Variable != "the order" || node.Label != "Order Type" {
		t.Fatalf("node = %+v", node)
	}
	if property := query.Return[0].Property.Property; property != "unit price" {
		t.Fatalf("property = %q", property)
	}
}

func TestAnalyzeLiterals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where string
		check func(*testing.T, Literal)
	}{
		{
			name:  "negative integer",
			where: "a.amount > -12",
			check: func(t *testing.T, l Literal) {
				if l.Kind != LiteralInteger || l.Integer != -12 {
					t.Fatalf("got %+v", l)
				}
			},
		},
		{
			name:  "float",
			where: "a.amount <= 1.5",
			check: func(t *testing.T, l Literal) {
				if l.Kind != LiteralFloat || l.Float != 1.5 {
					t.Fatalf("got %+v", l)
				}
			},
		},
		{
			name:  "hexadecimal",
			where: "a.amount = 0x1f",
			check: func(t *testing.T, l Literal) {
				if l.Kind != LiteralInteger || l.Integer != 31 {
					t.Fatalf("got %+v", l)
				}
			},
		},
		{
			name:  "boolean",
			where: "a.paid = true",
			check: func(t *testing.T, l Literal) {
				if l.Kind != LiteralBoolean || !l.Boolean {
					t.Fatalf("got %+v", l)
				}
			},
		},
		{
			name:  "escapes are decoded once, here",
			where: `a.name <> 'a\tbA\\c\''`,
			check: func(t *testing.T, l Literal) {
				if want := "a\tbA\\c'"; l.String != want {
					t.Fatalf("got %q, want %q", l.String, want)
				}
			},
		},
		{
			name:  "double quoted",
			where: `a.name = "quoted"`,
			check: func(t *testing.T, l Literal) {
				if l.String != "quoted" {
					t.Fatalf("got %q", l.String)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := mustAnalyze(t, "MATCH (a:Order) WHERE "+tc.where+" RETURN a.id")
			tc.check(t, query.Where[0].Right)
		})
	}
}

func TestAnalyzeComparisonOperators(t *testing.T) {
	for _, operator := range []string{"=", "<>", "<", ">", "<=", ">="} {
		query := mustAnalyze(t, "MATCH (a:Order) WHERE a.amount "+operator+" 1 RETURN a.id")
		if got := query.Where[0].Operator; got != operator {
			t.Fatalf("operator = %q, want %q", got, operator)
		}
	}
}

// Every rejection names the construct, so the author is told what to change
// rather than that the query failed.
func TestAnalyzeRejections(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"CREATE (a:Order) RETURN a.id", "read-only"},
		{"MATCH (a:Order) SET a.paid = true RETURN a.id", "read-only"},
		{"MATCH (a:Order) DETACH DELETE a RETURN a.id", "read-only"},
		{"MATCH (a:Order) RETURN a.id UNION MATCH (b:Order) RETURN b.id", "UNION"},
		{"MATCH (a:Order) WITH a RETURN a.id", "WITH"},
		{"OPTIONAL MATCH (a:Order) RETURN a.id", "OPTIONAL MATCH"},
		{"UNWIND [1, 2] AS x RETURN x", "UNWIND"},
		{"CALL db.labels() YIELD label RETURN label", "procedure calls"},
		{"MATCH (a:Order) MATCH (b:Order) RETURN a.id", "multiple reading clauses"},
		{"MATCH (a:Order), (b:Customer) RETURN a.id", "multiple pattern parts"},
		{"MATCH p = (a:Order) RETURN a.id", "path variables"},
		{"MATCH (a) RETURN a.id", "nodes without a label"},
		{"MATCH (a:Order:Invoice) RETURN a.id", "multiple labels"},
		{"MATCH (a:Order {id: 1}) RETURN a.id", "inline property maps"},
		{"MATCH (a:Order)-[:R]-(b:Customer) RETURN a.id", "undirected relationships"},
		{"MATCH (a:Order)<-[:R]->(b:Customer) RETURN a.id", "pointing both ways"},
		{"MATCH (a:Order)-->(b:Customer) RETURN a.id", "relationships without a type"},
		{"MATCH (a:Order)-[r:R]->(b:Customer) RETURN a.id", "relationship variables"},
		{"MATCH (a:Order)-[:R*1..3]->(b:Customer) RETURN a.id", "variable-length"},
		{"MATCH (a:Order)-[:R|:S]->(b:Customer) RETURN a.id", "alternative relationship types"},
		{"MATCH (a:Order)-[:R {x: 1}]->(b:Customer) RETURN a.id", "inline property maps"},
		{"MATCH (a:Order)-[:R]->(b:C)-[:S]->(c:D) RETURN a.id", "multi-hop"},
		{"MATCH (a:Order) RETURN *", "RETURN *"},
		{"MATCH (a:Order) RETURN a", "referring to a node as a value"},
		{"MATCH (a:Order) RETURN count(a.id)", "function calls"},
		{"MATCH (a:Order) RETURN count(*)", "count(*)"},
		{"MATCH (a:Order) RETURN a.id + 1", "arithmetic"},
		{"MATCH (a:Order) RETURN $parameter", "query parameters"},
		{"MATCH (a:Order) RETURN a.items[0]", "list indexing"},
		{"MATCH (a:Order) RETURN a.x.y", "nested property access"},
		{"MATCH (a:Order) RETURN 1", "only variable.property references"},
		{"MATCH (a:Order) WHERE a.x = 1 OR a.y = 2 RETURN a.id", "OR"},
		{"MATCH (a:Order) WHERE a.x = 1 XOR a.y = 2 RETURN a.id", "XOR"},
		{"MATCH (a:Order) WHERE NOT a.x = 1 RETURN a.id", "NOT"},
		{"MATCH (a:Order) WHERE a.x IN [1, 2] RETURN a.id", "IN"},
		{"MATCH (a:Order) WHERE a.x IS NULL RETURN a.id", "IS NULL"},
		{"MATCH (a:Order) WHERE a.x STARTS WITH 'A' RETURN a.id", "STARTS WITH"},
		{"MATCH (a:Order) WHERE a.x = a.y RETURN a.id", "against a literal"},
		{"MATCH (a:Order) WHERE a.x = null RETURN a.id", "comparing against null"},
		{"MATCH (a:Order) WHERE a.x < a.y < a.z RETURN a.id", "chained comparisons"},
		{"MATCH (a:Order) WHERE a.x RETURN a.id", "non-comparison predicate"},
		{"MATCH (a:Order) WHERE a.x = [1] RETURN a.id", "list and map literals"},
		{"MATCH (a:Order) WHERE (a)-[:R]->(:Customer) RETURN a.id", "pattern predicates"},
		{"MATCH (a:Order) WHERE EXISTS { (a)-[:R]->(:Customer) } RETURN a.id", "EXISTS subqueries"},
		{"MATCH (a:Order) WHERE (a.x = 1) RETURN a.id", "parenthesized expressions"},
		{"MATCH (a:Order) RETURN a.id LIMIT 1 + 1", "arithmetic"},
		{"MATCH (a:Order) RETURN a.id LIMIT 'ten'", "non-integer LIMIT"},
		{"MATCH (a:Order) RETURN a.id SKIP -1", "negative SKIP"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, err := analyze(t, tc.query)
			if err == nil {
				t.Fatalf("Analyze(%q) succeeded, want rejection mentioning %q", tc.query, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Analyze(%q) = %v, want rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// A rejection has to say where, because a long query has many places the same
// construct could appear.
func TestAnalyzeRejectionCarriesPosition(t *testing.T) {
	_, err := analyze(t, "MATCH (a:Order)\nWHERE NOT a.paid = true\nRETURN a.id")
	unsupported, ok := err.(*Unsupported)
	if !ok {
		t.Fatalf("error = %T (%v), want *Unsupported", err, err)
	}
	if unsupported.Pos.Line != 2 {
		t.Fatalf("line = %d, want 2", unsupported.Pos.Line)
	}
}
