package ormhelper

import (
	"context"
	"database/sql"
	"fmt"
)

// DB ORM core class.
type DB struct {
	executor Executor
	scanner  Scanner
	dbName   string
	// Add raw executor reference for transaction support.
	originalExecutor Executor
}

// New Create ORM instance.
func New(executor Executor, dbName string) *DB {
	return &DB{
		executor:         executor,
		scanner:          NewScanner(),
		dbName:           dbName,
		originalExecutor: executor,
	}
}

// WithTx creates a new ORM instance using transactions (recommended way)
func (db *DB) WithTx(tx *sql.Tx) *DB {
	return &DB{
		executor:         tx,
		scanner:          db.scanner,
		dbName:           db.dbName,
		originalExecutor: db.originalExecutor,
	}
}

// SetExecutor sets the executor (compatible with existing code patterns)
func (db *DB) SetExecutor(ctx context.Context, tx *sql.Tx) *DB {
	if tx != nil {
		return db.WithTx(tx)
	}
	return db
}

// WithTxIfProvided Convenient method: automatically select the executor based on tx parameters.
func (db *DB) WithTxIfProvided(tx *sql.Tx) *DB {
	if tx != nil {
		return db.WithTx(tx)
	}
	return db
}

// Select SELECT statement entry.
func (db *DB) Select(columns ...string) *SelectBuilder {
	return &SelectBuilder{
		db:      db,
		columns: columns,
	}
}

// Insert INSERT statement entry.
func (db *DB) Insert() *InsertBuilder {
	return &InsertBuilder{
		db: db,
	}
}

// Update UPDATE statement entry.
func (db *DB) Update(table string) *UpdateBuilder {
	fullTableName := fmt.Sprintf("`%s`.`%s`", db.dbName, table)
	return &UpdateBuilder{
		db:    db,
		table: fullTableName,
	}
}

// Delete DELETE statement entry.
func (db *DB) Delete() *DeleteBuilder {
	return &DeleteBuilder{
		db: db,
	}
}

// GetExecutor gets the current executor (for native SQL)
func (db *DB) GetExecutor() Executor {
	return db.executor
}

// IsInTransaction checks whether it is in a transaction.
func (db *DB) IsInTransaction() bool {
	_, isTx := db.executor.(*sql.Tx)
	return isTx
}
