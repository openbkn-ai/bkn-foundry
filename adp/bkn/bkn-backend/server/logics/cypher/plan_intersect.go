// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

// PlanIntersection is a query that compares two collected lists; see
// Query.Intersection. Each of Lists selects its property once per matched
// row, as the column "value".
type PlanIntersection struct {
	Lists    [2]*Plan
	Distinct [2]bool
	Items    []PlanIntersectionItem
	// Filtered is the list whose elements the shared count counts, each as
	// often as the list holds it; the other list is only tested against.
	Filtered int
}

// PlanIntersectionItem is one size the statement returns: of List, or, when
// Common is set, of the elements of List the other list also holds.
type PlanIntersectionItem struct {
	List   int
	Common bool
	Alias  string
}

// compileIntersection plans each list as a plan of its own, as a semi-join's
// stage is; what either reads is part of what the query reads.
func compileIntersection(node *Intersection, schema *Schema, options CompileOptions) (*Plan, error) {
	if len(options.ObjectProperties) > 0 {
		return nil, planErrorf(Position{}, "object_properties names properties of returned nodes; this query returns only sizes")
	}
	intersection := &PlanIntersection{Distinct: node.Distinct}
	plan := &Plan{NetworkID: schema.KNID, Branch: schema.Branch, Intersection: intersection, SQLOnly: true}
	for i, list := range node.Lists {
		sub, err := Compile(list, schema, CompileOptions{Parameters: options.Parameters})
		if err != nil {
			return nil, err
		}
		plan.Properties = append(plan.Properties, sub.Properties...)
		intersection.Lists[i] = sub
	}
	types := [2]string{}
	for i, sub := range intersection.Lists {
		types[i] = listValueType(schema, sub)
	}
	if families := [2]string{valueFamily(types[0]), valueFamily(types[1])}; families[0] != "" && families[1] != "" &&
		families[0] != families[1] {
		return nil, planErrorf(node.Lists[1].Return[0].Pos, "comparing values of %s (%s) with values of %s (%s): the two are never equal",
			node.Lists[0].Return[0].Property, types[0], node.Lists[1].Return[0].Property, types[1])
	}
	counted := -1
	for _, item := range node.Items {
		if item.Common {
			// Without DISTINCT the two directions count differently, since
			// each counts the repeats of its own list; one statement
			// counts one of them.
			if counted >= 0 && counted != item.List && !(node.Distinct[0] && node.Distinct[1]) {
				return nil, planErrorf(item.Pos, "counting what the lists share both ways round; "+
					"count it one way, or collect both with DISTINCT")
			}
			if counted < 0 {
				counted = item.List
			}
		}
		intersection.Items = append(intersection.Items, PlanIntersectionItem{List: item.List, Common: item.Common, Alias: item.Alias})
	}
	if counted > 0 {
		intersection.Filtered = counted
	}
	return plan, nil
}

// listValueType is the declared type of the property a list collects, or
// empty when the model does not say.
func listValueType(schema *Schema, sub *Plan) string {
	value := sub.Select[0]
	objectType := schema.objectTypesByID[sub.Tables[value.Table].ObjectTypeID]
	if objectType == nil {
		return ""
	}
	for _, dp := range objectType.DataProperties {
		if dp != nil && dp.Name == value.Property {
			return dp.Type
		}
	}
	return ""
}

// generateIntersection writes the sizes of two lists and of what they share
// as one row. Each list is its stage's statement as a derived table, nulls
// left out as collect leaves them out:
//
//	SELECT COUNT(b.value), COUNT(a.value), MAX(c.n)
//	FROM (SELECT 1 AS one FROM (<second stage>) s LIMIT 1) e
//	LEFT JOIN (SELECT [DISTINCT] value FROM (<filtered list>) ...) a ON 1 = 1
//	LEFT JOIN (SELECT DISTINCT value FROM (<other list>) ...) b ON a.value = b.value
//	CROSS JOIN (SELECT COUNT([DISTINCT] value) AS n FROM (<other list>) ...) c
//	GROUP BY e.one
//
// e holds a row only when the second stage matched something: there the
// second WITH groups by the first list, so with no row to group it returns
// nothing, while an empty first stage still gives an empty list and a row.
// Grouping by e.one returns no row when e has none.
func generateIntersection(node *PlanIntersection, dialect Dialect) (string, error) {
	g := &generator{dialect: dialect}
	value := g.identifier("value")
	stages := [2]string{}
	for i, list := range node.Lists {
		sql, err := Generate(list, GenerateOptions{Dialect: dialect})
		if err != nil {
			return "", err
		}
		stages[i] = sql
	}
	values := func(list int, distinct bool) string {
		keyword := ""
		if distinct {
			keyword = "DISTINCT "
		}
		return "(SELECT " + keyword + "s." + value + " FROM (" + stages[list] + ") s WHERE s." + value + " IS NOT NULL)"
	}
	filtered, other := node.Filtered, 1-node.Filtered
	count := "COUNT(s." + value + ")"
	if node.Distinct[other] {
		count = "COUNT(DISTINCT s." + value + ")"
	}
	g.out.WriteString("SELECT ")
	for i, item := range node.Items {
		if i > 0 {
			g.out.WriteString(", ")
		}
		switch {
		case item.Common:
			g.out.WriteString("COUNT(b." + value + ")")
		case item.List == filtered:
			g.out.WriteString("COUNT(a." + value + ")")
		default:
			g.out.WriteString("MAX(c." + g.identifier("n") + ")")
		}
		g.out.WriteString(" AS " + g.identifier(item.Alias))
	}
	g.out.WriteString(" FROM (SELECT 1 AS " + g.identifier("one") + " FROM (" + stages[1] + ") s LIMIT 1) e")
	g.out.WriteString(" LEFT JOIN " + values(filtered, node.Distinct[filtered]) + " a ON 1 = 1")
	g.out.WriteString(" LEFT JOIN " + values(other, true) + " b ON a." + value + " = b." + value)
	g.out.WriteString(" CROSS JOIN (SELECT " + count + " AS " + g.identifier("n") + " FROM (" + stages[other] + ") s) c")
	g.out.WriteString(" GROUP BY e." + g.identifier("one"))
	if g.err != nil {
		return "", g.err
	}
	return g.out.String(), nil
}
