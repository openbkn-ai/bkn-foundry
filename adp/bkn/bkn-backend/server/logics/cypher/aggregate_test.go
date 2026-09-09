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

// Cypher has no GROUP BY: aggregating anything groups by everything else that
// is returned. These assert the whole statement, because the grouping is
// derived rather than written and a change in it has to be deliberate.
func TestCompileAggregates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "count of rows",
			query: "MATCH (o:Order) RETURN count(*) AS n",
			want:  "SELECT COUNT(*) AS `n` FROM {{.res_order}} t0",
		},
		{
			name:  "count of values",
			query: "MATCH (o:Order) RETURN count(o.region) AS n",
			want:  "SELECT COUNT(t0.`f_region`) AS `n` FROM {{.res_order}} t0",
		},
		{
			name:  "count of distinct values",
			query: "MATCH (o:Order) RETURN count(DISTINCT o.region) AS n",
			want:  "SELECT COUNT(DISTINCT t0.`f_region`) AS `n` FROM {{.res_order}} t0",
		},
		{
			name:  "grouping is derived from what is returned",
			query: "MATCH (o:Order) RETURN o.region AS region, count(*) AS n",
			want: "SELECT t0.`f_region` AS `region`, COUNT(*) AS `n` FROM {{.res_order}} t0 " +
				"GROUP BY t0.`f_region`",
		},
		{
			name:  "several groups and several aggregates",
			query: "MATCH (o:Order) RETURN o.region AS r, o.customer_code AS c, sum(o.amount) AS total, max(o.amount) AS biggest",
			want: "SELECT t0.`f_region` AS `r`, t0.`f_cust_code` AS `c`, SUM(t0.`f_total`) AS `total`, " +
				"MAX(t0.`f_total`) AS `biggest` FROM {{.res_order}} t0 " +
				"GROUP BY t0.`f_region`, t0.`f_cust_code`",
		},
		{
			name:  "everything aggregated is one row and no grouping",
			query: "MATCH (o:Order) RETURN min(o.amount) AS lowest, avg(o.amount) AS mean",
			want:  "SELECT MIN(t0.`f_total`) AS `lowest`, AVG(t0.`f_total`) AS `mean` FROM {{.res_order}} t0",
		},
		{
			name:  "aggregate over a join",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c.name AS customer, count(*) AS n",
			want: "SELECT t1.`f_name` AS `customer`, COUNT(*) AS `n` " +
				"FROM {{.res_order}} t0 JOIN {{.res_customer}} t1 " +
				"ON t0.`f_cust_code` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
				"GROUP BY t1.`f_name`",
		},
		{
			name:  "sorted by the name of the aggregate",
			query: "MATCH (o:Order) RETURN o.region AS region, count(*) AS n ORDER BY n DESC LIMIT 5",
			want: "SELECT t0.`f_region` AS `region`, COUNT(*) AS `n` FROM {{.res_order}} t0 " +
				"GROUP BY t0.`f_region` ORDER BY `n` DESC LIMIT 5",
		},
		{
			name:  "sorted by the aggregate written out again",
			query: "MATCH (o:Order) RETURN o.region AS region, count(*) AS n ORDER BY count(*) DESC",
			want: "SELECT t0.`f_region` AS `region`, COUNT(*) AS `n` FROM {{.res_order}} t0 " +
				"GROUP BY t0.`f_region` ORDER BY COUNT(*) DESC",
		},
		{
			name:  "unaliased aggregate is named after what was written",
			query: "MATCH (o:Order) RETURN count(DISTINCT o.region)",
			want:  "SELECT COUNT(DISTINCT t0.`f_region`) AS `count(DISTINCT o.region)` FROM {{.res_order}} t0",
		},
		{
			name:  "filtered before grouping",
			query: "MATCH (o:Order) WHERE o.amount > 100 RETURN o.region AS region, count(*) AS n",
			want: "SELECT t0.`f_region` AS `region`, COUNT(*) AS `n` FROM {{.res_order}} t0 " +
				"WHERE t0.`f_total` > 100 GROUP BY t0.`f_region`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustCompile(t, tc.query); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestCompileAggregateRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "sorting a grouped result by something it does not return",
			query: "MATCH (o:Order) RETURN o.region AS r, count(*) AS n ORDER BY o.amount",
			want:  "can only be sorted by a returned value",
		},
		{
			name:  "sorting by a name the query does not return",
			query: "MATCH (o:Order) RETURN o.region AS r, count(*) AS n ORDER BY total",
			want:  `"total" is not returned by this query`,
		},
		{
			name:  "an aggregate over something that is not a property",
			query: "MATCH (o:Order) RETURN sum(1) AS n",
			want:  "only variable.property references are supported",
		},
		{
			name:  "an aggregate over an unknown property",
			query: "MATCH (o:Order) RETURN sum(o.nope) AS n",
			want:  `has no property "nope"`,
		},
		{
			name:  "a function that is not an aggregate",
			query: "MATCH (o:Order) RETURN collect(o.region) AS n",
			want:  "function calls",
		},
		{
			name:  "an aggregate over several arguments",
			query: "MATCH (o:Order) RETURN max(o.amount, o.region) AS n",
			want:  "an aggregate over 2 arguments",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile(t, tc.query, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// The duplicate-name check has to point at the item that repeats the name,
// whichever kind it is.
func TestCompileDuplicateAliasOnAggregatePointsAtIt(t *testing.T) {
	_, err := compile(t, "MATCH (o:Order)\nRETURN o.id AS x,\ncount(*) AS x", GenerateOptions{})
	planError, ok := err.(*PlanError)
	if !ok {
		t.Fatalf("error = %T (%v), want *PlanError", err, err)
	}
	if planError.Pos.Line != 3 {
		t.Fatalf("line = %d, want 3", planError.Pos.Line)
	}
}
