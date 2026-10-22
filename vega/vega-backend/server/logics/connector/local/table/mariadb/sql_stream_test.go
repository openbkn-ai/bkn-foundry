// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestSQLResultStreamNext(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows          *sqlmock.Rows
		limit         *int64
		want          int
		kind          qm.CompletionKind
		failure       error
		decodeFailure bool
	}{
		{name: "empty is relation EOF", rows: sqlmock.NewRows([]string{"id"}), kind: qm.CompletionRelationEnd},
		{name: "exact page EOF", rows: sqlmock.NewRows([]string{"id"}).AddRow(1), limit: new(int64(1)), want: 1, kind: qm.CompletionRelationEnd},
		{name: "probe establishes next page", rows: sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2), limit: new(int64(1)), want: 1, kind: qm.CompletionWindowEnd},
		{name: "null retains typed SQL NULL", rows: sqlmock.NewRows([]string{"id"}).AddRow(nil), want: 1, kind: qm.CompletionRelationEnd},
		{name: "row failure", rows: sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, io.ErrUnexpectedEOF), failure: io.ErrUnexpectedEOF},
		{name: "invalid integer not truncated", rows: sqlmock.NewRows([]string{"id"}).AddRow("9223372036854775808"), decodeFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("SELECT").WillReturnRows(tc.rows).RowsWillBeClosed()
			rows, err := db.QueryContext(context.Background(), "SELECT id")
			require.NoError(t, err)
			stream := &sqlResultStream{rows: rows, schema: qm.Schema{{ID: 1, Name: "id", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, Nullable: true}}, offset: 5, limit: tc.limit}
			batch, err := stream.Next(context.Background())
			if tc.failure != nil || tc.decodeFailure {
				assert.Empty(t, batch.Rows)
				if tc.failure != nil {
					assert.ErrorIs(t, err, tc.failure)
				} else {
					assert.Error(t, err)
				}
				assert.Empty(t, stream.Completion().Kind)
			} else {
				if tc.want == 0 {
					assert.ErrorIs(t, err, io.EOF)
				} else {
					require.NoError(t, err)
				}
				assert.Len(t, batch.Rows, tc.want)
				_, err = stream.Next(context.Background())
				assert.ErrorIs(t, err, io.EOF)
				assert.Equal(t, tc.kind, stream.Completion().Kind)
				if tc.kind == qm.CompletionWindowEnd {
					completion := stream.Completion()
					require.NotNil(t, completion.NextOffset)
					assert.Equal(t, int64(6), *completion.NextOffset)
					*completion.NextOffset = 999
					assert.Equal(t, int64(6), *stream.Completion().NextOffset)
				}
				if tc.name == "null retains typed SQL NULL" {
					assert.Equal(t, qm.CellValue, batch.Rows[0][0].State)
					assert.IsType(t, qm.NullValue{}, batch.Rows[0][0].Value.Data)
				}
			}
			require.NoError(t, stream.Close())
			require.NoError(t, stream.Close())
			if tc.failure != nil {
				assert.ErrorIs(t, rows.Err(), tc.failure)
			} else {
				assert.NoError(t, rows.Err())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("cancellation closes rows and never publishes completion", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1)).RowsWillBeClosed()
		rows, err := db.QueryContext(context.Background(), "SELECT id")
		require.NoError(t, err)
		stream := &sqlResultStream{rows: rows}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = stream.Next(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, stream.Completion().Kind)
		assert.NoError(t, rows.Err())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("early Close is not successful EOF", func(t *testing.T) {
		stream := &sqlResultStream{}
		require.NoError(t, stream.Close())
		_, err := stream.Next(context.Background())
		assert.Error(t, err)
		assert.False(t, errors.Is(err, io.EOF))
		assert.Empty(t, stream.Completion().Kind)
	})
}

func TestDecodeSQLCell(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     any
		typ     qm.LogicalType
		want    qm.ValueData
		invalid bool
	}{
		{name: "uint64 exact", raw: []byte("18446744073709551615"), typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}, want: qm.IntegerValue("18446744073709551615")},
		{name: "signed minimum", raw: int64(-9223372036854775808), typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, want: qm.IntegerValue("-9223372036854775808")},
		{name: "boolean", raw: int64(1), typ: qm.LogicalType{Kind: qm.TypeBoolean}, want: qm.BooleanValue(true)},
		{name: "invalid boolean", raw: int64(2), typ: qm.LogicalType{Kind: qm.TypeBoolean}, invalid: true},
		{name: "string bytes", raw: []byte("text "), typ: qm.LogicalType{Kind: qm.TypeString}, want: qm.StringValue("text ")},
		{name: "no numeric to string coercion", raw: int64(2), typ: qm.LogicalType{Kind: qm.TypeString}, invalid: true},
		{name: "no float to integer coercion", raw: 1.2, typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, invalid: true},
		{name: "nonnullable null", typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell, err := decodeSQLCell(tc.raw, qm.OutputColumn{Type: tc.typ})
			if tc.invalid {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, cell.Value.Data)
			assert.Equal(t, tc.typ, cell.Value.Type)
		})
	}
}
