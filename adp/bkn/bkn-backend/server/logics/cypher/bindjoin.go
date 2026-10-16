// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The bind-join executor runs a compiled plan without SQL. It reads one table
// at a time through vega-backend's resource data API: the first table with the
// conditions the data source can evaluate, then each neighbour with its join
// keys restricted to the values already found (`key in (...)`, in batches),
// joining the rows in memory. It needs nothing the data API does not offer,
// reads tables in different catalogs as easily as in one, and pages through a
// table with a cursor rather than stopping at a row cap.
//
// What it reads is bounded by maxRows across every table. A query that would
// read more fails as a whole rather than answering from part of the data.

// dataReader reads one page of a resource through the data API.
type dataReader func(ctx context.Context, resourceID string,
	params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error)

// bindJoinLimits bounds one execution.
type bindJoinLimits struct {
	// batchSize is how many join keys one read carries in its `in` filter.
	batchSize int
	// pageSize is how many rows one cursor page asks for.
	pageSize int
	// maxRows bounds both the rows read from every table together and the
	// matched combinations held in memory.
	maxRows int
	// workers is how many batch reads run at once.
	workers int
}

var defaultBindJoinLimits = bindJoinLimits{batchSize: 500, pageSize: 5000, maxRows: 100000, workers: 4}

// cursorKeepAliveSec is the shortest cursor lifetime vega-backend accepts. A
// read follows its cursor straight through, so it never needs more.
const cursorKeepAliveSec = 60

// ReadBudgetError is a query that would read more rows than one execution may.
type ReadBudgetError struct {
	Limit int
}

func (e *ReadBudgetError) Error() string {
	return fmt.Sprintf("the query reads more than %d rows; narrow it with conditions on the nodes it matches", e.Limit)
}

// residual is a condition the executor evaluates itself, as soon as every
// table it reads is bound.
type residual struct {
	predicate PlanPredicate
	tables    []int
	applied   bool
}

type bindJoinExecutor struct {
	plan   *Plan
	schema *Schema
	read   dataReader
	limits bindJoinLimits

	// fields lists, per table, the columns read from it.
	fields [][]string
	// primaryKeys lists, per table, the columns that identify a row.
	primaryKeys [][]string
	// filters holds, per table, the conditions the data source evaluates.
	filters [][]pushedFilter
	// residuals are the conditions evaluated here.
	residuals []*residual
	// joinResiduals are an optional join's conditions that read beyond the
	// table it attaches, keyed by join.
	joinResiduals map[int][]PlanPredicate
	// nothing is set when a condition can never hold, so no row can match.
	nothing bool

	rowsRead atomic.Int64
}

func newBindJoinExecutor(plan *Plan, schema *Schema, read dataReader, limits bindJoinLimits) (*bindJoinExecutor, error) {
	e := &bindJoinExecutor{
		plan:          plan,
		schema:        schema,
		read:          read,
		limits:        limits,
		fields:        make([][]string, len(plan.Tables)),
		primaryKeys:   make([][]string, len(plan.Tables)),
		filters:       make([][]pushedFilter, len(plan.Tables)),
		joinResiduals: map[int][]PlanPredicate{},
	}
	if err := e.prepare(); err != nil {
		return nil, err
	}
	return e, nil
}

// prepare splits the conditions between the data source and the executor and
// works out which columns each table has to be read with.
func (e *bindJoinExecutor) prepare() error {
	needed := make([]map[string]bool, len(e.plan.Tables))
	for i := range needed {
		needed[i] = map[string]bool{}
	}
	need := func(table int, column string) {
		if table >= 0 && table < len(needed) && column != "" {
			needed[table][column] = true
		}
	}

	for i, table := range e.plan.Tables {
		objectType := e.schema.objectTypesByID[table.ObjectTypeID]
		if objectType == nil {
			return fmt.Errorf("plan table %d has unknown object type %q", i, table.ObjectTypeID)
		}
		for _, key := range objectType.PrimaryKeys {
			column, err := e.schema.Column(objectType, key)
			if err != nil {
				return err
			}
			e.primaryKeys[i] = append(e.primaryKeys[i], column)
			need(i, column)
		}
	}

	terms := conjuncts(e.plan.Where)
	for _, join := range e.plan.Joins {
		for _, reading := range join.Readings {
			for _, key := range reading {
				need(join.Left, key.LeftColumn)
				need(join.Right, key.RightColumn)
			}
		}
		if !join.Optional {
			// On an inner join a condition in ON and one in WHERE keep the same
			// rows, so they are handled together.
			for _, condition := range join.Conditions {
				terms = append(terms, conjuncts(condition)...)
			}
		}
	}

	for _, term := range terms {
		tables := predicateTables(term)
		walkPredicateColumns(term, need)
		if len(tables) == 1 && !e.plan.Tables[tables[0]].Optional {
			if pushed, ok := pushFilter(term); ok {
				e.filters[tables[0]] = append(e.filters[tables[0]], pushed)
				continue
			}
		}
		if len(tables) == 0 {
			if pushed, ok := pushFilter(term); ok && pushed.constant != nil {
				if !*pushed.constant {
					e.nothing = true
				}
				continue
			}
		}
		e.residuals = append(e.residuals, &residual{predicate: term, tables: tables})
	}

	for index, join := range e.plan.Joins {
		if !join.Optional {
			continue
		}
		for _, condition := range join.Conditions {
			for _, term := range conjuncts(condition) {
				tables := predicateTables(term)
				walkPredicateColumns(term, need)
				// A condition on the attached table alone narrows the rows it
				// can attach, which is exactly what reading it filtered does.
				if len(tables) == 1 && tables[0] == join.OptionalTable {
					if pushed, ok := pushFilter(term); ok {
						e.filters[join.OptionalTable] = append(e.filters[join.OptionalTable], pushed)
						continue
					}
				}
				e.joinResiduals[index] = append(e.joinResiduals[index], term)
			}
		}
	}

	// A required table whose own conditions can never hold leaves nothing to
	// match, so nothing is read at all.
	for i, table := range e.plan.Tables {
		if _, possible := andFilters(e.filters[i]); !possible && !table.Optional {
			e.nothing = true
		}
	}

	for _, column := range e.plan.Select {
		walkColumnColumns(column, need)
	}
	for _, column := range e.plan.GroupBy {
		walkColumnColumns(column, need)
	}
	for _, order := range e.plan.OrderBy {
		switch {
		case order.Expression != nil:
			walkExpressionColumns(order.Expression, need)
		case order.Aggregate != nil:
			walkExpressionColumns(*order.Aggregate, need)
		case order.Alias == "":
			need(order.Table, order.Column)
		}
	}

	for i := range needed {
		fields := make([]string, 0, len(needed[i]))
		for column := range needed[i] {
			fields = append(fields, column)
		}
		sort.Strings(fields)
		e.fields[i] = fields
	}
	return nil
}

func walkColumnColumns(column PlanColumn, visit func(table int, column string)) {
	switch {
	case column.Expression != nil:
		walkExpressionColumns(column.Expression, visit)
	case column.Aggregate != nil:
		walkExpressionColumns(*column.Aggregate, visit)
	default:
		visit(column.Table, column.Column)
	}
}

// run matches the pattern and returns the rows it matched, before any
// projection.
func (e *bindJoinExecutor) run(ctx context.Context) ([]binding, error) {
	if e.nothing {
		return nil, nil
	}

	bound := make([]bool, len(e.plan.Tables))
	usedJoin := make([]bool, len(e.plan.Joins))

	start := e.startTable()
	if start < 0 {
		return nil, fmt.Errorf("the plan has no table to start from")
	}
	rows, err := e.readTable(ctx, start, nil)
	if err != nil {
		return nil, err
	}
	bindings := make([]binding, 0, len(rows))
	for _, row := range rows {
		b := make(binding, len(e.plan.Tables))
		b[start] = row
		bindings = append(bindings, b)
	}
	bound[start] = true
	if bindings, err = e.applyResiduals(bindings, bound); err != nil {
		return nil, err
	}

	for {
		index, next := e.nextJoin(bound, usedJoin)
		if index < 0 {
			next = e.unboundRequiredTable(bound)
			if next < 0 {
				break
			}
			// Nothing joins the next table to what is bound: the pattern has
			// two parts, which match as a cross product.
			if bindings, err = e.crossJoin(ctx, bindings, next); err != nil {
				return nil, err
			}
		} else {
			usedJoin[index] = true
			if next < 0 {
				// Both ends are bound already: a cycle in the pattern, or a
				// relationship from a node to itself. The join is a condition.
				bindings = e.keepJoined(bindings, e.plan.Joins[index])
			} else if bindings, err = e.hop(ctx, bindings, index, next); err != nil {
				return nil, err
			}
		}
		if next >= 0 {
			bound[next] = true
		}
		if bindings, err = e.applyResiduals(bindings, bound); err != nil {
			return nil, err
		}
	}

	// OPTIONAL MATCH attaches in the order the query wrote it, so whatever an
	// optional table hangs off is bound before it.
	for index, join := range e.plan.Joins {
		if !join.Optional || usedJoin[index] {
			continue
		}
		table := join.OptionalTable
		if bound[table] {
			return nil, fmt.Errorf("optional join %d attaches table %d, which is already bound", index, table)
		}
		usedJoin[index] = true
		if bindings, err = e.hop(ctx, bindings, index, table); err != nil {
			return nil, err
		}
		bound[table] = true
		if bindings, err = e.applyResiduals(bindings, bound); err != nil {
			return nil, err
		}
	}
	for index, used := range usedJoin {
		if !used {
			return nil, fmt.Errorf("join %d attaches no table", index)
		}
	}
	return bindings, nil
}

// startTable picks the required table the data source can narrow the most,
// judged by the conditions pushed to it: an equality or IN first, then any
// condition, then the first table.
func (e *bindJoinExecutor) startTable() int {
	best, bestScore := -1, -1
	for i, table := range e.plan.Tables {
		if table.Optional {
			continue
		}
		if score := e.selectivity(i); score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func (e *bindJoinExecutor) selectivity(table int) int {
	score := 0
	for _, filter := range e.filters[table] {
		if filter.condition == nil {
			continue
		}
		score = max(score, 1)
		switch filter.condition["operation"] {
		case "==", "in":
			score = 2
		}
	}
	return score
}

// nextJoin finds an unused inner join with at least one end bound. It returns
// the table it attaches, or -1 when both ends are bound. A join whose new table
// carries conditions goes first, so the bindings shrink as early as they can.
func (e *bindJoinExecutor) nextJoin(bound, used []bool) (int, int) {
	bestJoin, bestTable, bestScore := -1, -1, -2
	for index, join := range e.plan.Joins {
		if used[index] || join.Optional {
			continue
		}
		left, right := bound[join.Left], bound[join.Right]
		switch {
		case left && right:
			return index, -1
		case left || right:
			next := join.Right
			if right {
				next = join.Left
			}
			if score := e.selectivity(next); score > bestScore {
				bestJoin, bestTable, bestScore = index, next, score
			}
		}
	}
	return bestJoin, bestTable
}

func (e *bindJoinExecutor) unboundRequiredTable(bound []bool) int {
	for i, table := range e.plan.Tables {
		if !table.Optional && !bound[i] {
			return i
		}
	}
	return -1
}

// readTable reads every row of a table that passes its pushed conditions and,
// when extra is set, that condition as well.
func (e *bindJoinExecutor) readTable(ctx context.Context, table int, extra map[string]any) ([]map[string]any, error) {
	pushed, possible := andFilters(e.filters[table])
	if !possible {
		return nil, nil
	}
	condition := pushed
	if extra != nil {
		if condition == nil {
			condition = extra
		} else {
			condition = map[string]any{"operation": "and", "sub_conditions": []map[string]any{condition, extra}}
		}
	}

	sortFields := e.primaryKeys[table]
	if len(sortFields) == 0 && len(e.fields[table]) > 0 {
		sortFields = e.fields[table][:1]
	}
	sortParams := make([]*interfaces.SortParams, 0, len(sortFields))
	for _, field := range sortFields {
		sortParams = append(sortParams, &interfaces.SortParams{Field: field, Direction: "asc"})
	}
	// The SQL path reads the data source itself, so this does too: a local
	// index may lag it, and may compare and match strings differently.
	ignoreLocalIndex := true
	params := &interfaces.ResourceDataQueryParams{
		FilterCondition:  condition,
		OutputFields:     e.fields[table],
		Sort:             sortParams,
		IgnoreLocalIndex: &ignoreLocalIndex,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode: "cursor", Limit: e.limits.pageSize, KeepAliveSec: cursorKeepAliveSec,
		},
	}

	var rows []map[string]any
	resourceID := e.plan.Tables[table].ResourceID
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := e.read(ctx, resourceID, params)
		if err != nil {
			return nil, err
		}
		if e.rowsRead.Add(int64(len(response.Entries))) > int64(e.limits.maxRows) {
			return nil, &ReadBudgetError{Limit: e.limits.maxRows}
		}
		rows = append(rows, response.Entries...)
		if len(response.Entries) == 0 || response.Paging == nil ||
			response.Paging.NextCursor == nil || *response.Paging.NextCursor == "" {
			return rows, nil
		}
		// A continuation carries the cursor alone; vega-backend refuses
		// anything else beside it.
		params = &interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Cursor: *response.Paging.NextCursor},
		}
	}
}

// joinSide is one reading of a join from the bound table's point of view: the
// columns the bound rows supply and the columns of the new table they meet.
type joinSide struct {
	bound []string
	next  []string
}

func (e *bindJoinExecutor) joinSides(join PlanJoin, boundTable int) []joinSide {
	sides := make([]joinSide, 0, len(join.Readings))
	for _, reading := range join.Readings {
		side := joinSide{}
		for _, key := range reading {
			if join.Left == boundTable {
				side.bound = append(side.bound, key.LeftColumn)
				side.next = append(side.next, key.RightColumn)
			} else {
				side.bound = append(side.bound, key.RightColumn)
				side.next = append(side.next, key.LeftColumn)
			}
		}
		sides = append(sides, side)
	}
	return sides
}

// hop attaches table next to every binding through join index, reading only
// the rows whose keys the bindings hold.
func (e *bindJoinExecutor) hop(ctx context.Context, bindings []binding, index, next int) ([]binding, error) {
	join := e.plan.Joins[index]
	boundTable := join.Left
	if boundTable == next {
		boundTable = join.Right
	}
	sides := e.joinSides(join, boundTable)
	if len(sides) > 1 && len(e.primaryKeys[next]) == 0 {
		return nil, fmt.Errorf("object type %q has no primary key, so an undirected relationship to it cannot be matched",
			e.plan.Tables[next].ObjectTypeID)
	}

	rows, err := e.readKeyed(ctx, bindings, boundTable, next, sides)
	if err != nil {
		return nil, err
	}

	indexes := make([]map[string][]int, len(sides))
	for s, side := range sides {
		indexes[s] = map[string][]int{}
		for r, row := range rows {
			if key, ok := tupleKey(row, side.next); ok {
				indexes[s][key] = append(indexes[s][key], r)
			}
		}
	}

	optional := join.Optional && join.OptionalTable == next
	conditions := e.joinResiduals[index]
	joined := make([]binding, 0, len(bindings))
	for _, b := range bindings {
		matched := false
		if b[boundTable] != nil {
			for _, r := range matchingRows(b[boundTable], sides, indexes) {
				candidate := extend(b, next, rows[r])
				if len(conditions) > 0 {
					keep, err := allHold(conditions, candidate)
					if err != nil {
						return nil, err
					}
					if !keep {
						continue
					}
				}
				matched = true
				joined = append(joined, candidate)
				if len(joined) > e.limits.maxRows {
					return nil, &ReadBudgetError{Limit: e.limits.maxRows}
				}
			}
		}
		if optional && !matched {
			joined = append(joined, extend(b, next, nil))
		}
	}
	return joined, nil
}

// matchingRows lists the rows of the new table a bound row joins to. With more
// than one reading -- an undirected relationship -- a row that matches several
// readings is listed once, as an OR in the join condition would.
func matchingRows(row map[string]any, sides []joinSide, indexes []map[string][]int) []int {
	if len(sides) == 1 {
		key, ok := tupleKey(row, sides[0].bound)
		if !ok {
			return nil
		}
		return indexes[0][key]
	}
	seen := map[int]bool{}
	var matches []int
	for s, side := range sides {
		key, ok := tupleKey(row, side.bound)
		if !ok {
			continue
		}
		for _, r := range indexes[s][key] {
			if !seen[r] {
				seen[r] = true
				matches = append(matches, r)
			}
		}
	}
	sort.Ints(matches)
	return matches
}

// readKeyed reads the rows of table next whose join keys appear among the
// bindings, in batches of batchSize keys run workers at a time. A row read by
// more than one batch -- possible with a composite key or an undirected
// relationship -- is kept once, by primary key.
func (e *bindJoinExecutor) readKeyed(ctx context.Context, bindings []binding, boundTable, next int,
	sides []joinSide) ([]map[string]any, error) {
	type batch struct {
		columns []string
		tuples  [][]any
	}
	var batches []batch
	for _, side := range sides {
		seen := map[string]bool{}
		var tuples [][]any
		for _, b := range bindings {
			row := b[boundTable]
			if row == nil {
				continue
			}
			values, ok := tupleValues(row, side.bound)
			if !ok {
				continue
			}
			key := canonicalTuple(values)
			if !seen[key] {
				seen[key] = true
				tuples = append(tuples, values)
			}
		}
		for from := 0; from < len(tuples); from += e.limits.batchSize {
			to := min(from+e.limits.batchSize, len(tuples))
			batches = append(batches, batch{columns: side.next, tuples: tuples[from:to]})
		}
	}
	if len(batches) == 0 {
		return nil, nil
	}

	// The first failure stops the other batches; it is the one reported, not
	// the cancellations it caused.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([][]map[string]any, len(batches))
	var (
		failure     error
		failureOnce sync.Once
		wg          sync.WaitGroup
	)
	slots := make(chan struct{}, max(e.limits.workers, 1))
	for i, bt := range batches {
		wg.Add(1)
		go func(i int, bt batch) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			condition, err := keyInCondition(bt.columns, bt.tuples)
			if err == nil {
				results[i], err = e.readTable(ctx, next, condition)
			}
			if err != nil {
				failureOnce.Do(func() {
					failure = err
					cancel()
				})
			}
		}(i, bt)
	}
	wg.Wait()
	if failure != nil {
		return nil, failure
	}

	if len(batches) == 1 {
		return results[0], nil
	}
	var rows []map[string]any
	keys := e.primaryKeys[next]
	seen := map[string]bool{}
	for _, part := range results {
		for _, row := range part {
			if len(keys) > 0 {
				identity, ok := tupleKey(row, keys)
				if ok {
					if seen[identity] {
						continue
					}
					seen[identity] = true
				}
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// crossJoin pairs every binding with every row of a table nothing joins to.
func (e *bindJoinExecutor) crossJoin(ctx context.Context, bindings []binding, next int) ([]binding, error) {
	rows, err := e.readTable(ctx, next, nil)
	if err != nil {
		return nil, err
	}
	if len(bindings)*len(rows) > e.limits.maxRows {
		return nil, &ReadBudgetError{Limit: e.limits.maxRows}
	}
	joined := make([]binding, 0, len(bindings)*len(rows))
	for _, b := range bindings {
		for _, row := range rows {
			joined = append(joined, extend(b, next, row))
		}
	}
	return joined, nil
}

// keepJoined keeps the bindings whose two bound rows a join connects.
func (e *bindJoinExecutor) keepJoined(bindings []binding, join PlanJoin) []binding {
	sides := e.joinSides(join, join.Left)
	kept := bindings[:0]
	for _, b := range bindings {
		left, right := b[join.Left], b[join.Right]
		if left == nil || right == nil {
			continue
		}
		for _, side := range sides {
			leftKey, leftOK := tupleKey(left, side.bound)
			rightKey, rightOK := tupleKey(right, side.next)
			if leftOK && rightOK && leftKey == rightKey {
				kept = append(kept, b)
				break
			}
		}
	}
	return kept
}

// applyResiduals evaluates every pending condition whose tables are all bound.
func (e *bindJoinExecutor) applyResiduals(bindings []binding, bound []bool) ([]binding, error) {
	var ready []PlanPredicate
	for _, r := range e.residuals {
		if r.applied {
			continue
		}
		complete := true
		for _, table := range r.tables {
			if !bound[table] {
				complete = false
				break
			}
		}
		if complete {
			r.applied = true
			ready = append(ready, r.predicate)
		}
	}
	if len(ready) == 0 {
		return bindings, nil
	}
	kept := bindings[:0]
	for _, b := range bindings {
		keep, err := allHold(ready, b)
		if err != nil {
			return nil, err
		}
		if keep {
			kept = append(kept, b)
		}
	}
	return kept, nil
}

// allHold reports whether every condition is true for a binding; unknown, as in
// WHERE, does not keep it.
func allHold(conditions []PlanPredicate, b binding) (bool, error) {
	for _, condition := range conditions {
		value, err := evaluatePredicate(condition, scope{row: b})
		if err != nil {
			return false, err
		}
		if value != truthTrue {
			return false, nil
		}
	}
	return true, nil
}

func extend(b binding, table int, row map[string]any) binding {
	extended := make(binding, len(b))
	copy(extended, b)
	extended[table] = row
	return extended
}

// tupleValues reads key columns from a row. ok is false when one is NULL,
// which joins to nothing.
func tupleValues(row map[string]any, columns []string) ([]any, bool) {
	values := make([]any, len(columns))
	for i, column := range columns {
		value := row[column]
		if value == nil {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}

func tupleKey(row map[string]any, columns []string) (string, bool) {
	values, ok := tupleValues(row, columns)
	if !ok {
		return "", false
	}
	return canonicalTuple(values), true
}

// runBindJoin executes a compiled plan through the bind-join executor and
// returns the result in the shape the raw query route gives.
func runBindJoin(ctx context.Context, plan *Plan, schema *Schema, read dataReader, limits bindJoinLimits,
	defaultLimit int64) ([]interfaces.RawQueryColumn, []map[string]any, error) {
	executor, err := newBindJoinExecutor(plan, schema, read, limits)
	if err != nil {
		return nil, nil, err
	}
	rows, err := executor.run(ctx)
	if err != nil {
		return nil, nil, err
	}
	entries, err := finishRows(plan, rows, defaultLimit)
	if err != nil {
		return nil, nil, err
	}
	return bindJoinColumns(plan, schema, entries), entries, nil
}

// bindJoinColumns describes the result columns. No statement reports them, so
// a column takes the type of the values it holds, which is what the data
// source stored whatever the model declares; a column that is NULL throughout
// falls back to what the model says.
func bindJoinColumns(plan *Plan, schema *Schema, entries []map[string]any) []interfaces.RawQueryColumn {
	columns := make([]interfaces.RawQueryColumn, 0, len(plan.Select))
	for _, column := range plan.Select {
		kind := ""
		for _, entry := range entries {
			if kind = valueType(entry[column.Alias]); kind != "" {
				break
			}
		}
		if kind == "" {
			kind = columnType(plan, schema, column)
		}
		columns = append(columns, interfaces.RawQueryColumn{Name: column.Alias, Type: kind})
	}
	return columns
}

// valueType names the type of one value, or "" for NULL.
func valueType(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		if n, ok := asNumber(v); ok {
			if n.integer {
				return "integer"
			}
			return "float"
		}
		return "string"
	}
}

func columnType(plan *Plan, schema *Schema, column PlanColumn) string {
	switch {
	case column.Aggregate != nil:
		return aggregateType(plan, schema, *column.Aggregate)
	case column.Expression != nil:
		return expressionType(plan, schema, column.Expression)
	default:
		return propertyType(plan, schema, column.Table, column.Property)
	}
}

func propertyType(plan *Plan, schema *Schema, table int, property string) string {
	if table >= 0 && table < len(plan.Tables) {
		if objectType := schema.objectTypesByID[plan.Tables[table].ObjectTypeID]; objectType != nil {
			for _, dp := range objectType.DataProperties {
				if dp != nil && dp.Name == property && dp.Type != "" {
					return dp.Type
				}
			}
		}
	}
	return "string"
}

func aggregateType(plan *Plan, schema *Schema, aggregate PlanAggregate) string {
	switch aggregate.Function {
	case "COUNT":
		return "integer"
	case "AVG":
		return "float"
	}
	over := "float"
	if aggregate.Argument != nil {
		over = expressionType(plan, schema, aggregate.Argument)
	} else if !aggregate.Star {
		over = propertyType(plan, schema, aggregate.Table, aggregate.Property)
	}
	if aggregate.Function == "SUM" && over != "integer" {
		return "float"
	}
	return over
}

// expressionType is "integer" when every part of a computation is a whole
// number and nothing in it divides, and "float" otherwise.
func expressionType(plan *Plan, schema *Schema, value PlanExpression) string {
	switch node := value.(type) {
	case PlanValueColumn:
		if propertyType(plan, schema, node.Table, node.Property) == "integer" {
			return "integer"
		}
		return "float"
	case PlanValueLiteral:
		if node.Value.Kind == LiteralInteger {
			return "integer"
		}
		return "float"
	case PlanAggregate:
		return aggregateType(plan, schema, node)
	case PlanNegative:
		return expressionType(plan, schema, node.Operand)
	case PlanArithmetic:
		if node.Operator == "/" {
			return "float"
		}
		if expressionType(plan, schema, node.Left) == "integer" && expressionType(plan, schema, node.Right) == "integer" {
			return "integer"
		}
		return "float"
	default:
		return "float"
	}
}
