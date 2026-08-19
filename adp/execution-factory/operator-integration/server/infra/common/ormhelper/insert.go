package ormhelper

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// InsertBuilder INSERT statement builder.
type InsertBuilder struct {
	db             *DB
	table          string
	columns        []string
	values         [][]interface{}
	data           map[string]interface{}
	onDuplicateKey map[string]interface{}
	ignore         bool
}

// Into specifies the table name.
func (i *InsertBuilder) Into(table string) *InsertBuilder {
	i.table = fmt.Sprintf("`%s`.`%s`", i.db.dbName, table)
	return i
}

// Values specifies a single row of data.
func (i *InsertBuilder) Values(data map[string]interface{}) *InsertBuilder {
	i.data = data
	return i
}

// BatchValues specifies multiple rows of data.
func (i *InsertBuilder) BatchValues(columns []string, values [][]interface{}) *InsertBuilder {
	i.columns = columns
	i.values = values
	return i
}

// OnDuplicateKeyUpdate duplicate key update.
func (i *InsertBuilder) OnDuplicateKeyUpdate(data map[string]interface{}) *InsertBuilder {
	i.onDuplicateKey = data
	return i
}

// Ignore INSERT IGNORE
func (i *InsertBuilder) Ignore() *InsertBuilder {
	i.ignore = true
	return i
}

// Build build SQL statement.
func (i *InsertBuilder) Build() (query string, args []interface{}) {
	insertType := "INSERT"
	if i.ignore {
		insertType = "INSERT IGNORE"
	}

	if i.data != nil {
		// Single row insert.
		fields := make([]string, 0, len(i.data))
		placeholders := make([]string, 0, len(i.data))
		args := make([]interface{}, 0, len(i.data))

		for field, value := range i.data {
			fields = append(fields, field)
			placeholders = append(placeholders, "?")
			args = append(args, value)
		}

		query = fmt.Sprintf("%s INTO %s (%s) VALUES (%s)",
			insertType,
			i.table,
			strings.Join(fields, ", "),
			strings.Join(placeholders, ", "))

		// ON DUPLICATE KEY UPDATE
		if i.onDuplicateKey != nil {
			updates := make([]string, 0, len(i.onDuplicateKey))
			for field, value := range i.onDuplicateKey {
				updates = append(updates, field+" = ?")
				args = append(args, value)
			}
			query += " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")
		}

		return query, args
	} else if len(i.values) > 0 {
		// Batch insert.
		placeholderGroups := make([]string, len(i.values))
		args := make([]interface{}, 0, len(i.values)*len(i.columns))

		for idx, row := range i.values {
			placeholders := strings.Repeat("?,", len(row))
			placeholders = placeholders[:len(placeholders)-1]
			placeholderGroups[idx] = "(" + placeholders + ")"
			args = append(args, row...)
		}

		query = fmt.Sprintf("%s INTO %s (%s) VALUES %s",
			insertType,
			i.table,
			strings.Join(i.columns, ", "),
			strings.Join(placeholderGroups, ", "))

		return query, args
	}

	return "", nil
}

// Execute executes the insert.
func (i *InsertBuilder) Execute(ctx context.Context) (sql.Result, error) {
	query, args := i.Build()
	return i.db.executor.ExecContext(ctx, query, args...)
}

// ExecuteAndReturnID executes the insertion and returns the last inserted ID.
// @deprecated Deprecated: Databases such as Renda Jiancang, Dameng, and TiDB do not support the use of LastInsertId.
func (i *InsertBuilder) ExecuteAndReturnID(ctx context.Context) (int64, error) {
	result, err := i.Execute(ctx)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ExecuteAndReturnAffected executes the insertion and returns the number of affected rows.
func (i *InsertBuilder) ExecuteAndReturnAffected(ctx context.Context) (int64, error) {
	result, err := i.Execute(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
