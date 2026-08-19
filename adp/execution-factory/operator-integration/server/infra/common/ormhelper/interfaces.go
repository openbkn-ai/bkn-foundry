package ormhelper

import (
	"context"
	"database/sql"
)

// Executor database executor interface.
// Compatible with all standard database interfaces such as *sql.DB, *sql.Tx, *sqlx.DB, *sqlx.Tx etc.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Scanner results scanner interface.
type Scanner interface {
	ScanOne(row *sql.Row, dest interface{}) error
	ScanOneWithColumns(row *sql.Row, dest interface{}, columns []string) error
	ScanMany(rows *sql.Rows, dest interface{}) error
}
