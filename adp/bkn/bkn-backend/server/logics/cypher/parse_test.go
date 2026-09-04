// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"errors"
	"testing"
)

// The grammar deliberately accepts more than the subset. These cases pin that
// split down: everything the subset will later reject must still parse here,
// so the rejection can come from the tree walk with a reason attached.
func TestParseAcceptsWholeLanguage(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"phase 1 shape", "MATCH (o:销售订单)-[:明细属于订单]-(i:订单明细) WHERE o.actual_amount > 1000 RETURN o.order_no, i.product_name ORDER BY o.order_no LIMIT 10"},
		{"chinese label and relation type", "MATCH (o:销售订单) RETURN o.order_no"},
		{"identifier label", "MATCH (o:order) RETURN o.order_no"},
		{"parameter", "MATCH (u:User {name: $nm}) RETURN u.id"},
		{"variable length", "MATCH (a:A)-[:R*1..3]->(b:B) RETURN b.y"},
		{"negation and list", "MATCH (a:A) WHERE NOT a.x IN [1, 2] RETURN DISTINCT a.y SKIP 5 LIMIT 20"},
		{"pattern predicate", "MATCH (a:A)-[:R]->(b:B) WHERE (a)-[:S]->(b) RETURN a.x"},
		{"backtick identifier", "MATCH (a:`odd label`) RETURN a.`odd prop`"},
		{"escaped string literal", `MATCH (a:A) WHERE a.n = 'O\'Brien' RETURN a.n`},
		// Outside the subset, inside the grammar.
		{"write clause", "CREATE (n:X) RETURN n"},
		{"with", "MATCH (a:A) WITH a RETURN a"},
		{"union", "MATCH (a:A) RETURN a.x UNION MATCH (b:B) RETURN b.x"},
		{"aggregate", "MATCH (a:A) RETURN count(a)"},
		{"edge function", "MATCH (a)-[r]->(b) RETURN type(r)"},
		{"multi relation type", "MATCH (a)-[:R|S]->(b) RETURN a"},
		{"optional match", "OPTIONAL MATCH (a:A) RETURN a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("expected the grammar to accept this, got: %v", err)
			}
			if tree == nil {
				t.Fatal("parse returned no tree and no error")
			}
		})
	}
}

func TestParseReportsEverySyntaxError(t *testing.T) {
	for _, query := range []string{
		"THIS IS NOT CYPHER",
		"MATCH (a:A RETURN a",
		"",
	} {
		_, err := Parse(query)
		if err == nil {
			t.Fatalf("expected a syntax error for %q", query)
		}
		var errs SyntaxErrors
		if !errors.As(err, &errs) {
			t.Fatalf("expected SyntaxErrors, got %T", err)
		}
		if len(errs) == 0 {
			t.Fatal("SyntaxErrors carried no positions")
		}
		if errs[0].Msg == "" {
			t.Fatal("syntax error carried no message")
		}
	}
}
