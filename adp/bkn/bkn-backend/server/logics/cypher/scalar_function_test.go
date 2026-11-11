// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"
	"testing"
)

// Each scalar function is written in MySQL 5.7 with the functions vega-backend
// accepts, in the form that keeps its Cypher meaning.
func TestScalarFunctionSQL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"round gives a float", "MATCH (o:Order) RETURN round(o.amount) AS r", "(ROUND(t0.`f_amount`) + 0E0) AS `r`"},
		{"round to places", "MATCH (o:Order) RETURN round(o.amount, 2) AS r", "(ROUND(t0.`f_amount`, 2) + 0E0) AS `r`"},
		{"abs", "MATCH (o:Order) RETURN abs(o.amount) AS a", "ABS(t0.`f_amount`) AS `a`"},
		{"toUpper", "MATCH (o:Order) RETURN toUpper(o.region) AS u", "UPPER(t0.`f_region`) AS `u`"},
		{"toLower", "MATCH (o:Order) RETURN tolower(o.region) AS l", "LOWER(t0.`f_region`) AS `l`"},
		{"toString", "MATCH (o:Order) RETURN toString(o.id) AS s", "CAST(t0.`f_id` AS CHAR) AS `s`"},
		{"toInteger truncates toward zero", "MATCH (o:Order) RETURN toInteger(o.amount) AS i",
			"CAST(CASE WHEN t0.`f_amount` < 0 THEN CEIL(t0.`f_amount`) ELSE FLOOR(t0.`f_amount`) END AS SIGNED) AS `i`"},
		{"toFloat", "MATCH (o:Order) RETURN toFloat(o.amount) AS f", "(t0.`f_amount` + 0E0) AS `f`"},
		{"over an aggregate", "MATCH (o:Order) RETURN o.region AS region, round(sum(o.amount), 1) AS total",
			"(ROUND(SUM(t0.`f_amount`), 1) + 0E0) AS `total`"},
		{"inside an aggregate", "MATCH (o:Order) RETURN sum(round(o.amount)) AS total", "SUM((ROUND(t0.`f_amount`) + 0E0))"},
		{"in WHERE", "MATCH (o:Order) WHERE toUpper(o.region) = 'EAST' RETURN o.id", "WHERE UPPER(t0.`f_region`) = 'EAST'"},
		{"in ORDER BY", "MATCH (o:Order) RETURN o.id ORDER BY round(o.amount) DESC", "ORDER BY (ROUND(t0.`f_amount`) + 0E0) DESC"},
		{"inside coalesce", "MATCH (o:Order) RETURN coalesce(toUpper(o.region), 'NONE') AS r", "COALESCE(UPPER(t0.`f_region`), 'NONE')"},
		// round gives a float, so a division by it is not an integer one.
		{"divided after round", "MATCH (o:Order) RETURN round(o.amount) / 2 AS half", "(ROUND(t0.`f_amount`) + 0E0) / 2"},
		{"after a WITH", "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n RETURN o.id AS id, toFloat(o.amount) / n AS per",
			"(t1.`f_amount` + 0E0) / COUNT("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileComparison(t, tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("SQL lacks %q:\n%s", tc.want, sql)
			}
		})
	}
}

func TestScalarFunctionRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"round of a string", "MATCH (o:Order) RETURN round(o.region) AS r", "round() takes a number"},
		{"toInteger of a string", "MATCH (o:Order) RETURN toInteger(o.region) AS r", "toInteger() takes a number"},
		{"toUpper of a number", "MATCH (o:Order) RETURN toUpper(o.amount) AS r", "toUpper() takes a string; write toString(x) first"},
		{"fractional places", "MATCH (o:Order) RETURN round(o.amount, 2.5) AS r", "an integer from 0 to 30"},
		{"negative places", "MATCH (o:Order) RETURN round(o.amount, -1) AS r", "an integer from 0 to 30"},
		{"too many places", "MATCH (o:Order) RETURN round(o.amount, 31) AS r", "an integer from 0 to 30"},
		{"places as a parameter", "MATCH (o:Order) RETURN round(o.amount, $p) AS r", "an integer from 0 to 30"},
		{"no argument", "MATCH (o:Order) RETURN round() AS r", "write round(x) or round(x, 2)"},
		{"too many arguments", "MATCH (o:Order) RETURN abs(o.amount, 1) AS r", "write abs(x)"},
		{"DISTINCT", "MATCH (o:Order) RETURN round(DISTINCT o.amount) AS r", "DISTINCT belongs in an aggregate"},
		{"string arithmetic", "MATCH (o:Order) RETURN toString(o.id) + 1 AS r", "arithmetic"},
		{"a function still missing", "MATCH (o:Order) RETURN sqrt(o.amount) AS r", "the function sqrt() is not supported"},
		// Truncating gives an integer, so dividing by one still has to say
		// which division it means.
		{"integer division after toInteger", "MATCH (o:Order) RETURN toInteger(o.amount) / 2 AS r", "dividing values that may both be integers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileComparison(t, tc.query, map[string]any{"p": 2})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A function may sit in an optional condition: given a value, it filters;
// left out, the condition is dropped with the function in it.
func TestScalarFunctionInOptionalCondition(t *testing.T) {
	query := "MATCH (o:Order) WHERE ($r IS NULL OR toUpper(o.region) = $r) RETURN o.id"
	sql, err := compileComparison(t, query, map[string]any{"r": "EAST"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "UPPER(t0.`f_region`) = ") {
		t.Fatalf("given a value, SQL lacks the function condition:\n%s", sql)
	}
	sql, err = compileComparison(t, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "UPPER") || strings.Contains(sql, "WHERE") {
		t.Fatalf("left out, SQL still filters:\n%s", sql)
	}
}

// Only the SQL path computes these, as it does CASE and coalesce, so a query
// on a dataset or an index is refused rather than half answered.
func TestScalarFunctionIsSQLOnly(t *testing.T) {
	plan, _ := compileBindJoin(t, "MATCH (o:Order) RETURN toUpper(o.region) AS u", nil)
	if !plan.SQLOnly {
		t.Fatal("a plan with a scalar function must be marked SQLOnly")
	}
}
