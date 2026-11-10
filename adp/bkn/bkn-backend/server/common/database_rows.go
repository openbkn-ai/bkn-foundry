// Copyright openbkn.ai

package common

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
)

// ErrRowNotFound reports that a write addressed to one row found no such row.
var ErrRowNotFound = errors.New("row not found")

// RowQueryer is the read side of *sql.DB and *sql.Tx.
type RowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// EnsureRowUpdated checks the result of an UPDATE addressed to the one row that key
// identifies. MySQL counts changed rows only, so zero also means the row exists and
// already holds the new values; an existence query on the same key tells the two
// apart. Run it on the transaction that ran the UPDATE.
func EnsureRowUpdated(ctx context.Context, q RowQueryer, result sql.Result, table string, key sq.Eq) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	query, args, err := sq.Select("1").From(table).Where(key).Limit(1).ToSql()
	if err != nil {
		return err
	}
	var one int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRowNotFound
		}
		return err
	}
	return nil
}
