package ormhelper

import (
	"fmt"
	"strings"
)

// WhereBuilder WHERE condition builder.
type WhereBuilder struct {
	conditions []string
	args       []interface{}
	operator   string // AND or OR.
}

// NewWhere creates a WHERE builder.
func NewWhere() *WhereBuilder {
	return &WhereBuilder{
		operator: "AND",
	}
}

// Condition Add condition.
func (w *WhereBuilder) Condition(field, op string, value interface{}) *WhereBuilder {
	w.conditions = append(w.conditions, fmt.Sprintf("%s %s ?", field, op))
	w.args = append(w.args, value)
	return w
}

// Eq equals condition.
func (w *WhereBuilder) Eq(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, "=", value)
}

// Ne is not equal to the condition.
func (w *WhereBuilder) Ne(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, "!=", value)
}

// Gt is greater than condition.
func (w *WhereBuilder) Gt(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, ">", value)
}

// Gte greater than or equal to condition.
func (w *WhereBuilder) Gte(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, ">=", value)
}

// Lt is less than condition.
func (w *WhereBuilder) Lt(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, "<", value)
}

// Lte less than or equal to condition.
func (w *WhereBuilder) Lte(field string, value interface{}) *WhereBuilder {
	return w.Condition(field, "<=", value)
}

// In IN condition.
func (w *WhereBuilder) In(field string, values ...interface{}) *WhereBuilder {
	if len(values) == 0 {
		return w
	}

	placeholders := strings.Repeat("?,", len(values))
	placeholders = placeholders[:len(placeholders)-1]

	w.conditions = append(w.conditions, fmt.Sprintf("%s IN (%s)", field, placeholders))
	w.args = append(w.args, values...)
	return w
}

// NotIn NOT IN condition.
func (w *WhereBuilder) NotIn(field string, values ...interface{}) *WhereBuilder {
	if len(values) == 0 {
		return w
	}

	placeholders := strings.Repeat("?,", len(values))
	placeholders = placeholders[:len(placeholders)-1]

	w.conditions = append(w.conditions, fmt.Sprintf("%s NOT IN (%s)", field, placeholders))
	w.args = append(w.args, values...)
	return w
}

// Like LIKE condition.
func (w *WhereBuilder) Like(field, pattern string) *WhereBuilder {
	return w.Condition(field, "LIKE", pattern)
}

// NotLike NOT LIKE condition.
func (w *WhereBuilder) NotLike(field, pattern string) *WhereBuilder {
	return w.Condition(field, "NOT LIKE", pattern)
}

// Between BETWEEN condition.
func (w *WhereBuilder) Between(field string, start, end interface{}) *WhereBuilder {
	w.conditions = append(w.conditions, fmt.Sprintf("%s BETWEEN ? AND ?", field))
	w.args = append(w.args, start, end)
	return w
}

// NotBetween NOT BETWEEN condition.
func (w *WhereBuilder) NotBetween(field string, start, end interface{}) *WhereBuilder {
	w.conditions = append(w.conditions, fmt.Sprintf("%s NOT BETWEEN ? AND ?", field))
	w.args = append(w.args, start, end)
	return w
}

// IsNull IS NULL condition.
func (w *WhereBuilder) IsNull(field string) *WhereBuilder {
	w.conditions = append(w.conditions, field+" IS NULL")
	return w
}

// IsNotNull IS NOT NULL condition.
func (w *WhereBuilder) IsNotNull(field string) *WhereBuilder {
	w.conditions = append(w.conditions, field+" IS NOT NULL")
	return w
}

// And Add AND condition group.
func (w *WhereBuilder) And(fn func(*WhereBuilder)) *WhereBuilder {
	subWhere := NewWhere()
	fn(subWhere)

	if len(subWhere.conditions) > 0 {
		subClause, subArgs := subWhere.Build()
		w.conditions = append(w.conditions, "("+subClause+")")
		w.args = append(w.args, subArgs...)
	}
	return w
}

// Or Add OR condition group.
func (w *WhereBuilder) Or(fn func(*WhereBuilder)) *WhereBuilder {
	subWhere := &WhereBuilder{operator: "OR"}
	fn(subWhere)

	if len(subWhere.conditions) > 0 {
		subClause, subArgs := subWhere.Build()
		w.conditions = append(w.conditions, "("+subClause+")")
		w.args = append(w.args, subArgs...)
	}
	return w
}

// Cursor cursor condition.
func (w *WhereBuilder) Cursor(field string, value interface{}, isAsc bool) *WhereBuilder {
	if isAsc {
		w.Gte(field, value)
	} else {
		w.Lt(field, value)
	}
	return w
}

// Raw original condition.
func (w *WhereBuilder) Raw(condition string, args ...interface{}) *WhereBuilder {
	w.conditions = append(w.conditions, condition)
	w.args = append(w.args, args...)
	return w
}

// Build build WHERE clause.
func (w *WhereBuilder) Build() (query string, args []interface{}) {
	if len(w.conditions) == 0 {
		return "", nil
	}
	query = strings.Join(w.conditions, " "+w.operator+" ")
	args = w.args
	return
}
