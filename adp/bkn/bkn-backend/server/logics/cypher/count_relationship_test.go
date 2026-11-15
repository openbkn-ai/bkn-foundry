// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// count(r) counts relationships: every row of a MATCH holds one, a row an
// OPTIONAL MATCH found nothing for holds none, and DISTINCT counts each pair
// of objects once however many rows repeat it.
func TestCountRelationshipResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			name:  "every matched row holds the relationship",
			query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(r) AS n",
			want:  [][]any{{3}},
		},
		{
			name: "a relationship repeated by a longer pattern is counted once with DISTINCT",
			query: "MATCH (i:Item)-[e:BELONGS_TO]->(o:Order)-[r:PLACED_BY]->(c:Customer) " +
				"RETURN count(r) AS rows, count(DISTINCT r) AS relationships",
			want: [][]any{{3, 2}},
		},
		{
			name: "a row an OPTIONAL MATCH found nothing for is not counted",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[e:BELONGS_TO]-(i:Item) " +
				"RETURN count(*) AS rows, count(e) AS n, count(DISTINCT e) AS d",
			want: [][]any{{5, 3, 3}},
		},
		{
			name: "grouped, an object with no relationship counts zero",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[e:BELONGS_TO]-(i:Item) " +
				"RETURN o.id AS id, count(e) AS n ORDER BY id",
			want: [][]any{{1, 2}, {2, 1}, {3, 0}, {4, 0}},
		},
		{
			name: "counted in a WITH and passed on",
			query: "MATCH (i:Item)-[e:BELONGS_TO]->(o:Order) WITH o, count(e) AS n " +
				"RETURN o.id AS id, n ORDER BY id",
			want: [][]any{{1, 2}, {2, 1}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, nil)
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

// What each form of count(r) compiles to: a count of rows, a count of the
// optional end's key, and a count of both ends' keys together.
func TestCountRelationshipStatements(t *testing.T) {
	for _, tc := range []struct {
		name    string
		query   string
		dialect Dialect
		want    string
	}{
		{
			name:  "inside a MATCH",
			query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(r) AS n",
			want:  "SELECT COUNT(*) AS `n` FROM",
		},
		{
			name:  "inside an OPTIONAL MATCH",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[e:BELONGS_TO]-(i:Item) RETURN count(e) AS n",
			want:  "SELECT COUNT(t1.`f_id`) AS `n` FROM",
		},
		{
			name:  "DISTINCT on MySQL",
			query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(DISTINCT r) AS n",
			want:  "COUNT(DISTINCT t0.`f_id`, t1.`f_code`, t1.`f_region`)",
		},
		{
			name:    "DISTINCT on Postgres",
			query:   "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(DISTINCT r) AS n",
			dialect: DialectPostgres,
			want:    `COUNT(DISTINCT (t0."f_id", t1."f_code", t1."f_region"))`,
		},
		{
			name:  "DISTINCT starts from the source whichever way the pattern is written",
			query: "MATCH (c:Customer)<-[r:PLACED_BY]-(o:Order) RETURN count(DISTINCT r) AS n",
			want:  "COUNT(DISTINCT t1.`f_id`, t0.`f_code`, t0.`f_region`)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _ := compileBindJoin(t, tc.query, nil)
			statement, err := Generate(plan, GenerateOptions{Dialect: tc.dialect})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !strings.Contains(statement, tc.want) {
				t.Fatalf("statement = %s, want it to contain %s", statement, tc.want)
			}
		})
	}
}

// Before count(r) was planned, a bound relationship variable was reported as
// not defined at all. Where its count cannot be told, the refusal now says why.
func TestCountRelationshipRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "DISTINCT over an end without a primary key",
			query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(DISTINCT r) AS n",
			want:  `count(DISTINCT r) needs both ends to have a primary key`,
		},
		{
			name:  "an optional end without a primary key",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)-[r:PLACED_BY]->(c:Customer) RETURN count(r) AS n",
			want:  `count(r) needs the object type "Customer" to have a primary key`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile(t, tc.query, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a refusal mentioning %q", tc.query, err, tc.want)
			}
		})
	}
	// Customer has no primary key in this schema, but a MATCH needs none.
	if _, err := compile(t, "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(r) AS n", GenerateOptions{}); err != nil {
		t.Fatalf("count(r) inside a MATCH: %v", err)
	}
}

// count(DISTINCT r) reads both ends' keys, so the trace records and the
// authorization checks every one of them.
func TestCountDistinctRelationshipReadsBothEndsKeys(t *testing.T) {
	query := "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN count(DISTINCT r) AS n"
	plan, _ := compileBindJoin(t, query, nil)
	read := map[string]bool{}
	for _, property := range plan.Properties {
		read[property.ObjectTypeID+"."+property.Property] = true
	}
	for _, want := range []string{"ot_order.id", "ot_customer.code", "ot_customer.region"} {
		if !read[want] {
			t.Errorf("properties = %v, want %s among them", plan.Properties, want)
		}
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	want := []string{
		"property:kn_test:ot_order:id",
		"property:kn_test:ot_customer:code",
		"property:kn_test:ot_customer:region",
	}
	if got := descriptor.Projections[0].PropertyRefs; !slices.Equal(got, want) {
		t.Errorf("refs = %v, want %v", got, want)
	}
}
