// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A query that returns graph values reads its rows with one SQL statement and
// only assembles the values here: a node from its key columns, a relationship
// from the key columns of its two ends. Joins, conditions, row filters,
// grouping and paging stay in the statement.
//
// Without collect, each statement row is one result row: graph values expand
// to the columns they are built from, in SELECT and, when the query
// aggregates, in GROUP BY.
//
// With collect, a result row is a conclusion -- the values beside collect --
// and its evidence is every matched row of that conclusion. The statement
// returns those matched rows flat, joined to a derived table of the
// conclusions that LIMIT keeps, so LIMIT counts conclusions and never cuts
// evidence. The rows are grouped into conclusions here.

// graphRowCap bounds the rows one graph read returns. It is the most one
// vega-backend raw query returns; a read that reaches it is refused rather
// than answered with part of its evidence.
const graphRowCap = interfaces.CYPHER_MAX_LIMIT

// graphRead is how a query that returns graph values reads its rows.
type graphRead struct {
	statement string
	rowLimit  int
	// columns are the statement columns graph values are built from.
	columns []graphReadColumn
	// evidence is set when RETURN collects: the statement returns matched
	// rows, which are grouped into conclusions after reading.
	evidence bool
	// conclusionLimit is how many conclusions LIMIT keeps.
	conclusionLimit int64
	// paged is set when a derived table chose and paged the conclusions;
	// otherwise SKIP and LIMIT apply after grouping.
	paged bool
	// flat and chosen are the plans of the statement and of its derived
	// table, which tests run to stand in for the database.
	flat, chosen *Plan
	// keys are the conclusion's columns: each table column, and its alias
	// in the derived table.
	keys []graphReadColumn
}

// graphReadColumn is one table column a graph read returns, under an alias of
// its own.
type graphReadColumn struct {
	alias  string
	table  int
	column string
}

// conclusionJoin keeps the rows whose key columns match a row of a derived
// table: the conclusions LIMIT kept.
type conclusionJoin struct {
	statement string
	keys      []graphReadColumn // the outer table column, and its alias in the derived table
}

// planGraphRead writes the statement for a plan that returns graph values.
func planGraphRead(plan *Plan, dialect Dialect, defaultLimit int64) (*graphRead, error) {
	collects := false
	for _, column := range plan.Select {
		if column.Graph != nil && planGraphCollects(column.Graph) {
			collects = true
		}
	}
	if collects {
		return planEvidenceRead(plan, dialect, defaultLimit)
	}
	return planRowRead(plan, dialect, defaultLimit)
}

// graphColumns lists, once each, the table columns a set of plan columns
// reads, each under a new alias.
type graphColumns struct {
	prefix  string
	columns []graphReadColumn
	seen    map[[2]string]int
}

func newGraphColumns(prefix string) *graphColumns {
	return &graphColumns{prefix: prefix, seen: map[[2]string]int{}}
}

func (c *graphColumns) add(table int, column string) graphReadColumn {
	key := [2]string{strconv.Itoa(table), column}
	if i, ok := c.seen[key]; ok {
		return c.columns[i]
	}
	read := graphReadColumn{alias: c.prefix + strconv.Itoa(len(c.columns)), table: table, column: column}
	c.seen[key] = len(c.columns)
	c.columns = append(c.columns, read)
	return read
}

func (c *graphColumns) addColumn(column PlanColumn) []graphReadColumn {
	var added []graphReadColumn
	walkColumnColumns(column, func(table int, name string) {
		added = append(added, c.add(table, name))
	})
	return added
}

func (c *graphColumns) planColumns() []PlanColumn {
	columns := make([]PlanColumn, 0, len(c.columns))
	for _, read := range c.columns {
		columns = append(columns, PlanColumn{Table: read.table, Column: read.column, Alias: read.alias})
	}
	return columns
}

// planRowRead reads a query without collect: the plan's statement with each
// graph value replaced by the columns it is built from.
func planRowRead(plan *Plan, dialect Dialect, defaultLimit int64) (*graphRead, error) {
	columns := newGraphColumns("_graph_")
	flat := *plan
	flat.Select = nil
	for _, column := range plan.Select {
		if column.Graph == nil {
			flat.Select = append(flat.Select, column)
		}
	}
	for _, column := range plan.Select {
		if column.Graph != nil {
			columns.addColumn(column)
		}
	}
	flat.Select = append(flat.Select, columns.planColumns()...)

	// A graph value grouped by groups by the columns it is built from.
	flat.GroupBy = nil
	for _, column := range plan.GroupBy {
		if column.Graph == nil {
			flat.GroupBy = append(flat.GroupBy, column)
			continue
		}
		for _, read := range columns.addColumn(column) {
			key := PlanColumn{Table: read.table, Column: read.column, Alias: read.alias}
			if !hasColumn(flat.GroupBy, key) {
				flat.GroupBy = append(flat.GroupBy, key)
			}
		}
	}
	flat.OrderBy = withoutGraphSorts(plan)
	if len(columns.columns) == 0 && len(flat.Select) == 0 {
		return nil, errReadsNothing
	}

	statement, err := Generate(&flat, GenerateOptions{Dialect: dialect, DefaultLimit: defaultLimit})
	if err != nil {
		return nil, err
	}
	return &graphRead{statement: statement, rowLimit: pageSize(plan.Limit), columns: columns.columns, flat: &flat}, nil
}

// planEvidenceRead reads a query with collect: the matched rows of the
// conclusions LIMIT keeps.
func planEvidenceRead(plan *Plan, dialect Dialect, defaultLimit int64) (*graphRead, error) {
	if plan.Grouped {
		return nil, planErrorf(Position{}, "collect after a WITH that aggregates is not supported yet; collect in a query of its own")
	}
	// The conclusion is every column beside collect. Each is a graph value
	// or a property, so it is a set of table columns the derived table can
	// return and the outer rows can be matched on.
	keys := newGraphColumns("_key_")
	var conclusionSelect []PlanColumn
	for _, column := range plan.Select {
		switch {
		case column.Graph != nil && planGraphCollects(column.Graph):
		case column.Graph != nil:
			keys.addColumn(column)
		case column.Expression == nil && column.Aggregate == nil:
			// A returned property is a key, and keeps its alias in the
			// derived table too, which ORDER BY may name.
			keys.add(column.Table, column.Column)
			conclusionSelect = append(conclusionSelect, column)
		default:
			return nil, planErrorf(Position{}, "beside collect, return nodes, relationships, properties or constants")
		}
	}
	for _, read := range keys.columns {
		if !hasAlias(conclusionSelect, read.alias) {
			conclusionSelect = append(conclusionSelect, PlanColumn{Table: read.table, Column: read.column, Alias: read.alias})
		}
	}
	for _, order := range plan.OrderBy {
		if order.Alias == "" || !hasAlias(conclusionSelect, order.Alias) {
			if order.Alias != "" && isGraphConstant(plan, order.Alias) {
				continue
			}
			return nil, planErrorf(Position{}, "beside collect, sort by the alias of a returned property")
		}
	}

	limit := defaultLimit
	if plan.Limit != nil {
		limit = *plan.Limit
	}
	var conclusions *conclusionJoin
	var chosenPlan *Plan
	if len(keys.columns) > 0 {
		chosen := *plan
		chosenPlan = &chosen
		chosen.Select = conclusionSelect
		chosen.Distinct = true
		chosen.GroupBy = nil
		chosen.OrderBy = withoutGraphSorts(plan)
		statement, err := Generate(&chosen, GenerateOptions{Dialect: dialect, DefaultLimit: defaultLimit})
		if err != nil {
			return nil, err
		}
		conclusions = &conclusionJoin{statement: statement, keys: keys.columns}
	}

	// The outer statement returns every column the result is built from,
	// unsorted and unlimited but for the row cap.
	columns := newGraphColumns("_graph_")
	for _, column := range plan.Select {
		columns.addColumn(column)
	}
	if len(columns.columns) == 0 {
		return nil, errReadsNothing
	}
	flat := *plan
	flat.Select = columns.planColumns()
	flat.Distinct = false
	flat.GroupBy = nil
	flat.OrderBy = nil
	flat.Skip = nil
	rowCap := int64(graphRowCap)
	flat.Limit = &rowCap
	statement, err := Generate(&flat, GenerateOptions{Dialect: dialect, conclusions: conclusions})
	if err != nil {
		return nil, err
	}
	return &graphRead{
		statement: statement, rowLimit: graphRowCap, columns: columns.columns,
		evidence: true, conclusionLimit: limit, paged: conclusions != nil,
		flat: &flat, chosen: chosenPlan, keys: keys.columns,
	}, nil
}

// errReadsNothing refuses a RETURN of only constants: no column of the match
// is read, so there is nothing to select. It was refused before graph values
// existed, and stays refused.
var errReadsNothing = planErrorf(Position{}, "RETURN reads nothing from the matched rows; "+
	"return a node, a relationship or a property beside constants")

func hasAlias(columns []PlanColumn, alias string) bool {
	for _, column := range columns {
		if column.Alias == alias {
			return true
		}
	}
	return false
}

func isGraphConstant(plan *Plan, alias string) bool {
	for _, column := range plan.Select {
		if column.Alias == alias {
			_, constant := column.Graph.(PlanGraphConstant)
			return constant
		}
	}
	return false
}

// withoutGraphSorts drops sorting by a constant graph value, which orders
// nothing and names no statement column.
func withoutGraphSorts(plan *Plan) []PlanOrder {
	var keys []PlanOrder
	for _, key := range plan.OrderBy {
		if key.Alias != "" && isGraphConstant(plan, key.Alias) {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// writeConclusions joins the derived table of chosen conclusions. A key may
// be NULL, a node an OPTIONAL MATCH did not find, and NULL matches NULL here.
func (g *generator) writeConclusions() {
	if g.conclusions == nil {
		return
	}
	g.out.WriteString(" JOIN (")
	g.out.WriteString(g.conclusions.statement)
	g.out.WriteString(") c0 ON ")
	for i, key := range g.conclusions.keys {
		if i > 0 {
			g.out.WriteString(" AND ")
		}
		outer := g.column(key.table, key.column)
		inner := "c0." + g.identifier(key.alias)
		g.out.WriteString("(" + outer + " = " + inner + " OR (" + outer + " IS NULL AND " + inner + " IS NULL))")
	}
}

// graphBindings turns statement rows back into matched rows, one table row
// per table the statement read. A table every column of which is NULL is a
// node an OPTIONAL MATCH did not find.
func graphBindings(plan *Plan, read *graphRead, entries []map[string]any) []binding {
	rows := make([]binding, 0, len(entries))
	for _, entry := range entries {
		row := make(binding, len(plan.Tables))
		found := make([]bool, len(plan.Tables))
		for _, column := range read.columns {
			if row[column.table] == nil {
				row[column.table] = map[string]any{}
			}
			value := entry[column.alias]
			row[column.table][column.column] = value
			if value != nil {
				found[column.table] = true
			}
		}
		for table := range row {
			if row[table] != nil && !found[table] && plan.Tables[table].Optional {
				row[table] = nil
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// assembleGraphRows builds the result of a graph read. It reports how many
// matched rows were read and whether LIMIT may have left conclusions out.
func assembleGraphRows(plan *Plan, read *graphRead, entries []map[string]any) ([]map[string]any, bool, error) {
	rows := graphBindings(plan, read, entries)
	if read.evidence {
		if !read.paged {
			// Nothing beside collect: one conclusion, which SKIP and
			// LIMIT apply to as they would to any result row.
			return finishRows(plan, rows, read.conclusionLimit, false)
		}
		// The statement chose and paged the conclusions; grouping them is
		// all that is left.
		grouped := *plan
		grouped.Skip, grouped.Limit = nil, nil
		result, _, err := finishRows(&grouped, rows, -1, false)
		if err != nil {
			return nil, false, err
		}
		return result, read.conclusionLimit >= 0 && int64(len(result)) >= read.conclusionLimit, nil
	}

	result := make([]map[string]any, 0, len(entries))
	for i, entry := range entries {
		out := make(map[string]any, len(plan.Select))
		for _, column := range plan.Select {
			if column.Graph == nil {
				out[column.Alias] = entry[column.Alias]
				continue
			}
			value, err := evaluateGraph(column.Graph, scope{row: rows[i], group: []binding{rows[i]}})
			if err != nil {
				return nil, false, err
			}
			out[column.Alias] = value
		}
		result = append(result, out)
	}
	return result, len(entries) >= read.rowLimit, nil
}
