// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A node counted with DISTINCT is told apart by all of its primary key
// columns, in the form each dialect counts a list of columns.
func TestCountDistinctNodeWithAKeyOfSeveralColumns(t *testing.T) {
	compile := func(query string) *Plan {
		t.Helper()
		tree, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		analyzed, err := Analyze(tree)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		return plan
	}
	plan := compile("MATCH (c:Customer) RETURN count(DISTINCT c) AS n")
	for dialect, want := range map[Dialect]string{
		DialectMySQL:    "COUNT(DISTINCT t0.`f_code`, t0.`f_region`)",
		DialectPostgres: `COUNT(DISTINCT (t0."f_code", t0."f_region"))`,
	} {
		statement, err := Generate(plan, GenerateOptions{Dialect: dialect})
		if err != nil {
			t.Fatalf("%s: %v", dialect, err)
		}
		if !strings.Contains(statement, want) {
			t.Errorf("%s: statement = %s, want %s", dialect, statement, want)
		}
	}
	// Both key properties are read, so both are authorized.
	read := map[string]bool{}
	for _, property := range plan.Properties {
		read[property.Property] = true
	}
	if !read["code"] || !read["region"] {
		t.Errorf("properties = %v, want code and region", plan.Properties)
	}
	// Without DISTINCT a node is there when its first key is.
	if statement, _ := Generate(compile("MATCH (c:Customer) RETURN count(c) AS n"), GenerateOptions{}); strings.Contains(statement, "f_region") {
		t.Errorf("count(c) statement = %s, want only the first key", statement)
	}
}

// Without a primary key nothing tells two objects apart, so counting them
// distinct is refused, naming the property to count instead.
func TestCountDistinctNodeWithoutAKeyIsRefused(t *testing.T) {
	service := relationService(nil, &stubPermission{}, nil)
	service.vba = &recordingVega{}
	_, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: "MATCH (c:Customer) RETURN count(DISTINCT c) AS n",
	})
	if err == nil || !strings.Contains(err.Error(), "count(DISTINCT c.property)") {
		t.Fatalf("err = %v, want the refusal naming a property to count", err)
	}
}

// The trace records every key property count(DISTINCT n) reads, the same
// whether the count stands alone, sits in arithmetic or orders the rows.
func TestCountDistinctNodeTraceNamesEveryKey(t *testing.T) {
	want := []string{"property:kn_test:ot_customer:code", "property:kn_test:ot_customer:region"}
	for _, tc := range []struct{ query, alias string }{
		{"MATCH (c:Customer) RETURN count(DISTINCT c) AS n", "n"},
		{"MATCH (c:Customer) RETURN count(DISTINCT c) * 1 AS n", "n"},
		{"MATCH (c:Customer) RETURN c.region AS r, count(*) AS m ORDER BY count(DISTINCT c) DESC", ""},
	} {
		tree, err := Parse(tc.query)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		analyzed, err := Analyze(tree)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
		if err != nil {
			t.Fatalf("%s: Compile: %v", tc.query, err)
		}
		descriptor, err := BuildSemanticQueryDescriptor(plan, tc.query)
		if err != nil {
			t.Fatalf("%s: descriptor: %v", tc.query, err)
		}
		var got []string
		if tc.alias != "" {
			for _, projection := range descriptor.Projections {
				if projection.OutputPointer == semanticOutputPointer(tc.alias) {
					got = projection.PropertyRefs
				}
			}
		} else if len(descriptor.Ordering) == 1 {
			got = descriptor.Ordering[0].PropertyRefs
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: refs = %v, want %v", tc.query, got, want)
		}
	}
}

// Explain names the first key of a composite-key count(DISTINCT n) it sorts
// by, as it did when the trace recorded only that key.
func TestCountDistinctNodeOrderingExplainsItsFirstKey(t *testing.T) {
	query := "MATCH (c:Customer) RETURN c.region AS r, count(*) AS m ORDER BY count(DISTINCT c) DESC"
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	if got := orderingProperty(descriptor.Ordering[0]); got != "ot_customer.code" {
		t.Errorf("ordering property = %q, want ot_customer.code", got)
	}
}
