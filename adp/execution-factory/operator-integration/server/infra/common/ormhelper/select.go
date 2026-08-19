package ormhelper

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
)

// SelectBuilder SELECT statement builder.
type SelectBuilder struct {
	db      *DB
	columns []string
	table   string
	joins   []string
	where   *WhereBuilder
	groupBy []string
	having  *WhereBuilder
	orderBy []string
	limit   int
	offset  int
}

// From specifies the table name.
func (s *SelectBuilder) From(table string) *SelectBuilder {
	s.table = fmt.Sprintf("`%s`.`%s`", s.db.dbName, table)
	return s
}

// Join inner join.
func (s *SelectBuilder) Join(table, condition string) *SelectBuilder {
	fullTable := fmt.Sprintf("`%s`.`%s`", s.db.dbName, table)
	s.joins = append(s.joins, fmt.Sprintf("JOIN %s ON %s", fullTable, condition))
	return s
}

// LeftJoin left join.
func (s *SelectBuilder) LeftJoin(table, condition string) *SelectBuilder {
	fullTable := fmt.Sprintf("`%s`.`%s`", s.db.dbName, table)
	s.joins = append(s.joins, fmt.Sprintf("LEFT JOIN %s ON %s", fullTable, condition))
	return s
}

// RightJoin right join.
func (s *SelectBuilder) RightJoin(table, condition string) *SelectBuilder {
	fullTable := fmt.Sprintf("`%s`.`%s`", s.db.dbName, table)
	s.joins = append(s.joins, fmt.Sprintf("RIGHT JOIN %s ON %s", fullTable, condition))
	return s
}

// InnerJoin inner connection (alias of Join)
func (s *SelectBuilder) InnerJoin(table, condition string) *SelectBuilder {
	return s.Join(table, condition)
}

// Where Add WHERE condition.
func (s *SelectBuilder) Where(field, op string, value interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.Condition(field, op, value)
	return s
}

// WhereEq abbreviation for equal condition.
func (s *SelectBuilder) WhereEq(field string, value interface{}) *SelectBuilder {
	return s.Where(field, "=", value)
}

// WhereNe is not equal to the condition.
func (s *SelectBuilder) WhereNe(field string, value interface{}) *SelectBuilder {
	return s.Where(field, "!=", value)
}

// WhereGt is greater than condition.
func (s *SelectBuilder) WhereGt(field string, value interface{}) *SelectBuilder {
	return s.Where(field, ">", value)
}

// WhereGte is greater than or equal to the condition.
func (s *SelectBuilder) WhereGte(field string, value interface{}) *SelectBuilder {
	return s.Where(field, ">=", value)
}

// WhereLt is less than condition.
func (s *SelectBuilder) WhereLt(field string, value interface{}) *SelectBuilder {
	return s.Where(field, "<", value)
}

// WhereLte is less than or equal to the condition.
func (s *SelectBuilder) WhereLte(field string, value interface{}) *SelectBuilder {
	return s.Where(field, "<=", value)
}

// WhereIn IN condition.
func (s *SelectBuilder) WhereIn(field string, values ...interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.In(field, values...)
	return s
}

// WhereNotIn NOT IN condition.
func (s *SelectBuilder) WhereNotIn(field string, values ...interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.NotIn(field, values...)
	return s
}

// WhereLike LIKE condition.
func (s *SelectBuilder) WhereLike(field, pattern string) *SelectBuilder {
	return s.Where(field, "LIKE", pattern)
}

// WhereNotLike NOT LIKE condition.
func (s *SelectBuilder) WhereNotLike(field, pattern string) *SelectBuilder {
	return s.Where(field, "NOT LIKE", pattern)
}

// WhereBetween BETWEENCondition.
func (s *SelectBuilder) WhereBetween(field string, start, end interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.Between(field, start, end)
	return s
}

// WhereNotBetween NOT BETWEEN condition.
func (s *SelectBuilder) WhereNotBetween(field string, start, end interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.NotBetween(field, start, end)
	return s
}

// WhereNull IS NULL condition.
func (s *SelectBuilder) WhereNull(field string) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.IsNull(field)
	return s
}

// WhereNotNull IS NOT NULL condition.
func (s *SelectBuilder) WhereNotNull(field string) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.IsNotNull(field)
	return s
}

// And starts the AND condition group.
func (s *SelectBuilder) And(fn func(*WhereBuilder)) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.And(fn)
	return s
}

// Or starts the OR condition group.
func (s *SelectBuilder) Or(fn func(*WhereBuilder)) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.Or(fn)
	return s
}

// WhereRaw original WHERE condition.
func (s *SelectBuilder) WhereRaw(condition string, args ...interface{}) *SelectBuilder {
	if s.where == nil {
		s.where = NewWhere()
	}
	s.where.Raw(condition, args...)
	return s
}

// GroupBy group.
func (s *SelectBuilder) GroupBy(columns ...string) *SelectBuilder {
	s.groupBy = append(s.groupBy, columns...)
	return s
}

// Having HAVING conditions.
func (s *SelectBuilder) Having(field, op string, value interface{}) *SelectBuilder {
	if s.having == nil {
		s.having = NewWhere()
	}
	s.having.Condition(field, op, value)
	return s
}

// HavingEq HAVING is equal to the condition.
func (s *SelectBuilder) HavingEq(field string, value interface{}) *SelectBuilder {
	return s.Having(field, "=", value)
}

// HavingGt HAVING greater than condition.
func (s *SelectBuilder) HavingGt(field string, value interface{}) *SelectBuilder {
	return s.Having(field, ">", value)
}

// HavingLt HAVING is less than condition.
func (s *SelectBuilder) HavingLt(field string, value interface{}) *SelectBuilder {
	return s.Having(field, "<", value)
}

// OrderBy sorting (ascending order)
func (s *SelectBuilder) OrderBy(column string) *SelectBuilder {
	s.orderBy = append(s.orderBy, column)
	return s
}

// OrderByDesc descending order.
func (s *SelectBuilder) OrderByDesc(column string) *SelectBuilder {
	s.orderBy = append(s.orderBy, column+" DESC")
	return s
}

// OrderByAsc ascending order (alias for OrderBy)
func (s *SelectBuilder) OrderByAsc(column string) *SelectBuilder {
	s.orderBy = append(s.orderBy, column+" ASC")
	return s
}

// Limit limit quantity.
func (s *SelectBuilder) Limit(limit int) *SelectBuilder {
	s.limit = limit
	return s
}

// Offset offset.
func (s *SelectBuilder) Offset(offset int) *SelectBuilder {
	s.offset = offset
	return s
}

// Pagination Apply pagination parameters.
func (s *SelectBuilder) Pagination(pagination *PaginationParams) *SelectBuilder {
	if pagination != nil && pagination.Page > 0 && pagination.PageSize > 0 {
		offset := (pagination.Page - 1) * pagination.PageSize
		s.Limit(pagination.PageSize).Offset(offset)
	}
	return s
}

// Sort applies sorting parameters.
func (s *SelectBuilder) Sort(sort *SortParams) *SelectBuilder {
	if sort != nil && len(sort.Fields) > 0 {
		for _, field := range sort.Fields {
			if field.Order.ToUpper() == SortOrderAsc {
				s.OrderByAsc(field.Field)
			} else {
				s.OrderByDesc(field.Field)
			}
		}
	}
	return s
}

// Cursor Apply Cursor Parameters.
func (s *SelectBuilder) Cursor(cursor *CursorParams) *SelectBuilder {
	if cursor != nil && cursor.Field != "" && cursor.Value != nil {
		if cursor.Direction.ToUpper() == SortOrderDesc {
			s.WhereLt(cursor.Field, cursor.Value)
		} else {
			s.WhereGt(cursor.Field, cursor.Value)
		}
	}
	return s
}

// Build build SQL statement.
func (s *SelectBuilder) Build() (query string, args []interface{}) {
	// Build the SELECT section.
	columns := "*"
	if len(s.columns) > 0 {
		columns = strings.Join(s.columns, ", ")
	}

	query = fmt.Sprintf("SELECT %s FROM %s", columns, s.table)

	// JOIN
	if len(s.joins) > 0 {
		query += " " + strings.Join(s.joins, " ")
	}

	// WHERE
	if s.where != nil {
		whereClause, whereArgs := s.where.Build()
		if whereClause != "" {
			query += " WHERE " + whereClause
			args = append(args, whereArgs...)
		}
	}

	// GROUP BY
	if len(s.groupBy) > 0 {
		query += " GROUP BY " + strings.Join(s.groupBy, ", ")
	}

	// HAVING
	if s.having != nil {
		havingClause, havingArgs := s.having.Build()
		if havingClause != "" {
			query += " HAVING " + havingClause
			args = append(args, havingArgs...)
		}
	}

	// ORDER BY
	if len(s.orderBy) > 0 {
		query += " ORDER BY " + strings.Join(s.orderBy, ", ")
	}

	// LIMIT
	if s.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", s.limit)
	}

	// OFFSET
	if s.offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", s.offset)
	}

	return query, args
}

// Execute execute query.
func (s *SelectBuilder) Execute(ctx context.Context) (*sql.Rows, error) {
	query, args := s.Build()
	return s.db.executor.QueryContext(ctx, query, args...)
}

// First Query the first record.
func (s *SelectBuilder) First(ctx context.Context, dest interface{}) error {
	s.Limit(1)

	// Use QueryContext instead of QueryRowContext to get column information.
	rows, err := s.Execute(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	// Check if there is data.
	if !rows.Next() {
		return sql.ErrNoRows
	}

	// Get column information.
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	// Field map scanning using reflection.
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr {
		return fmt.Errorf("dest must be a pointer")
	}

	destValue = destValue.Elem()
	if destValue.Kind() != reflect.Struct {
		return fmt.Errorf("dest must be a pointer to struct")
	}

	destType := destValue.Type()

	// Create field mapping.
	fieldMap := buildFieldMap(destType)

	// Prepare to scan target.
	scanTargets := prepareScanTargets(destValue, columns, fieldMap)

	// scan data.
	err = rows.Scan(scanTargets...)
	if err != nil {
		return err
	}

	// Check for errors during traversal.
	return rows.Err()
}

// Get queries multiple records.
func (s *SelectBuilder) Get(ctx context.Context, dest interface{}) error {
	rows, err := s.Execute(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()
	err = s.db.scanner.ScanMany(rows, dest)
	if err != nil {
		return err
	}
	return rows.Err()
}

// Count count number.
func (s *SelectBuilder) Count(ctx context.Context) (int64, error) {
	// Reconstruct the COUNT query.
	countBuilder := &SelectBuilder{
		db:      s.db,
		columns: []string{"COUNT(*)"},
		table:   s.table,
		joins:   s.joins,
		where:   s.where,
		groupBy: s.groupBy,
		having:  s.having,
	}

	query, args := countBuilder.Build()
	var count int64
	row := s.db.executor.QueryRowContext(ctx, query, args...)
	err := row.Scan(&count)
	return count, err
}

// Exists checks whether it exists.
func (s *SelectBuilder) Exists(ctx context.Context) (bool, error) {
	count, err := s.Count(ctx)
	return count > 0, err
}
