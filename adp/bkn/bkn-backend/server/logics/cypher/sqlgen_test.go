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

func TestGenerateRejectsWhatItCannotWrite(t *testing.T) {
	plan := &Plan{
		Tables: []PlanTable{{Alias: "t0", ResourceID: "res", Label: "Order"}},
		Select: []PlanColumn{{Table: 0, Column: "f_id", Alias: "id"}},
	}

	if _, err := Generate(plan, GenerateOptions{Dialect: "oracle"}); err == nil ||
		!strings.Contains(err.Error(), "unsupported SQL dialect") {
		t.Fatalf("an unknown dialect must be refused, got %v", err)
	}
	if _, err := Generate(&Plan{}, GenerateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "empty plan") {
		t.Fatalf("an empty plan must be refused, got %v", err)
	}
}

// A null byte truncates the statement in the client libraries underneath, so
// it must not reach them -- in a name or in a value.
func TestGenerateRejectsNullBytes(t *testing.T) {
	inAlias := &Plan{
		Tables: []PlanTable{{Alias: "t0", ResourceID: "res"}},
		Select: []PlanColumn{{Table: 0, Column: "f_id", Alias: "a\x00b"}},
	}
	if _, err := Generate(inAlias, GenerateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "null byte") {
		t.Fatalf("a null byte in a name must be refused, got %v", err)
	}

	inValue := &Plan{
		Tables: []PlanTable{{Alias: "t0", ResourceID: "res"}},
		Select: []PlanColumn{{Table: 0, Column: "f_id", Alias: "id"}},
		Where: []PlanCondition{{
			Table: 0, Column: "f_name", Operator: "=",
			Value: Literal{Kind: LiteralString, String: "a\x00b"},
		}},
	}
	if _, err := Generate(inValue, GenerateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "null byte") {
		t.Fatalf("a null byte in a value must be refused, got %v", err)
	}
}

// The analyzer refuses null comparisons, so reaching generation with one means
// the stages disagree. It says that rather than writing SQL nobody asked for.
func TestGenerateRefusesLiteralsTheAnalyzerNeverProduces(t *testing.T) {
	plan := &Plan{
		Tables: []PlanTable{{Alias: "t0", ResourceID: "res"}},
		Select: []PlanColumn{{Table: 0, Column: "f_id", Alias: "id"}},
		Where: []PlanCondition{{
			Table: 0, Column: "f_name", Operator: "=", Value: Literal{Kind: LiteralNull},
		}},
	}
	if _, err := Generate(plan, GenerateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "null") {
		t.Fatalf("want a refusal naming the literal, got %v", err)
	}
}

func TestGenerateBooleanLiterals(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"MATCH (o:Order) WHERE o.region = true RETURN o.id", "= TRUE"},
		{"MATCH (o:Order) WHERE o.region = false RETURN o.id", "= FALSE"},
	} {
		got := mustCompile(t, tc.query)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("got %s, want it to contain %s", got, tc.want)
		}
	}
}

// SKIP without a limit cannot be written as MySQL, and the service always
// injects one; a plan that arrives without either is a disagreement between
// the stages rather than a query anyone wrote.
func TestGenerateRefusesSkipWithoutLimit(t *testing.T) {
	skip := int64(10)
	plan := &Plan{
		Tables: []PlanTable{{Alias: "t0", ResourceID: "res"}},
		Select: []PlanColumn{{Table: 0, Column: "f_id", Alias: "id"}},
		Skip:   &skip,
	}
	if _, err := Generate(plan, GenerateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "SKIP requires a LIMIT") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
