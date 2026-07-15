// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package table

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanRows(t *testing.T) {
	t.Run("scan rows", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}()

		mock.ExpectQuery("SELECT id, name FROM users").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, []byte("alice")).
				AddRow(2, "bob"))

		rows, err := db.Query("SELECT id, name FROM users")
		require.NoError(t, err)
		defer rows.Close()

		got, err := ScanRows(rows)

		require.NoError(t, err)
		assert.Equal(t, []string{"id", "name"}, got.Columns)
		assert.Equal(t, int64(2), got.Total)
		assert.Equal(t, []map[string]any{
			{"id": int64(1), "name": "alice"},
			{"id": int64(2), "name": "bob"},
		}, got.Rows)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("scan rows returns rows error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		}()

		mock.ExpectQuery("SELECT id FROM users").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).
				AddRow(1).
				RowError(0, errors.New("row failed")))

		rows, err := db.Query("SELECT id FROM users")
		require.NoError(t, err)
		defer rows.Close()

		got, err := ScanRows(rows)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "row failed")
	})
}

func TestConvertValue(t *testing.T) {
	t.Run("convert value", func(t *testing.T) {
		assert.Equal(t, "hello", convertValue([]byte("hello")))
		assert.Equal(t, 42, convertValue(42))
		assert.Nil(t, convertValue(nil))
	})
}
