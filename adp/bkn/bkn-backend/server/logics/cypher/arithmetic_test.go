// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"
	"testing"

	"bkn-backend/interfaces"
)

// arithmeticSchema types its properties, because what arithmetic accepts
// depends on the types: an integer, a float, a decimal, a string, and one the
// model leaves untyped.
func arithmeticSchema(t *testing.T) *Schema {
	t.Helper()
	typed := func(name, column, dataType string) *interfaces.DataProperty {
		property := dataProperty(name, column)
		property.Type = dataType
		return property
	}
	line := objectType("ot_line", "Line", resource("res_line", "lines"),
		typed("id", "f_id", "integer"),
		typed("quantity", "f_qty", "integer"),
		typed("price", "f_price", "float"),
		typed("discount", "f_discount", "decimal"),
		typed("name", "f_name", "string"),
		typed("note", "f_note", ""),
	)
	return testSchema(t, &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{line}})
}

func compileArithmetic(t *testing.T, query string, parameters map[string]any) (*Plan, string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, "", err
	}
	plan, err := Compile(analyzed, arithmeticSchema(t), CompileOptions{Parameters: parameters})
	if err != nil {
		return nil, "", err
	}
	sql, err := Generate(plan, GenerateOptions{})
	return plan, sql, err
}

func TestCompileArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{
			// The reported case: an aggregate scaled by a number.
			name:  "an aggregate times a number",
			query: "MATCH (l:Line) RETURN count(*) * 2 AS doubled",
			want:  "SELECT COUNT(*) * 2 AS `doubled` FROM {{.res_line}} t0",
		},
		{
			name:  "an unaliased computed column is named after what was written",
			query: "MATCH (l:Line) RETURN count(l.id) * 2",
			want:  "SELECT COUNT(t0.`f_id`) * 2 AS `count(l.id) * 2` FROM {{.res_line}} t0",
		},
		{
			name:  "count is a number whatever it counts",
			query: "MATCH (l:Line) RETURN count(l.name) + 1 AS n",
			want:  "SELECT COUNT(t0.`f_name`) + 1 AS `n` FROM {{.res_line}} t0",
		},
		{
			name:  "two properties",
			query: "MATCH (l:Line) RETURN l.price * l.quantity AS total",
			want:  "SELECT t0.`f_price` * t0.`f_qty` AS `total` FROM {{.res_line}} t0",
		},
		{
			name:  "parentheses the tree needs are kept",
			query: "MATCH (l:Line) RETURN (l.price + 1) * 2 AS x",
			want:  "SELECT (t0.`f_price` + 1) * 2 AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "parentheses the tree does not need are dropped",
			query: "MATCH (l:Line) RETURN l.quantity + (l.id * 2) AS x",
			want:  "SELECT t0.`f_qty` + t0.`f_id` * 2 AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "subtraction on the right keeps its parentheses",
			query: "MATCH (l:Line) RETURN l.quantity - (l.id - 1) AS x",
			want:  "SELECT t0.`f_qty` - (t0.`f_id` - 1) AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "operators of one precedence read left to right",
			query: "MATCH (l:Line) RETURN l.quantity - l.id - 1 AS x",
			want:  "SELECT t0.`f_qty` - t0.`f_id` - 1 AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "a negated property",
			query: "MATCH (l:Line) RETURN -l.price AS x",
			want:  "SELECT -t0.`f_price` AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "a negated computation",
			query: "MATCH (l:Line) RETURN -(l.price + 1) AS x",
			want:  "SELECT -(t0.`f_price` + 1) AS `x` FROM {{.res_line}} t0",
		},
		{
			// Written with a space so that no database reads -- as a comment.
			name:  "a negative number on the right of a minus",
			query: "MATCH (l:Line) RETURN l.quantity - -1 AS x",
			want:  "SELECT t0.`f_qty` - -1 AS `x` FROM {{.res_line}} t0",
		},
		{
			// A whole float keeps an exponent: printed as 1 it would make the
			// division integral again, and MySQL reads an exponent as a double
			// rather than a decimal rounded to four places.
			name:  "a ratio made fractional by a float",
			query: "MATCH (l:Line) RETURN count(*) * 1.0 / count(l.id) AS ratio",
			want:  "SELECT COUNT(*) * 1E+00 / COUNT(t0.`f_id`) AS `ratio` FROM {{.res_line}} t0",
		},
		{
			name:  "dividing by a float property",
			query: "MATCH (l:Line) RETURN l.quantity / l.price AS x",
			want:  "SELECT t0.`f_qty` / t0.`f_price` AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "dividing a decimal",
			query: "MATCH (l:Line) RETURN l.discount / 2 AS x",
			want:  "SELECT t0.`f_discount` / 2 AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "avg is a fraction",
			query: "MATCH (l:Line) RETURN sum(l.quantity) / avg(l.quantity) AS x",
			want:  "SELECT SUM(t0.`f_qty`) / AVG(t0.`f_qty`) AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "remainder of integers",
			query: "MATCH (l:Line) RETURN l.quantity % 2 AS parity",
			want:  "SELECT t0.`f_qty` % 2 AS `parity` FROM {{.res_line}} t0",
		},
		{
			name:       "a parameter is bound as a number",
			query:      "MATCH (l:Line) RETURN l.price * $rate AS x",
			parameters: map[string]any{"rate": 1.5},
			want:       "SELECT t0.`f_price` * 1.5E+00 AS `x` FROM {{.res_line}} t0",
		},
		{
			// The model does not say what note is, so the database decides,
			// as it does for a comparison.
			name:  "an untyped property",
			query: "MATCH (l:Line) RETURN l.note + 1 AS x",
			want:  "SELECT t0.`f_note` + 1 AS `x` FROM {{.res_line}} t0",
		},
		{
			name:  "an aggregate over a computed value",
			query: "MATCH (l:Line) RETURN l.name AS name, sum(l.price * l.quantity) AS revenue",
			want: "SELECT t0.`f_name` AS `name`, SUM(t0.`f_price` * t0.`f_qty`) AS `revenue` " +
				"FROM {{.res_line}} t0 GROUP BY t0.`f_name`",
		},
		{
			name:  "an unaliased aggregate over a computed value",
			query: "MATCH (l:Line) RETURN sum(l.price * l.quantity)",
			want:  "SELECT SUM(t0.`f_price` * t0.`f_qty`) AS `sum(l.price * l.quantity)` FROM {{.res_line}} t0",
		},
		{
			name:  "a computed column is a grouping key",
			query: "MATCH (l:Line) RETURN l.quantity * 2 AS q, count(*) AS n",
			want: "SELECT t0.`f_qty` * 2 AS `q`, COUNT(*) AS `n` FROM {{.res_line}} t0 " +
				"GROUP BY t0.`f_qty` * 2",
		},
		{
			name:  "a property outside an aggregate that is also returned",
			query: "MATCH (l:Line) RETURN l.quantity AS q, l.quantity * count(*) AS units",
			want: "SELECT t0.`f_qty` AS `q`, t0.`f_qty` * COUNT(*) AS `units` FROM {{.res_line}} t0 " +
				"GROUP BY t0.`f_qty`",
		},
		{
			name:  "sorted by the name of a computed column",
			query: "MATCH (l:Line) RETURN l.name AS name, sum(l.price) * 2 AS x ORDER BY x DESC",
			want: "SELECT t0.`f_name` AS `name`, SUM(t0.`f_price`) * 2 AS `x` FROM {{.res_line}} t0 " +
				"GROUP BY t0.`f_name` ORDER BY `x` DESC",
		},
		{
			name:  "sorted by an aggregate over a computed value",
			query: "MATCH (l:Line) RETURN l.name AS name, sum(l.price * l.quantity) AS r ORDER BY sum(l.price * l.quantity) DESC",
			want: "SELECT t0.`f_name` AS `name`, SUM(t0.`f_price` * t0.`f_qty`) AS `r` FROM {{.res_line}} t0 " +
				"GROUP BY t0.`f_name` ORDER BY SUM(t0.`f_price` * t0.`f_qty`) DESC",
		},
		{
			name:  "a parenthesized property is the property",
			query: "MATCH (l:Line) RETURN (l.name)",
			want:  "SELECT t0.`f_name` AS `(l.name)` FROM {{.res_line}} t0",
		},
		{
			name:  "a computed condition",
			query: "MATCH (l:Line) WHERE l.price * 2 > 10 RETURN l.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 WHERE t0.`f_price` * 2 > 10",
		},
		{
			name:       "a computed condition against a parameter",
			query:      "MATCH (l:Line) WHERE l.price * l.quantity >= $floor AND l.id > 0 RETURN l.id AS id",
			parameters: map[string]any{"floor": 100},
			want: "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 " +
				"WHERE t0.`f_price` * t0.`f_qty` >= 100 AND t0.`f_id` > 0",
		},
		{
			name:  "arithmetic on both sides",
			query: "MATCH (l:Line) WHERE l.quantity + 1 = l.id RETURN l.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 WHERE t0.`f_qty` + 1 = t0.`f_id`",
		},
		{
			name:       "a negated parameter",
			query:      "MATCH (l:Line) WHERE l.price > -$floor RETURN l.id AS id",
			parameters: map[string]any{"floor": 10},
			want:       "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 WHERE t0.`f_price` > -(10)",
		},
		{
			// Parentheses around a property alone leave the plain comparison.
			name:  "a parenthesized property in a condition",
			query: "MATCH (l:Line) WHERE (l.price) > 3 RETURN l.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 WHERE t0.`f_price` > 3",
		},
		{
			name:  "sorted by a computed value",
			query: "MATCH (l:Line) RETURN l.id AS id ORDER BY l.price * l.quantity DESC",
			want:  "SELECT t0.`f_id` AS `id` FROM {{.res_line}} t0 ORDER BY t0.`f_price` * t0.`f_qty` DESC",
		},
		{
			// The weighted ranking from the report: an aggregate and a
			// returned property, weighted and summed.
			name: "sorted by a weighted score",
			query: "MATCH (l:Line) RETURN l.name AS name, l.quantity AS q, count(l.id) AS n " +
				"ORDER BY count(l.id) * 0.5 + l.quantity * 0.3 DESC, name",
			want: "SELECT t0.`f_name` AS `name`, t0.`f_qty` AS `q`, COUNT(t0.`f_id`) AS `n` FROM {{.res_line}} t0 " +
				"GROUP BY t0.`f_name`, t0.`f_qty` ORDER BY COUNT(t0.`f_id`) * 5E-01 + t0.`f_qty` * 3E-01 DESC, `name`",
		},
		{
			name:  "distinct computed values",
			query: "MATCH (l:Line) RETURN DISTINCT l.quantity * 10 AS bucket",
			want:  "SELECT DISTINCT t0.`f_qty` * 10 AS `bucket` FROM {{.res_line}} t0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got, err := compileArithmetic(t, tc.query, tc.parameters)
			if err != nil {
				t.Fatalf("compile(%q): %v", tc.query, err)
			}
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// What Cypher and SQL would compute differently is refused by name rather
// than given either answer.
func TestCompileArithmeticRejections(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{
			name:  "a string property",
			query: "MATCH (l:Line) RETURN l.name + 1 AS x",
			want:  "arithmetic takes numbers; l.name is string",
		},
		{
			name:  "an aggregate over a string property",
			query: "MATCH (l:Line) RETURN min(l.name) + 1 AS x",
			want:  "arithmetic takes numbers",
		},
		{
			// Cypher gives 3 for 7 / 2, MySQL 3.5, PostgreSQL 3.
			name:  "integer division",
			query: "MATCH (l:Line) RETURN l.quantity / 2 AS x",
			want:  "dividing values that may both be integers",
		},
		{
			name:  "a ratio of counts",
			query: "MATCH (l:Line) RETURN count(*) / count(l.id) AS x",
			want:  "multiply one side by 1.0",
		},
		{
			name:  "dividing an untyped property",
			query: "MATCH (l:Line) RETURN l.note / 2 AS x",
			want:  "dividing values that may both be integers",
		},
		{
			name:  "remainder of a float",
			query: "MATCH (l:Line) RETURN l.price % 2 AS x",
			want:  "% takes integers",
		},
		{
			name:  "division by a literal zero",
			query: "MATCH (l:Line) RETURN l.price / 0.0 AS x",
			want:  "division by zero",
		},
		{
			name:  "a string literal",
			query: "MATCH (l:Line) RETURN l.quantity + 'a' AS x",
			want:  "arithmetic on a non-numeric value",
		},
		{
			name:  "null",
			query: "MATCH (l:Line) RETURN l.quantity + null AS x",
			want:  "arithmetic on null",
		},
		{
			name:       "a string parameter",
			query:      "MATCH (l:Line) RETURN l.price * $rate AS x",
			parameters: map[string]any{"rate": "x"},
			want:       `parameter "rate" is used in arithmetic`,
		},
		{
			name:  "a constant",
			query: "MATCH (l:Line) RETURN 1 + 2 AS x",
			want:  "returning a constant",
		},
		{
			name:  "a property outside an aggregate that is not returned",
			query: "MATCH (l:Line) RETURN l.price - avg(l.price) AS spread",
			want:  "reads price outside an aggregate",
		},
		{
			name:  "an aggregate inside an aggregate",
			query: "MATCH (l:Line) RETURN sum(count(*) + 1) AS x",
			want:  "an aggregate inside an aggregate",
		},
		{
			name:  "an aggregate over a constant",
			query: "MATCH (l:Line) RETURN sum(1 + 2) AS x",
			want:  "an aggregate over a constant",
		},
		{
			name: "a weighted score over a property the query does not return",
			query: "MATCH (l:Line) RETURN l.name AS name, count(l.id) AS n " +
				"ORDER BY count(l.id) * 0.5 + l.quantity * 0.3 DESC",
			want: "reads quantity outside an aggregate",
		},
		{
			name:  "a computed sort key under DISTINCT",
			query: "MATCH (l:Line) RETURN DISTINCT l.name AS name ORDER BY l.price * 2",
			want:  "a DISTINCT result cannot be sorted by a computed value",
		},
		{
			name:  "sorting by a computed aggregate without aggregating",
			query: "MATCH (l:Line) RETURN l.id AS id ORDER BY count(*) * 2",
			want:  "needs the query to return an aggregate too",
		},
		{
			name:  "sorting by a constant",
			query: "MATCH (l:Line) RETURN l.id AS id ORDER BY 1 + 1",
			want:  "sorting by a constant",
		},
		{
			name:  "an aggregate in a condition",
			query: "MATCH (l:Line) WHERE count(*) * 2 > 1 RETURN l.id",
			want:  "an aggregate in a condition",
		},
		{
			name:  "a computed number against a string",
			query: "MATCH (l:Line) WHERE l.price * 2 > 'x' RETURN l.id",
			want:  "comparing a computed number with string",
		},
		{
			name:  "a computed number against null",
			query: "MATCH (l:Line) WHERE l.price * 2 = null RETURN l.id",
			want:  "comparing against null",
		},
		{
			name:  "a condition over constants",
			query: "MATCH (l:Line) WHERE 1 + 1 > 1 RETURN l.id",
			want:  "a condition over constants",
		},
		{
			name:  "a string property in a condition",
			query: "MATCH (l:Line) WHERE l.name * 2 > 1 RETURN l.id",
			want:  "arithmetic takes numbers; l.name is string",
		},
		{
			name:  "arithmetic inside IN",
			query: "MATCH (l:Line) WHERE l.price * 2 IN [1, 2] RETURN l.id",
			want:  "arithmetic here",
		},
		{
			name:  "exponentiation",
			query: "MATCH (l:Line) RETURN l.price ^ 2 AS x",
			want:  "exponentiation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, sql, err := compileArithmetic(t, tc.query, tc.parameters)
			if err == nil {
				t.Fatalf("compile(%q) = %s, want a rejection mentioning %q", tc.query, sql, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}

// A property read inside a computation is read all the same, so it has to be
// in the list the caller's access is checked against. Missing it would let a
// masked property through as sum(l.secret * 1).
func TestCompileArithmeticNamesEveryPropertyItReads(t *testing.T) {
	plan, _, err := compileArithmetic(t,
		"MATCH (l:Line) RETURN sum(l.price * l.quantity) - count(l.discount) AS x", nil)
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{}
	for _, property := range plan.Properties {
		read[property.Property] = true
	}
	for _, want := range []string{"price", "quantity", "discount"} {
		if !read[want] {
			t.Fatalf("plan.Properties = %+v, missing %q", plan.Properties, want)
		}
	}
}

// OPTIONAL MATCH decides what a condition may name by the properties it reads,
// so a computed condition has to report its properties like any other: one
// over the optional node belongs to the clause, and one in a later WHERE that
// requires the optional node has to be refused.
func TestOptionalMatchSeesThePropertiesOfAComputedCondition(t *testing.T) {
	if _, err := analyze(t, "MATCH (o:Order) OPTIONAL MATCH (o)-[:PLACED_BY]->(c:Customer) "+
		"WHERE c.score + 1 > 2 RETURN o.id"); err != nil {
		t.Fatalf("a computed condition over the optional node was refused: %v", err)
	}
	_, err := analyze(t, "MATCH (o:Order) OPTIONAL MATCH (o)-[:PLACED_BY]->(c:Customer) "+
		"WHERE o.amount + 1 > 2 RETURN o.id")
	if err == nil || !strings.Contains(err.Error(), "names nothing it introduces") {
		t.Fatalf("err = %v, want a condition naming only o refused", err)
	}
	_, err = analyze(t, "MATCH (o:Order) OPTIONAL MATCH (o)-[:PLACED_BY]->(c:Customer) "+
		"MATCH (o)-[:FOLLOWS]->(p:Order) WHERE c.score * 2 > 1 RETURN o.id")
	if err == nil || !strings.Contains(err.Error(), "requiring what an OPTIONAL MATCH introduced") {
		t.Fatalf("err = %v, want a later WHERE over c refused", err)
	}
}

// A computed condition reads properties too, and they have to be checked.
func TestComputedConditionNamesItsProperties(t *testing.T) {
	plan, _, err := compileArithmetic(t, "MATCH (l:Line) WHERE l.price * l.discount > 1 RETURN l.id AS id", nil)
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{}
	for _, property := range plan.Properties {
		read[property.Property] = true
	}
	if !read["price"] || !read["discount"] {
		t.Fatalf("plan.Properties = %+v, want price and discount", plan.Properties)
	}
}

func TestSemanticQueryDescriptorRecordsComputedConditionsAndSortKeys(t *testing.T) {
	query := "MATCH (l:Line) WHERE l.price * l.quantity >= $floor RETURN l.id AS id ORDER BY l.discount * 2 DESC"
	plan, _, err := compileArithmetic(t, query, map[string]any{"floor": 100})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Predicates) != 1 {
		t.Fatalf("predicates = %+v", descriptor.Predicates)
	}
	predicate := descriptor.Predicates[0]
	if predicate.Expression != "arithmetic" || predicate.Operator != ">=" ||
		predicate.InputPointer != "$.parameters.floor" || len(predicate.ValueHashes) != 1 ||
		strings.Join(predicate.PropertyRefs, ",") != "property:kn_test:ot_line:price,property:kn_test:ot_line:quantity" {
		t.Fatalf("predicate = %+v", predicate)
	}
	if len(descriptor.Ordering) != 1 || descriptor.Ordering[0].Expression != "arithmetic" ||
		strings.Join(descriptor.Ordering[0].PropertyRefs, ",") != "property:kn_test:ot_line:discount" {
		t.Fatalf("ordering = %+v", descriptor.Ordering)
	}
}

func TestSemanticQueryDescriptorRecordsComputedColumns(t *testing.T) {
	query := "MATCH (l:Line) RETURN l.quantity * 2 AS q, sum(l.price * l.quantity) AS revenue " +
		"ORDER BY sum(l.price * l.quantity) DESC"
	plan, _, err := compileArithmetic(t, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	price, quantity := "property:kn_test:ot_line:price", "property:kn_test:ot_line:quantity"

	computed := descriptor.Projections[0]
	if computed.Expression != "arithmetic" || computed.PropertyRef != "" || computed.Aggregate != "" ||
		strings.Join(computed.PropertyRefs, ",") != quantity {
		t.Fatalf("computed projection = %+v", computed)
	}
	revenue := descriptor.Projections[1]
	if revenue.Expression != "arithmetic" || revenue.Aggregate != "sum" ||
		strings.Join(revenue.PropertyRefs, ",") != price+","+quantity {
		t.Fatalf("aggregate projection = %+v", revenue)
	}
	if strings.Join(descriptor.Grouping, ",") != quantity {
		t.Fatalf("grouping = %v", descriptor.Grouping)
	}
	if len(descriptor.Ordering) != 1 || descriptor.Ordering[0].Aggregate != "sum" ||
		strings.Join(descriptor.Ordering[0].PropertyRefs, ",") != price+","+quantity {
		t.Fatalf("ordering = %+v", descriptor.Ordering)
	}
}
