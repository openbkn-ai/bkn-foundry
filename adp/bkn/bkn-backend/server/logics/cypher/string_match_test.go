// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"errors"
	"strings"
	"testing"

	"bkn-backend/interfaces"
)

// #1720: STARTS WITH, ENDS WITH and CONTAINS become LIKE with '!' as the
// escape character, and the value is matched literally: the LIKE wildcards
// and SQL Server's '[' are escaped before the dialect's own quoting applies.
func TestCompileStringMatches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where string
		want  string
	}{
		{
			name:  "starts with",
			where: "o.region STARTS WITH 'eu'",
			want:  "t0.`f_region` LIKE 'eu%' ESCAPE '!'",
		},
		{
			name:  "ends with",
			where: "o.region ENDS WITH 'eu'",
			want:  "t0.`f_region` LIKE '%eu' ESCAPE '!'",
		},
		{
			name:  "contains",
			where: "o.region CONTAINS 'eu'",
			want:  "t0.`f_region` LIKE '%eu%' ESCAPE '!'",
		},
		{
			// Cypher: every string contains the empty string, and null
			// contains nothing. LIKE '%%' says the same.
			name:  "contains the empty string",
			where: "o.region CONTAINS ''",
			want:  "t0.`f_region` LIKE '%%' ESCAPE '!'",
		},
		{
			name:  "wildcards are matched literally",
			where: "o.region CONTAINS '50%_off'",
			want:  "t0.`f_region` LIKE '%50!%!_off%' ESCAPE '!'",
		},
		{
			name:  "the escape character is matched literally",
			where: "o.region STARTS WITH 'hi!'",
			want:  "t0.`f_region` LIKE 'hi!!%' ESCAPE '!'",
		},
		{
			name:  "a bracket is matched literally",
			where: "o.region ENDS WITH '[a]'",
			want:  "t0.`f_region` LIKE '%![a]' ESCAPE '!'",
		},
		{
			// A quote is doubled and a backslash doubled for MySQL, after
			// the LIKE escaping, so each layer reads its own.
			name:  "quote and backslash",
			where: `o.region CONTAINS 'a\'b\\c'`,
			want:  "t0.`f_region` LIKE '%a''b\\\\c%' ESCAPE '!'",
		},
		{
			name:  "and",
			where: "o.region STARTS WITH 'e' AND o.region ENDS WITH 'u'",
			want:  "t0.`f_region` LIKE 'e%' ESCAPE '!' AND t0.`f_region` LIKE '%u' ESCAPE '!'",
		},
		{
			name:  "or under and",
			where: "(o.region CONTAINS 'e' OR o.region = 'us') AND o.amount > 1",
			want:  "(t0.`f_region` LIKE '%e%' ESCAPE '!' OR t0.`f_region` = 'us') AND t0.`f_total` > 1",
		},
		{
			name:  "not",
			where: "NOT o.region CONTAINS 'eu'",
			want:  "NOT t0.`f_region` LIKE '%eu%' ESCAPE '!'",
		},
		{
			name:  "not over a group",
			where: "NOT (o.region STARTS WITH 'e' OR o.region IS NULL)",
			want:  "NOT (t0.`f_region` LIKE 'e%' ESCAPE '!' OR t0.`f_region` IS NULL)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compileWhere(t, tc.where); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestCompileStringMatchParameters(t *testing.T) {
	query := "MATCH (o:Order) WHERE o.region CONTAINS $v RETURN o.id AS id"

	got, err := compileWithParameters(t, query, map[string]any{"v": `%_![']\`})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := "t0.`f_region` LIKE '%!%!_!!!['']\\\\%' ESCAPE '!'"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("got  %s\nwant suffix %s", got, want)
	}

	for name, value := range map[string]any{"number": 1, "boolean": true, "list": []any{"a"}, "null": nil} {
		t.Run(name, func(t *testing.T) {
			_, err := compileWithParameters(t, query, map[string]any{"v": value})
			if err == nil {
				t.Fatalf("a %s parameter was accepted", name)
			}
			if _, ok := err.(*PlanError); !ok {
				t.Fatalf("error = %T (%v), want *PlanError", err, err)
			}
		})
	}

	if _, err := compileWithParameters(t, query, nil); err == nil {
		t.Fatal("a missing parameter was accepted")
	}
}

// PostgreSQL reads a backslash as itself, so only the quote is doubled; the
// LIKE escaping is the same in every dialect.
func TestGeneratePostgresStringMatch(t *testing.T) {
	tree, err := Parse(`MATCH (c:Customer) WHERE c.name CONTAINS 'a\\b\'c%' RETURN c.name AS n`)
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
	want := `SELECT t0."f_name" AS "n" FROM {{.res_customer}} t0 WHERE t0."f_name" LIKE '%a\b''c!%%' ESCAPE '!'`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestLikePattern(t *testing.T) {
	for _, tc := range []struct {
		operator StringMatchOperator
		value    string
		want     string
	}{
		{StartsWith, "", "%"},
		{EndsWith, "", "%"},
		{Contains, "", "%%"},
		{Contains, "!!", "%!!!!%"},
		{Contains, "a]b", "%a]b%"},
		{StartsWith, "%", "!%%"},
	} {
		if got := likePattern(tc.operator, tc.value); got != tc.want {
			t.Errorf("likePattern(%s, %q) = %q, want %q", tc.operator, tc.value, got, tc.want)
		}
	}
}

// The property a string predicate names is recorded like any other, so the
// level check in the service refuses a masked one the way it refuses a
// comparison on it.
func TestStringMatchRecordsItsProperty(t *testing.T) {
	tree, err := Parse("MATCH (o:Order) WHERE o.region CONTAINS 'e' RETURN o.id")
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
	for _, property := range plan.Properties {
		if property.Property == "region" {
			return
		}
	}
	t.Fatalf("properties = %v, want region among them", plan.Properties)
}

// A string predicate on a property the model declares as something other than
// a string is refused while planning, so every data source gives the same
// answer instead of MySQL matching a number's text and the others failing.
func TestStringMatchNeedsAStringProperty(t *testing.T) {
	typed := func(name, column, propertyType string) *interfaces.DataProperty {
		property := dataProperty(name, column)
		property.Type = propertyType
		return property
	}
	schema := testSchema(t, &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{
		objectType("ot_order", "Order", resource("res_order", "orders"),
			dataProperty("id", "f_id"),
			typed("amount", "f_amount", "integer"),
			typed("paid_at", "f_paid_at", "datetime"),
			typed("note", "f_note", "text"),
			typed("code", "f_code", "keyword"),
			typed("legacy", "f_legacy", ""),
		),
	}})
	compile := func(query string) error {
		tree, err := Parse(query)
		if err != nil {
			return err
		}
		analyzed, err := Analyze(tree)
		if err != nil {
			return err
		}
		_, err = Compile(analyzed, schema, CompileOptions{})
		return err
	}

	for _, property := range []string{"amount", "paid_at"} {
		err := compile("MATCH (o:Order) WHERE o." + property + " CONTAINS '1' RETURN o.id")
		var planErr *PlanError
		if !errors.As(err, &planErr) || !strings.Contains(err.Error(), "applies to string properties") {
			t.Fatalf("%s: error = %v, want a plan error about string properties", property, err)
		}
	}
	for _, property := range []string{"id", "note", "code", "legacy"} {
		if err := compile("MATCH (o:Order) WHERE o." + property + " STARTS WITH 'a' RETURN o.id"); err != nil {
			t.Fatalf("%s: %v", property, err)
		}
	}
}
