// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package table

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type tableSessionConnectorMock struct {
	*vmock.MockTableConnector
	*vmock.MockTableReadSessionConnector
}

type earlyCloseRows struct {
	ctx      context.Context
	next     int
	closeErr error
}

func (*earlyCloseRows) Columns() []string { return []string{"id"} }

func (r *earlyCloseRows) Next(values []driver.Value) error {
	if r.next == 3 {
		return io.EOF
	}
	r.next++
	values[0] = int64(r.next)
	return nil
}

func (r *earlyCloseRows) Close() error {
	r.closeErr = r.ctx.Err()
	return nil
}

type earlyCloseDriver struct {
	driver.Connector
	driver.Conn
	rows *earlyCloseRows
}

func (d *earlyCloseDriver) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (*earlyCloseDriver) Close() error                                   { return nil }

func (d *earlyCloseDriver) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	d.rows.ctx = ctx
	return d.rows, nil
}

type earlyCloseConnector struct {
	interfaces.TableConnector
	interfaces.TableReadSessionConnector
	db *sql.DB
}

func (*earlyCloseConnector) SupportsReadTransaction() bool      { return false }
func (*earlyCloseConnector) MapType(string) string              { return interfaces.DataType_Integer }
func (*earlyCloseConnector) ClassifyQueryError(err error) error { return err }

func (c *earlyCloseConnector) QueryContext(ctx context.Context, _ *sql.Tx, query string, args ...any) (*sql.Rows, error) {
	return c.db.QueryContext(ctx, query, args...)
}

func (*earlyCloseConnector) ScanSQLBuilderRow(rows *sql.Rows, columns []interfaces.ColumnInfo) (map[string]any, error) {
	return ScanSQLRow(rows, columns)
}

func (*tableSessionConnectorMock) SupportsReadTransaction() bool { return true }

func (*tableSessionConnectorMock) QueryContext(ctx context.Context, tx *sql.Tx, query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, query, args...)
}

func (*tableSessionConnectorMock) QueryRowContext(ctx context.Context, tx *sql.Tx, query string, args ...any) (*sql.Row, error) {
	return tx.QueryRowContext(ctx, query, args...), nil
}

func (*tableSessionConnectorMock) ScanTableReadRow(rows *sql.Rows, columns []interfaces.ColumnInfo,
	_ map[string]*interfaces.Property, _ *string, binaryContentColumns map[string]bool) (map[string]any, error) {

	return ScanTableReadRow(rows, columns, binaryContentColumns)
}

func TestOpenReadSession(t *testing.T) {
	t.Run("raw SQL", func(t *testing.T) {
		testOpenReadSessionRows(t, false, "first", "second")
	})
	t.Run("structured query", func(t *testing.T) {
		testOpenReadSessionRows(t, true, []byte("first"), []byte("second"))
	})
	t.Run("structured read errors use the connector classifier", func(t *testing.T) {
		ctx := t.Context()
		db, driver, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() {
			driver.ExpectClose()
			assert.NoError(t, db.Close())
		})
		driver.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		cause := errors.New("driver read error")
		query := SQLStatement{Text: "SELECT id FROM events"}
		column := sqlmock.NewColumn("id").OfType("BIGINT", int64(0))
		rows := sqlmock.NewRowsWithColumnDefinition(column).AddRow(1).RowError(0, cause)
		driver.ExpectQuery(query.Text).WillReturnRows(rows).RowsWillBeClosed()
		driver.ExpectRollback()

		ctrl := gomock.NewController(t)
		base := vmock.NewMockTableConnector(ctrl)
		sessionConnector := vmock.NewMockTableReadSessionConnector(ctrl)
		want := interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, cause)
		gomock.InOrder(
			sessionConnector.EXPECT().BeginReadTx(ctx).Return(tx, nil),
			base.EXPECT().MapType("BIGINT").Return(interfaces.DataType_Integer),
			sessionConnector.EXPECT().ClassifyQueryError(cause).Return(want),
		)
		session, err := OpenReadSession(ctx, &tableSessionConnectorMock{base, sessionConnector}, query, nil,
			&interfaces.Resource{}, &interfaces.ResourceDataQueryParams{}, nil, nil, 0)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()
		page, err := session.ReadPage(ctx, 1)
		assert.Nil(t, page)
		assert.ErrorIs(t, err, want)
		assert.ErrorIs(t, err, cause)
		require.NoError(t, session.Close())
		require.NoError(t, driver.ExpectationsWereMet())
	})
}

func TestTableReadSessionReadPage(t *testing.T) {
	t.Run("request cancellation during lookahead leaves the page readable", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		rows := new(sql.Rows)
		session := &tableReadSession{
			connector: &tableSessionConnectorMock{MockTableConnector: connector},
			rows:      rows,
			pending:   map[string]any{"id": 1},
		}
		connector.EXPECT().ScanSQLBuilderRow(rows, nil).
			DoAndReturn(func(*sql.Rows, []interfaces.ColumnInfo) (map[string]any, error) {
				cancel()
				return map[string]any{"id": 2}, nil
			})

		page, err := session.ReadPage(ctx, 1)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, page.Entries)
		assert.True(t, page.HasMore)
	})
}

func TestTableReadSessionEarlyCloseCancelsQuery(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Minute} {
		t.Run(timeout.String(), func(t *testing.T) {
			rows := &earlyCloseRows{}
			db := sql.OpenDB(&earlyCloseDriver{rows: rows})
			defer func() { _ = db.Close() }()
			ctx := context.WithoutCancel(t.Context())
			session, err := OpenReadSession(ctx, &earlyCloseConnector{db: db}, SQLStatement{Text: "SELECT id"},
				nil, nil, nil, nil, nil, timeout)
			require.NoError(t, err)
			defer func() { _ = session.Close() }()

			page, err := session.ReadPage(ctx, 1)
			require.NoError(t, err)
			require.True(t, page.HasMore)
			require.NoError(t, session.Close())
			assert.ErrorIs(t, rows.closeErr, context.Canceled)
		})
	}
}

func testOpenReadSessionRows(t *testing.T, structured bool, first, second any) {
	t.Helper()
	ctx := t.Context()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	readCtx := context.WithoutCancel(requestCtx)
	db, driver, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() {
		driver.ExpectClose()
		assert.NoError(t, db.Close())
	})
	driver.ExpectBegin()
	query := SQLStatement{Text: "SELECT label FROM events"}
	count := SQLStatement{Text: "SELECT COUNT(*) FROM events"}

	ctrl := gomock.NewController(t)
	connector := vmock.NewMockTableConnector(ctrl)
	readConnector := vmock.NewMockTableReadSessionConnector(ctrl)
	columns := []interfaces.ColumnInfo{{Name: "label", Type: interfaces.DataType_Text}}
	gomock.InOrder(
		readConnector.EXPECT().BeginReadTx(readCtx).
			DoAndReturn(func(txCtx context.Context) (*sql.Tx, error) { return db.BeginTx(txCtx, nil) }),
		connector.EXPECT().MapType("TEXT").Return(interfaces.DataType_Text),
	)
	var resource *interfaces.Resource
	var params *interfaces.ResourceDataQueryParams
	if structured {
		resource = &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
			{Name: "label", Type: interfaces.DataType_Binary},
		}}
		binaryMode := interfaces.BinaryModeContent
		params = &interfaces.ResourceDataQueryParams{BinaryMode: &binaryMode}
	} else {
		scanColumns := []interfaces.ColumnInfo{{Name: "label", Type: interfaces.DataType_Text, NativeType: "TEXT"}}
		connector.EXPECT().ScanSQLBuilderRow(gomock.Any(), scanColumns).DoAndReturn(ScanSQLRow).Times(3)
	}

	sessionConnector := &tableSessionConnectorMock{connector, readConnector}
	session, err := OpenReadSession(readCtx, sessionConnector, query, &count, resource, params, nil, nil, 0)
	cancelRequest()
	require.NoError(t, err)
	require.NoError(t, driver.ExpectationsWereMet())
	defer func() { _ = session.Close() }()
	driver.ExpectQuery(count.Text).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0)).RowsWillBeClosed()
	total, err := session.ReadTotal(readCtx)
	require.NoError(t, err)
	require.NotNil(t, total)
	assert.Zero(t, *total)
	cached, err := session.ReadTotal(ctx)
	require.NoError(t, err)
	assert.Equal(t, total, cached)
	require.NoError(t, driver.ExpectationsWereMet())

	column := sqlmock.NewColumn("label").OfType("TEXT", "")
	rows := sqlmock.NewRowsWithColumnDefinition(column).AddRow([]byte("first")).AddRow([]byte("second"))
	driver.ExpectQuery(query.Text).WillReturnRows(rows).RowsWillBeClosed()
	driver.ExpectRollback()
	firstPageCtx, cancelFirstPage := context.WithCancel(ctx)
	cancelFirstPage()
	page, err := session.ReadPage(context.WithoutCancel(firstPageCtx), 1)
	require.NoError(t, err)
	if structured {
		columns[0].Type = interfaces.DataType_Binary
	}
	assert.Equal(t, columns, page.Columns)
	assert.Equal(t, []map[string]any{{"label": first}}, page.Entries)
	require.NotNil(t, page.TotalCount)
	assert.Zero(t, *page.TotalCount)
	assert.True(t, page.HasMore)
	last, err := session.ReadPage(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"label": second}}, last.Entries)
	assert.False(t, last.HasMore)
	require.NoError(t, session.Close())
	_, err = session.ReadPage(ctx, 1)
	require.ErrorIs(t, err, sql.ErrTxDone)
	require.NoError(t, driver.ExpectationsWereMet())
}

func TestStructuredResultFields(t *testing.T) {
	schema := []*interfaces.Property{
		{Name: "category", Type: interfaces.DataType_String},
		{Name: "created", Type: interfaces.DataType_Datetime},
		{Name: "total", Type: interfaces.DataType_Decimal},
	}
	resource := &interfaces.Resource{SchemaDefinition: schema}

	detail := StructuredResultFields(resource, &interfaces.ResourceDataQueryParams{OutputFields: []string{"total"}})
	require.Equal(t, map[string]*interfaces.Property{"total": schema[2]}, detail)
	assert.NotSame(t, schema[2], detail["total"])

	grouped := StructuredResultFields(resource, &interfaces.ResourceDataQueryParams{
		Aggregation: &interfaces.Aggregation{Aggr: "count", Property: "*", Alias: "total"},
		GroupBy: []*interfaces.GroupByItem{
			{Property: "category"},
			{Property: "created", CalendarInterval: interfaces.CALENDAR_UNIT_MONTH},
		},
	})
	assert.Equal(t, map[string]*interfaces.Property{"category": schema[0]}, grouped)
}
