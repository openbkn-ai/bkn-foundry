// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"strings"
	"testing"
)

func compileWithParameters(t *testing.T, query string, parameters map[string]any) (string, error) {
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

// A parameter is a value. It reaches the statement through the same escaping
// as a literal written in the query, which is what makes it safe to accept.
func TestCompileParameters(t *testing.T) {
	for _, tc := range []struct {
		name       string
		where      string
		parameters map[string]any
		want       string
	}{
		{
			name:       "string",
			where:      "o.region = $region",
			parameters: map[string]any{"region": "eu"},
			want:       "t0.`f_region` = 'eu'",
		},
		{
			name:       "a value that would end the literal",
			where:      "o.region = $region",
			parameters: map[string]any{"region": `a'; DROP TABLE x --`},
			want:       "t0.`f_region` = 'a''; DROP TABLE x --'",
		},
		{
			name:       "a value ending in a backslash",
			where:      "o.region = $region",
			parameters: map[string]any{"region": `abc\`},
			want:       "t0.`f_region` = 'abc\\\\'",
		},
		{
			name:       "integer",
			where:      "o.amount > $floor",
			parameters: map[string]any{"floor": 100},
			want:       "t0.`f_total` > 100",
		},
		{
			// JSON has one number type, so an integer arrives as a float.
			// Writing it back as 100.0 would stop it matching an integer key.
			name:       "integer that arrived as a float",
			where:      "o.amount > $floor",
			parameters: map[string]any{"floor": float64(100)},
			want:       "t0.`f_total` > 100",
		},
		{
			name:       "float",
			where:      "o.amount > $floor",
			parameters: map[string]any{"floor": 1.5},
			want:       "t0.`f_total` > 1.5",
		},
		{
			name:       "boolean",
			where:      "o.region = $flag",
			parameters: map[string]any{"flag": true},
			want:       "t0.`f_region` = TRUE",
		},
		{
			name:       "json number keeps its precision",
			where:      "o.amount = $exact",
			parameters: map[string]any{"exact": json.Number("9223372036854775807")},
			want:       "t0.`f_total` = 9223372036854775807",
		},
		{
			name:       "inside IN",
			where:      "o.region IN [$first, 'us']",
			parameters: map[string]any{"first": "eu"},
			want:       "t0.`f_region` IN ('eu', 'us')",
		},
		{
			name:       "backticked name",
			where:      "o.region = $`the region`",
			parameters: map[string]any{"the region": "eu"},
			want:       "t0.`f_region` = 'eu'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileWithParameters(t,
				"MATCH (o:Order) WHERE "+tc.where+" RETURN o.id AS id", tc.parameters)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("got  %s\nwant it to contain %s", sql, tc.want)
			}
		})
	}
}

func TestCompileParameterRejections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{
			name:  "not supplied",
			query: "MATCH (o:Order) WHERE o.region = $region RETURN o.id",
			want:  `parameter "region" was not supplied`,
		},
		{
			name:       "null",
			query:      "MATCH (o:Order) WHERE o.region = $region RETURN o.id",
			parameters: map[string]any{"region": nil},
			want:       "write IS NULL",
		},
		{
			name:       "a type the statement cannot carry",
			query:      "MATCH (o:Order) WHERE o.region = $region RETURN o.id",
			parameters: map[string]any{"region": []string{"eu"}},
			want:       "parameters must be a string, a number or a boolean",
		},
		{
			// A parameter names a value, never a table or a column, so there
			// is nowhere else in the statement it could appear.
			name:  "in place of a label",
			query: "MATCH (o:$label) RETURN o.id",
			want:  "line 1",
		},
		{
			name:  "positional",
			query: "MATCH (o:Order) WHERE o.region = $0 RETURN o.id",
			want:  "positional parameters",
		},
		{
			name:  "negated",
			query: "MATCH (o:Order) WHERE o.amount > -$floor RETURN o.id",
			want:  "negating a parameter",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileWithParameters(t, tc.query, tc.parameters)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// JSON has one number type and no bound, so a value that is neither an integer
// nor representable as a float has to be refused rather than truncated into
// one that compares against the wrong rows.
func TestParameterNumbers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value json.Number
		want  Literal
	}{
		{name: "integer", value: json.Number("42"), want: Literal{Kind: LiteralInteger, Integer: 42}},
		{name: "float", value: json.Number("1.5"), want: Literal{Kind: LiteralFloat, Float: 1.5}},
		{
			name:  "beyond int64 falls back to a float",
			value: json.Number("100000000000000000000"),
			want:  Literal{Kind: LiteralFloat, Float: 1e20},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := literalFromParameter(tc.value)
			if err != nil {
				t.Fatalf("literalFromParameter(%s): %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	if _, err := literalFromParameter(json.Number("not a number")); err == nil ||
		!strings.Contains(err.Error(), "not a number this interface can carry") {
		t.Fatalf("a malformed number must be refused, got %v", err)
	}
}
