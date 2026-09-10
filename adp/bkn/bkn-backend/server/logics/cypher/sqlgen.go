// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strconv"
	"strings"
)

// Generation is the last stage and the only one that writes SQL text. Every
// name it prints was resolved by the planner against the model, and every
// value it prints is a decoded literal that is escaped here, once, for the
// dialect being written. Nothing from the request reaches the output without
// passing through one of those two paths.

// Dialect is the SQL dialect the generated statement is written in.
type Dialect string

const (
	// DialectMySQL is what we submit today. vega-backend parses the statement
	// in this dialect and translates it to whatever the resource's connector
	// speaks, which is also the path the platform's own SQL tool takes.
	DialectMySQL Dialect = "mysql"
	// DialectPostgres is generated but not submitted yet; it exists so that
	// writing the target dialect directly, and skipping the translation, is a
	// configuration change rather than a rewrite.
	DialectPostgres Dialect = "postgres"
)

// GenerateOptions carries the choices that are not the query's to make.
type GenerateOptions struct {
	Dialect Dialect
	// DefaultLimit caps a query that did not ask for a limit. Without it a
	// pattern over two large resources would stream an unbounded result
	// through the whole path.
	//
	// It also keeps ORDER BY meaningful: vega-backend runs this statement
	// inside a derived table, and MySQL is free to drop the ordering of one
	// that has no LIMIT. Removing the default would therefore cost sorted
	// results, not just a row cap.
	DefaultLimit int64
}

// Generate writes the plan as one SQL statement. Resources appear as
// {{.resource_id}} placeholders, which vega-backend replaces with the quoted
// physical table name, so no table name is ever composed here.
func Generate(plan *Plan, options GenerateOptions) (string, error) {
	dialect := options.Dialect
	if dialect == "" {
		dialect = DialectMySQL
	}
	if dialect != DialectMySQL && dialect != DialectPostgres {
		return "", fmt.Errorf("unsupported SQL dialect %q", dialect)
	}
	if len(plan.Tables) == 0 || len(plan.Select) == 0 {
		return "", fmt.Errorf("cannot generate SQL for an empty plan")
	}

	g := &generator{dialect: dialect, plan: plan}
	g.writeSelect()
	g.writeFrom()
	if err := g.writeWhere(); err != nil {
		return "", err
	}
	g.writeGroupBy()
	g.writeOrderBy()
	g.writeLimit(options.DefaultLimit)
	if g.err != nil {
		return "", g.err
	}
	return g.out.String(), nil
}

type generator struct {
	dialect Dialect
	plan    *Plan
	out     strings.Builder
	err     error
}

func (g *generator) writeSelect() {
	g.out.WriteString("SELECT ")
	if g.plan.Distinct {
		g.out.WriteString("DISTINCT ")
	}
	for i, column := range g.plan.Select {
		if i > 0 {
			g.out.WriteString(", ")
		}
		g.out.WriteString(g.selectExpression(column))
		g.out.WriteString(" AS ")
		g.out.WriteString(g.identifier(column.Alias))
	}
}

func (g *generator) selectExpression(column PlanColumn) string {
	if column.Aggregate != nil {
		return g.aggregate(*column.Aggregate)
	}
	return g.column(column.Table, column.Column)
}

// aggregate writes COUNT, SUM, AVG, MIN or MAX. The function name comes from a
// fixed set rather than from the query, so nothing the caller wrote reaches
// the statement as SQL.
func (g *generator) aggregate(aggregate PlanAggregate) string {
	if aggregate.Star {
		return aggregate.Function + "(*)"
	}
	inner := g.column(aggregate.Table, aggregate.Column)
	if aggregate.Distinct {
		inner = "DISTINCT " + inner
	}
	return aggregate.Function + "(" + inner + ")"
}

// writeGroupBy writes the grouping the planner derived from what is returned.
func (g *generator) writeGroupBy() {
	if len(g.plan.GroupBy) == 0 {
		return
	}
	g.out.WriteString(" GROUP BY ")
	for i, column := range g.plan.GroupBy {
		if i > 0 {
			g.out.WriteString(", ")
		}
		g.out.WriteString(g.column(column.Table, column.Column))
	}
}

func (g *generator) writeFrom() {
	g.out.WriteString(" FROM ")
	g.out.WriteString(g.table(0))

	joined := make([]bool, len(g.plan.Tables))
	joined[0] = true
	used := make([]bool, len(g.plan.Joins))

	// A pattern is a graph, not a chain, so the tables are emitted in an order
	// where each one joins to a table already in the statement. Anything left
	// over belongs to a shape the query never connected, and is written as a
	// cross join rather than silently dropped.
	for pending := len(g.plan.Tables) - 1; pending > 0; pending-- {
		next, join := g.nextJoinable(joined, used)
		if join < 0 {
			next = g.firstUnjoined(joined)
			g.out.WriteString(" CROSS JOIN ")
			g.out.WriteString(g.table(next))
			joined[next] = true
			continue
		}
		g.writeJoin(g.plan.Joins[join], next)
		joined[next] = true
		used[join] = true
	}

	// Joins between two tables that are both in already still have to be
	// written, or the condition they carry is lost.
	for i, join := range g.plan.Joins {
		if used[i] {
			continue
		}
		g.out.WriteString(" AND ")
		g.writeJoinCondition(join)
	}
}

// nextJoinable finds a table not yet in the statement that some unused join
// attaches to one that is.
func (g *generator) nextJoinable(joined, used []bool) (table int, join int) {
	for i, candidate := range g.plan.Joins {
		if used[i] {
			continue
		}
		if joined[candidate.Left] && !joined[candidate.Right] {
			return candidate.Right, i
		}
		if joined[candidate.Right] && !joined[candidate.Left] {
			return candidate.Left, i
		}
	}
	return -1, -1
}

func (g *generator) firstUnjoined(joined []bool) int {
	for i, in := range joined {
		if !in {
			return i
		}
	}
	return 0
}

func (g *generator) writeJoin(join PlanJoin, table int) {
	g.out.WriteString(" JOIN ")
	g.out.WriteString(g.table(table))
	g.out.WriteString(" ON ")
	g.writeJoinCondition(join)
}

func (g *generator) writeJoinCondition(join PlanJoin) {
	for r, reading := range join.Readings {
		if r > 0 {
			g.out.WriteString(" OR ")
		}
		// One reading needs no parentheses; several do, because they are
		// joined by OR and each is a conjunction of key pairs.
		if len(join.Readings) > 1 {
			g.out.WriteString("(")
		}
		for i, key := range reading {
			if i > 0 {
				g.out.WriteString(" AND ")
			}
			g.out.WriteString(g.column(join.Left, key.LeftColumn))
			g.out.WriteString(" = ")
			g.out.WriteString(g.column(join.Right, key.RightColumn))
		}
		if len(join.Readings) > 1 {
			g.out.WriteString(")")
		}
	}
}

func (g *generator) writeWhere() error {
	if g.plan.Where == nil {
		return nil
	}
	g.out.WriteString(" WHERE ")
	return g.writePredicate(g.plan.Where, false)
}

// writePredicate writes one node of the condition tree. Parentheses are added
// where an operator sits inside another one, rather than everywhere: a reader
// comparing the statement to the query should see the same shape.
func (g *generator) writePredicate(predicate PlanPredicate, nested bool) error {
	switch node := predicate.(type) {
	case PlanCondition:
		value, err := g.literal(node.Value)
		if err != nil {
			return err
		}
		g.out.WriteString(g.column(node.Table, node.Column))
		g.out.WriteString(" ")
		g.out.WriteString(node.Operator)
		g.out.WriteString(" ")
		g.out.WriteString(value)
		return nil

	case PlanColumnComparison:
		g.out.WriteString(g.column(node.LeftTable, node.LeftColumn))
		g.out.WriteString(" ")
		g.out.WriteString(node.Operator)
		g.out.WriteString(" ")
		g.out.WriteString(g.column(node.RightTable, node.RightColumn))
		return nil

	case PlanNullCheck:
		g.out.WriteString(g.column(node.Table, node.Column))
		if node.Negated {
			g.out.WriteString(" IS NOT NULL")
			return nil
		}
		g.out.WriteString(" IS NULL")
		return nil

	case PlanMembership:
		return g.writeMembership(node)

	case PlanNegation:
		g.out.WriteString("NOT ")
		return g.writePredicate(node.Operand, true)

	case PlanLogical:
		if nested {
			g.out.WriteString("(")
		}
		for i, operand := range node.Operands {
			if i > 0 {
				g.out.WriteString(" ")
				g.out.WriteString(node.Operator)
				g.out.WriteString(" ")
			}
			if err := g.writePredicate(operand, true); err != nil {
				return err
			}
		}
		if nested {
			g.out.WriteString(")")
		}
		return nil

	default:
		return fmt.Errorf("cannot generate a %T condition", predicate)
	}
}

func (g *generator) writeMembership(node PlanMembership) error {
	if len(node.Values) == 0 {
		// SQL has no empty IN list. Cypher says an empty list matches nothing,
		// so that is written out as a constant rather than left to the parser
		// of whichever database receives it.
		if node.Negated {
			g.out.WriteString("1 = 1")
			return nil
		}
		g.out.WriteString("1 = 0")
		return nil
	}

	g.out.WriteString(g.column(node.Table, node.Column))
	if node.Negated {
		g.out.WriteString(" NOT IN (")
	} else {
		g.out.WriteString(" IN (")
	}
	for i, value := range node.Values {
		if i > 0 {
			g.out.WriteString(", ")
		}
		written, err := g.literal(value)
		if err != nil {
			return err
		}
		g.out.WriteString(written)
	}
	g.out.WriteString(")")
	return nil
}

func (g *generator) writeOrderBy() {
	if len(g.plan.OrderBy) == 0 {
		return
	}
	g.out.WriteString(" ORDER BY ")
	for i, order := range g.plan.OrderBy {
		if i > 0 {
			g.out.WriteString(", ")
		}
		switch {
		case order.Aggregate != nil:
			g.out.WriteString(g.aggregate(*order.Aggregate))
		case order.Alias != "":
			// Sorting by the output name rather than by the expression: both
			// dialects accept it, and it keeps a grouped result from
			// computing the same aggregate twice.
			g.out.WriteString(g.identifier(order.Alias))
		default:
			g.out.WriteString(g.column(order.Table, order.Column))
		}
		if order.Descending {
			g.out.WriteString(" DESC")
		}
	}
}

func (g *generator) writeLimit(defaultLimit int64) {
	limit := g.plan.Limit
	if limit == nil && defaultLimit > 0 {
		limit = &defaultLimit
	}
	if limit != nil {
		g.out.WriteString(" LIMIT ")
		g.out.WriteString(strconv.FormatInt(*limit, 10))
	}
	if g.plan.Skip != nil {
		// MySQL only accepts OFFSET together with LIMIT. A plan that skips
		// without a limit therefore relies on one being injected, which the
		// service always does.
		if limit == nil {
			g.err = fmt.Errorf("SKIP requires a LIMIT")
			return
		}
		g.out.WriteString(" OFFSET ")
		g.out.WriteString(strconv.FormatInt(*g.plan.Skip, 10))
	}
}

// table writes the placeholder and its alias. The alias is generated (t0, t1),
// never taken from the query, so it cannot collide with anything the author
// wrote or carry anything the author controls.
func (g *generator) table(index int) string {
	t := g.plan.Tables[index]
	return "{{." + t.ResourceID + "}} " + t.Alias
}

func (g *generator) column(table int, column string) string {
	return g.plan.Tables[table].Alias + "." + g.identifier(column)
}

// identifier quotes a column or alias. Names here come from the model or from
// an AS in the query, and both can contain the quote character itself, so it
// is doubled the way each dialect expects.
func (g *generator) identifier(name string) string {
	if strings.ContainsRune(name, 0) {
		// A NUL would truncate the statement in the client libraries below,
		// and no real column is named this way.
		g.err = fmt.Errorf("identifier %q contains a null byte", name)
		return ""
	}
	if g.dialect == DialectPostgres {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (g *generator) literal(value Literal) (string, error) {
	switch value.Kind {
	case LiteralString:
		return g.stringLiteral(value.String)
	case LiteralInteger:
		return strconv.FormatInt(value.Integer, 10), nil
	case LiteralFloat:
		return strconv.FormatFloat(value.Float, 'g', -1, 64), nil
	case LiteralBoolean:
		if value.Boolean {
			return "TRUE", nil
		}
		return "FALSE", nil
	default:
		// The analyzer refuses null comparisons, so reaching here means the
		// stages disagree rather than that the query was unusual.
		return "", fmt.Errorf("cannot generate a %s literal", value.describe())
	}
}

// stringLiteral escapes a decoded Go string for the dialect. This is the one
// place a caller-supplied value becomes SQL text, so the rules are spelled out
// rather than delegated.
func (g *generator) stringLiteral(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("string literals must not contain a null byte")
	}
	if g.dialect == DialectPostgres {
		// With standard_conforming_strings on, which has been the default
		// since PostgreSQL 9.1, a backslash is an ordinary character and only
		// the quote is doubled.
		return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
	}
	// MySQL and MariaDB treat a backslash as an escape character inside a
	// string, so it has to be doubled before the quote is doubled. Both were
	// checked against a live MariaDB, including a trailing backslash, which is
	// the case that swallows the closing quote when it is missed.
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", "''")
	return "'" + escaped + "'", nil
}
