// Package ormhelper provides a simple and efficient way to interact with databases in Go applications.
package ormhelper

import (
	"context"
	"database/sql"
	"fmt"
)

// DeleteBuilder DELETE statement builder.
type DeleteBuilder struct {
	db    *DB
	table string
	where *WhereBuilder
	limit int
}

// From specifies the table name.
func (d *DeleteBuilder) From(table string) *DeleteBuilder {
	d.table = fmt.Sprintf("`%s`.`%s`", d.db.dbName, table)
	return d
}

// Where Add WHERE condition.
func (d *DeleteBuilder) Where(field, op string, value interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.Condition(field, op, value)
	return d
}

// WhereEq abbreviation for equal condition.
func (d *DeleteBuilder) WhereEq(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, "=", value)
}

// WhereNe is not equal to the condition.
func (d *DeleteBuilder) WhereNe(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, "!=", value)
}

// WhereGt is greater than condition.
func (d *DeleteBuilder) WhereGt(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, ">", value)
}

// WhereGte is greater than or equal to the condition.
func (d *DeleteBuilder) WhereGte(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, ">=", value)
}

// WhereLt is less than condition.
func (d *DeleteBuilder) WhereLt(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, "<", value)
}

// WhereLte is less than or equal to the condition.
func (d *DeleteBuilder) WhereLte(field string, value interface{}) *DeleteBuilder {
	return d.Where(field, "<=", value)
}

// WhereIn IN condition.
func (d *DeleteBuilder) WhereIn(field string, values ...interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.In(field, values...)
	return d
}

// WhereNotIn NOT IN condition.
func (d *DeleteBuilder) WhereNotIn(field string, values ...interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.NotIn(field, values...)
	return d
}

// WhereLike LIKE condition.
func (d *DeleteBuilder) WhereLike(field, pattern string) *DeleteBuilder {
	return d.Where(field, "LIKE", pattern)
}

// WhereNotLike NOT LIKE condition.
func (d *DeleteBuilder) WhereNotLike(field, pattern string) *DeleteBuilder {
	return d.Where(field, "NOT LIKE", pattern)
}

// WhereBetween BETWEENCondition.
func (d *DeleteBuilder) WhereBetween(field string, start, end interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.Between(field, start, end)
	return d
}

// WhereNotBetween NOT BETWEEN condition.
func (d *DeleteBuilder) WhereNotBetween(field string, start, end interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.NotBetween(field, start, end)
	return d
}

// WhereNull IS NULL condition.
func (d *DeleteBuilder) WhereNull(field string) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.IsNull(field)
	return d
}

// WhereNotNull IS NOT NULL condition.
func (d *DeleteBuilder) WhereNotNull(field string) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.IsNotNull(field)
	return d
}

// And starts the AND condition group.
func (d *DeleteBuilder) And(fn func(*WhereBuilder)) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.And(fn)
	return d
}

// Or starts the OR condition group.
func (d *DeleteBuilder) Or(fn func(*WhereBuilder)) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.Or(fn)
	return d
}

// WhereRaw original WHERE condition.
func (d *DeleteBuilder) WhereRaw(condition string, args ...interface{}) *DeleteBuilder {
	if d.where == nil {
		d.where = NewWhere()
	}
	d.where.Raw(condition, args...)
	return d
}

// Limit limit the number of deletions.
func (d *DeleteBuilder) Limit(limit int) *DeleteBuilder {
	d.limit = limit
	return d
}

// Build build SQL statement.
func (d *DeleteBuilder) Build() (query string, args []interface{}) {
	query = fmt.Sprintf("DELETE FROM %s", d.table)

	// WHERE condition.
	if d.where != nil {
		whereClause, whereArgs := d.where.Build()
		if whereClause != "" {
			query += " WHERE " + whereClause
			args = append(args, whereArgs...)
		}
	}

	// LIMIT
	if d.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", d.limit)
	}

	return query, args
}

// Execute execute delete.
func (d *DeleteBuilder) Execute(ctx context.Context) (sql.Result, error) {
	query, args := d.Build()
	return d.db.executor.ExecContext(ctx, query, args...)
}

// ExecuteAndReturnAffected executes deletion and returns the number of affected rows.
func (d *DeleteBuilder) ExecuteAndReturnAffected(ctx context.Context) (int64, error) {
	result, err := d.Execute(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
