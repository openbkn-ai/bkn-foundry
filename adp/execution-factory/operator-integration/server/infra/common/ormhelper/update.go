package ormhelper

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// UpdateBuilder UPDATE statement builder.
type UpdateBuilder struct {
	db    *DB
	table string
	sets  map[string]interface{}
	where *WhereBuilder
	limit int
}

// rawExpression raw expression.
type rawExpression struct {
	expr string
}

// Set sets the field value.
func (u *UpdateBuilder) Set(field string, value interface{}) *UpdateBuilder {
	if u.sets == nil {
		u.sets = make(map[string]interface{})
	}
	u.sets[field] = value
	return u
}

// SetData sets field values in batches.
func (u *UpdateBuilder) SetData(data map[string]interface{}) *UpdateBuilder {
	if u.sets == nil {
		u.sets = make(map[string]interface{})
	}
	for field, value := range data {
		u.sets[field] = value
	}
	return u
}

// SetRaw sets a raw SQL expression.
func (u *UpdateBuilder) SetRaw(field, expr string) *UpdateBuilder {
	if u.sets == nil {
		u.sets = make(map[string]interface{})
	}
	// Use special notation to indicate that this is a primitive expression.
	u.sets[field] = &rawExpression{expr: expr}
	return u
}

// Increment field auto-increment.
func (u *UpdateBuilder) Increment(field string, value interface{}) *UpdateBuilder {
	return u.SetRaw(field, fmt.Sprintf("%s + %v", field, value))
}

// Decrement field decrements.
func (u *UpdateBuilder) Decrement(field string, value interface{}) *UpdateBuilder {
	return u.SetRaw(field, fmt.Sprintf("%s - %v", field, value))
}

// Where Add WHERE condition.
func (u *UpdateBuilder) Where(field, op string, value interface{}) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.Condition(field, op, value)
	return u
}

// WhereEq abbreviation for equal condition.
func (u *UpdateBuilder) WhereEq(field string, value interface{}) *UpdateBuilder {
	return u.Where(field, "=", value)
}

// WhereNe is not equal to the condition.
func (u *UpdateBuilder) WhereNe(field string, value interface{}) *UpdateBuilder {
	return u.Where(field, "!=", value)
}

// WhereIn IN condition.
func (u *UpdateBuilder) WhereIn(field string, values ...interface{}) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.In(field, values...)
	return u
}

// WhereNotIn NOT IN condition.
func (u *UpdateBuilder) WhereNotIn(field string, values ...interface{}) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.NotIn(field, values...)
	return u
}

// WhereLike LIKE condition.
func (u *UpdateBuilder) WhereLike(field, pattern string) *UpdateBuilder {
	return u.Where(field, "LIKE", pattern)
}

// WhereBetween BETWEENCondition.
func (u *UpdateBuilder) WhereBetween(field string, start, end interface{}) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.Between(field, start, end)
	return u
}

// WhereNull IS NULL condition.
func (u *UpdateBuilder) WhereNull(field string) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.IsNull(field)
	return u
}

// WhereNotNull IS NOT NULL condition.
func (u *UpdateBuilder) WhereNotNull(field string) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.IsNotNull(field)
	return u
}

// And starts the AND condition group.
func (u *UpdateBuilder) And(fn func(*WhereBuilder)) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.And(fn)
	return u
}

// Or starts the OR condition group.
func (u *UpdateBuilder) Or(fn func(*WhereBuilder)) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.Or(fn)
	return u
}

// WhereRaw original WHERE condition.
func (u *UpdateBuilder) WhereRaw(condition string, args ...interface{}) *UpdateBuilder {
	if u.where == nil {
		u.where = NewWhere()
	}
	u.where.Raw(condition, args...)
	return u
}

// Limit limits the number of updates.
func (u *UpdateBuilder) Limit(limit int) *UpdateBuilder {
	u.limit = limit
	return u
}

// Build build SQL statement.
func (u *UpdateBuilder) Build() (query string, args []interface{}) {
	sets := make([]string, 0, len(u.sets))
	args = make([]interface{}, 0, len(u.sets))

	for field, value := range u.sets {
		if raw, ok := value.(*rawExpression); ok {
			// Raw expression, no placeholders required.
			sets = append(sets, field+" = "+raw.expr)
		} else {
			sets = append(sets, field+" = ?")
			args = append(args, value)
		}
	}

	query = fmt.Sprintf("UPDATE %s SET %s", u.table, strings.Join(sets, ", "))

	// WHERE condition.
	if u.where != nil {
		whereClause, whereArgs := u.where.Build()
		if whereClause != "" {
			query += " WHERE " + whereClause
			args = append(args, whereArgs...)
		}
	}

	// LIMIT
	if u.limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", u.limit)
	}

	return query, args
}

// Execute performs updates.
func (u *UpdateBuilder) Execute(ctx context.Context) (sql.Result, error) {
	query, args := u.Build()
	return u.db.executor.ExecContext(ctx, query, args...)
}

// ExecuteAndReturnAffected performs an update and returns the number of affected rows.
func (u *UpdateBuilder) ExecuteAndReturnAffected(ctx context.Context) (int64, error) {
	result, err := u.Execute(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
