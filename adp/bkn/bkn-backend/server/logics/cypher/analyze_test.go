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

	conjunction, ok := query.Where.(LogicalOperator)
	if !ok || conjunction.Operator != "AND" || len(conjunction.Operands) != 2 {
		t.Fatalf("where = %+v, want an AND of two comparisons", query.Where)
	}
	if p := conjunction.Operands[0].(Comparison); p.Left.String() != "a.amount" ||
		p.Operator != ">" || p.Right.Literal.Integer != 100 {
		t.Fatalf("first predicate = %+v", p)
	}
	if p := conjunction.Operands[1].(Comparison); p.Left.String() != "b.name" ||
		p.Operator != "=" || p.Right.Literal.String != "Acme" {
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
			name:  "eight-digit unicode escape",
			where: `a.name = '\U0001F600'`,
			check: func(t *testing.T, l Literal) {
				if l.String != "\U0001F600" {
					t.Fatalf("got %q", l.String)
				}
			},
		},
		{
			name:  "four-digit unicode escape",
			where: `a.name = '\u0041b'`,
			check: func(t *testing.T, l Literal) {
				// Four hex digits then a non-hex character: the short form,
				// with the character after it kept as itself.
				if l.String != "Ab" {
					t.Fatalf("got %q", l.String)
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
			tc.check(t, *query.Where.(Comparison).Right.Literal)
		})
	}
}

func TestAnalyzeComparisonOperators(t *testing.T) {
	for _, operator := range []string{"=", "<>", "<", ">", "<=", ">="} {
		query := mustAnalyze(t, "MATCH (a:Order) WHERE a.amount "+operator+" 1 RETURN a.id")
		if got := query.Where.(Comparison).Operator; got != operator {
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
		{"MATCH (a:Order) RETURN lower(a.id)", "function calls"},
		{"MATCH (a:Order) RETURN a.id + 1", "arithmetic"},
		{"MATCH (a:Order) RETURN CASE a.x WHEN 1 THEN 2 ELSE 3 END", "CASE"},
		{"MATCH (a:Order) RETURN [x IN [1, 2] | x]", "list comprehensions"},
		{"MATCH (a:Order) RETURN [(a)-[:R]->(b:Customer) | b.id]", "pattern comprehensions"},
		{"MATCH (a:Order) WHERE all(x IN [1] WHERE x = 1) RETURN a.id", "quantified expressions"},
		{"MATCH (a:Order) RETURN a.x IS NULL", "a condition here"},
		{"MATCH (a:Order) RETURN a.items[0]", "list indexing"},
		{"MATCH (a:Order) RETURN a.x.y", "nested property access"},
		{"MATCH (a:Order) RETURN 1", "only variable.property references"},
		{"MATCH (a:Order) WHERE a.x = 1 XOR a.y = 2 RETURN a.id", "XOR"},
		{"MATCH (a:Order) WHERE a.x STARTS WITH 'A' RETURN a.id", "STARTS WITH"},
		{"MATCH (a:Order) WHERE a.x = a.y RETURN a.id", "against a literal"},
		{"MATCH (a:Order) WHERE a.x = null RETURN a.id", "comparing against null"},
		{"MATCH (a:Order) WHERE a.x = 1 XOR a.y = 2 RETURN a.id", "XOR"},
		{"MATCH (a:Order) WHERE a.x < a.y < a.z RETURN a.id", "chained comparisons"},
		{"MATCH (a:Order) WHERE a.x RETURN a.id", "non-comparison predicate"},
		{"MATCH (a:Order) WHERE a.x = [1] RETURN a.id", "list and map literals"},
		{"MATCH (a:Order) WHERE (a)-[:R]->(:Customer) RETURN a.id", "pattern predicates"},
		{"MATCH (a:Order) WHERE EXISTS { (a)-[:R]->(:Customer) } RETURN a.id", "EXISTS subqueries"},
		{"MATCH (a:Order) RETURN (a.x)", "parenthesized expressions"},
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

// An escape past the last code point used to wrap into a negative rune and
// encode as a replacement character, which would have made the query mean
// something other than what was written.
func TestAnalyzeRejectsUnicodeEscapesThatAreNotCodePoints(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "beyond the last code point",
			query: `MATCH (a:Order) WHERE a.name = '\uFFFFFFFF' RETURN a.id`,
			want:  "beyond the last code point",
		},
		{
			name:  "unpaired surrogate",
			query: `MATCH (a:Order) WHERE a.name = '\uD800' RETURN a.id`,
			want:  "unpaired surrogate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := analyze(t, tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Analyze(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// Every escape the grammar allows is decoded here, and the two malformed
// forms are refused rather than passed through as text.
func TestAnalyzeStringEscapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where string
		want  string
	}{
		{name: "backspace", where: `a.name = '\b'`, want: "\b"},
		{name: "form feed", where: `a.name = '\f'`, want: "\f"},
		{name: "carriage return", where: `a.name = '\r'`, want: "\r"},
		{name: "newline", where: `a.name = '\n'`, want: "\n"},
		{name: "double quote", where: `a.name = '\"'`, want: `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := mustAnalyze(t, "MATCH (a:Order) WHERE "+tc.where+" RETURN a.id")
			if got := query.Where.(Comparison).Right.Literal.String; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The lexer refuses a malformed escape before the decoder ever sees it, so
// these branches are a second line rather than the first. They are exercised
// directly: reaching them through a query is not possible, and a test that
// pretended otherwise would be asserting the lexer instead.
func TestDecodeStringLiteralRefusesMalformedEscapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		literal string
		want    string
	}{
		{name: "unknown escape", literal: `'\q'`, want: "unknown escape sequence"},
		{name: "trailing backslash", literal: "'a\\'", want: "trailing backslash"},
		{name: "incomplete unicode escape", literal: `'\u41'`, want: "incomplete unicode escape"},
		{name: "invalid unicode digits", literal: `'\uzzzz'`, want: "invalid unicode escape"},
		{name: "not a literal at all", literal: `'`, want: "malformed string literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeStringLiteral(tc.literal)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeStringLiteral(%s) = %v, want an error mentioning %q", tc.literal, err, tc.want)
			}
		})
	}
}

// A rejection has to say where, because a long query has many places the same
// construct could appear.
func TestAnalyzeRejectionCarriesPosition(t *testing.T) {
	_, err := analyze(t, "MATCH (a:Order)\nWHERE a.name STARTS WITH 'A'\nRETURN a.id")
	unsupported, ok := err.(*Unsupported)
	if !ok {
		t.Fatalf("error = %T (%v), want *Unsupported", err, err)
	}
	if unsupported.Pos.Line != 2 {
		t.Fatalf("line = %d, want 2", unsupported.Pos.Line)
	}
}
