// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"errors"
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

// A whole list passed as one parameter, IN $name, has to produce the same
// statement as the list written in the query: both go through the literal
// path, so both are escaped the same way (#1719).
func TestCompileListParameterMatchesListLiteral(t *testing.T) {
	for _, tc := range []struct {
		name       string
		literal    string
		parameter  string
		parameters map[string]any
	}{
		{
			name:       "integers",
			literal:    "o.id IN [6, 7, 8]",
			parameter:  "o.id IN $keys",
			parameters: map[string]any{"keys": []any{float64(6), float64(7), float64(8)}},
		},
		{
			name:       "strings with quotes",
			literal:    `o.region IN ['eu', 'a\'; DROP TABLE x --']`,
			parameter:  "o.region IN $regions",
			parameters: map[string]any{"regions": []any{"eu", "a'; DROP TABLE x --"}},
		},
		{
			name:       "integers and floats are both numbers",
			literal:    "o.amount IN [1, 2.5]",
			parameter:  "o.amount IN $amounts",
			parameters: map[string]any{"amounts": []any{json.Number("1"), 2.5}},
		},
		{
			name:       "negated",
			literal:    "NOT (o.id IN [6, 7])",
			parameter:  "NOT (o.id IN $keys)",
			parameters: map[string]any{"keys": []any{6, 7}},
		},
		{
			name:       "element-wise parameters",
			literal:    "o.id IN [6, 7]",
			parameter:  "o.id IN [$a, $b]",
			parameters: map[string]any{"a": 6, "b": 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, err := compileWithParameters(t, "MATCH (o:Order) WHERE "+tc.literal+" RETURN o.id AS id", nil)
			if err != nil {
				t.Fatalf("compile literal: %v", err)
			}
			got, err := compileWithParameters(t, "MATCH (o:Order) WHERE "+tc.parameter+" RETURN o.id AS id", tc.parameters)
			if err != nil {
				t.Fatalf("compile parameter: %v", err)
			}
			if got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestAnalyzeListParameter(t *testing.T) {
	query := mustAnalyze(t, "MATCH (o:Order) WHERE o.id IN $keys RETURN o.id")
	membership, ok := query.Where.(Membership)
	if !ok {
		t.Fatalf("where = %T, want Membership", query.Where)
	}
	if membership.ListParameter == nil || membership.ListParameter.Name != "keys" || len(membership.Values) != 0 {
		t.Fatalf("membership = %+v", membership)
	}
}

func TestPlanListParameterPointers(t *testing.T) {
	tree, err := Parse("MATCH (o:Order) WHERE o.id IN $keys RETURN o.id AS id")
	if err != nil {
		t.Fatal(err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(analyzed, modelSchema(t), CompileOptions{Parameters: map[string]any{"keys": []any{6, 7}}})
	if err != nil {
		t.Fatal(err)
	}
	membership, ok := plan.Where.(PlanMembership)
	if !ok {
		t.Fatalf("where = %T", plan.Where)
	}
	if len(membership.Values) != 2 || membership.ListInputPointer != "$.parameters.keys" ||
		len(membership.InputPointers) != 0 {
		t.Fatalf("membership = %+v", membership)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, tree.GetText())
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Predicates) != 1 || descriptor.Predicates[0].InputPointer != "$.parameters.keys" ||
		descriptor.Predicates[0].Operator != "in" || len(descriptor.Predicates[0].ValueHashes) != 2 {
		t.Fatalf("predicates = %+v", descriptor.Predicates)
	}
}

func TestCompileListParameterRejections(t *testing.T) {
	tooMany := make([]any, MaxListParameterLength+1)
	for i := range tooMany {
		tooMany[i] = i
	}
	atLimit := make([]any, MaxListParameterLength)
	for i := range atLimit {
		atLimit[i] = i
	}
	if _, err := compileWithParameters(t, "MATCH (o:Order) WHERE o.id IN $keys RETURN o.id",
		map[string]any{"keys": atLimit}); err != nil {
		t.Fatalf("a list at the limit must compile: %v", err)
	}
	for _, tc := range []struct {
		name  string
		value any
		skip  bool
		want  string
	}{
		{name: "missing", skip: true, want: `parameter "keys" was not supplied`},
		{name: "null", value: nil, want: "is null"},
		{name: "scalar", value: 6, want: "needs the parameter to be a list"},
		{name: "object", value: map[string]any{"a": 1}, want: "is an object; IN $name needs"},
		{name: "empty", value: []any{}, want: "is an empty list"},
		{name: "null element", value: []any{1, nil}, want: "null at index 1"},
		{name: "nested list", value: []any{1, []any{2}}, want: "element 1 is a list"},
		{name: "object element", value: []any{map[string]any{"a": 1}}, want: "element 0 is an object"},
		{name: "number out of range", value: []any{json.Number("1e999")}, want: "element 0 is not a number this interface can carry"},
		{name: "mixed kinds", value: []any{1, "2"}, want: "mixes number and string"},
		{name: "mixed booleans", value: []any{true, "x"}, want: "mixes boolean and string"},
		{name: "too long", value: tooMany, want: "at most 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parameters := map[string]any{}
			if !tc.skip {
				parameters["keys"] = tc.value
			}
			_, err := compileWithParameters(t, "MATCH (o:Order) WHERE o.id IN $keys RETURN o.id", parameters)
			var planErr *PlanError
			if err == nil || !errors.As(err, &planErr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a plan error mentioning %q", err, tc.want)
			}
			// The caller wrote JSON; a Go type name in the error means nothing to them.
			if strings.Contains(err.Error(), "interface {}") || strings.Contains(err.Error(), "[]") {
				t.Fatalf("error names a Go type: %v", err)
			}
		})
	}
}
