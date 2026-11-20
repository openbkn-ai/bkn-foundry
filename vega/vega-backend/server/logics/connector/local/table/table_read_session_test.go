// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package table

import (
	"context"
	"database/sql"
	"errors"
	"testing"

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
			&interfaces.Resource{}, &interfaces.ResourceDataQueryParams{}, nil)
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
	t.Run("cancellation during lookahead discards the page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		rows := new(sql.Rows)
		readCanceled := make(chan struct{})
		session := &tableReadSession{
			connector: &tableSessionConnectorMock{MockTableConnector: connector},
			rows:      rows,
			pending:   map[string]any{"id": 1},
			cancel:    func() { close(readCanceled) },
		}
		connector.EXPECT().ScanSQLBuilderRow(rows, nil).
			DoAndReturn(func(*sql.Rows, []interfaces.ColumnInfo) (map[string]any, error) {
				cancel()
				<-readCanceled
				return map[string]any{"id": 2}, nil
			})

		page, err := session.ReadPage(ctx, 1)

		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, page)
	})
}

func testOpenReadSessionRows(t *testing.T, structured bool, first, second any) {
	t.Helper()
	ctx := t.Context()
	db, driver, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() {
		driver.ExpectClose()
		assert.NoError(t, db.Close())
	})
	driver.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	query := SQLStatement{Text: "SELECT label FROM events"}
	count := SQLStatement{Text: "SELECT COUNT(*) FROM events"}
	driver.ExpectQuery(count.Text).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	column := sqlmock.NewColumn("label").OfType("TEXT", "")
	rows := sqlmock.NewRowsWithColumnDefinition(column).AddRow([]byte("first")).AddRow([]byte("second"))
	driver.ExpectQuery(query.Text).WillReturnRows(rows).RowsWillBeClosed()
	driver.ExpectRollback()

	ctrl := gomock.NewController(t)
	connector := vmock.NewMockTableConnector(ctrl)
	readConnector := vmock.NewMockTableReadSessionConnector(ctrl)
	columns := []interfaces.ColumnInfo{{Name: "label", Type: interfaces.DataType_Text}}
	gomock.InOrder(
		readConnector.EXPECT().BeginReadTx(ctx).Return(tx, nil),
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
	session, err := OpenReadSession(ctx, sessionConnector, query, &count, resource, params, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	firstPageCtx, cancelFirstPage := context.WithCancel(ctx)
	defer cancelFirstPage()
	page, err := session.ReadPage(firstPageCtx, 1)
	require.NoError(t, err)
	assert.Equal(t, columns, page.Columns)
	assert.Equal(t, []map[string]any{{"label": first}}, page.Entries)
	require.NotNil(t, page.TotalCount)
	assert.EqualValues(t, 2, *page.TotalCount)
	assert.True(t, page.HasMore)
	cancelFirstPage()
	last, err := session.ReadPage(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"label": second}}, last.Entries)
	assert.False(t, last.HasMore)
	require.NoError(t, session.Close())
	require.NoError(t, driver.ExpectationsWereMet())
}
