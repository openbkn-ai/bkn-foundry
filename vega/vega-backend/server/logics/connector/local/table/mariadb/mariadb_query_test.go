// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestMariaDBConnectorExecuteRawSQL(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	connector := &MariaDBConnector{db: db, connected: true}
	driverErr := &mysql.MySQLError{Number: 1054, Message: "Unknown column 'missing' in 'field list'"}
	mock.ExpectQuery("SELECT missing FROM orders").WillReturnError(driverErr)
	result, err := connector.ExecuteRawSQL(context.Background(), "SELECT missing FROM orders")
	assert.Nil(t, result)
	var invalid *interfaces.SourceQueryInvalidParameterError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
	assert.True(t, errors.Is(err, driverErr))
	assert.ErrorContains(t, err, "failed to execute raw query")
	require.NoError(t, mock.ExpectationsWereMet())
	t.Run("raw rows retain column types, NULLs, and returned row count", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		connector := &MariaDBConnector{db: db, connected: true}
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
			sqlmock.NewColumn("value").OfType("VARCHAR", ""),
			sqlmock.NewColumn("optional").OfType("INT", int64(0)),
		).AddRow([]byte("text"), nil))
		result, err := connector.ExecuteRawSQL(context.Background(), "SELECT value, optional FROM orders")
		require.NoError(t, err)
		assert.Equal(t, []interfaces.ColumnInfo{{Name: "value", Type: interfaces.DataType_String}, {Name: "optional", Type: interfaces.DataType_Integer}}, result.Columns)
		assert.Equal(t, []map[string]any{{"value": "text", "optional": nil}}, result.Entries)
		require.NotNil(t, result.TotalCount)
		assert.EqualValues(t, 1, *result.TotalCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("raw scan errors retain their cause and classification", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		connector := &MariaDBConnector{db: db, connected: true}
		cause := &mysql.MySQLError{Number: 1054, Message: "source column detail"}
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1).RowError(0, cause))
		result, err := connector.ExecuteRawSQL(context.Background(), "SELECT value FROM orders")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "failed to scan raw query rows")
		assert.ErrorContains(t, err, "failed to iterate result rows")
		assert.ErrorIs(t, err, cause)
		var invalid *interfaces.SourceQueryInvalidParameterError
		require.ErrorAs(t, err, &invalid)
		assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
		require.NoError(t, mock.ExpectationsWereMet())
	})

}

func TestMariaDBConnectorExecuteQuery(t *testing.T) {
	t.Run("aggregate alias conflicts reject duplicate result columns", func(t *testing.T) {
		for _, kind := range []string{"explicit", "default", "implicit"} {
			t.Run(kind, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() {
					mock.ExpectClose()
					require.NoError(t, db.Close())
					require.NoError(t, mock.ExpectationsWereMet())
				})
				connector := &MariaDBConnector{db: db, connected: true}
				column := "__value"
				params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count"}}
				if kind == "explicit" {
					column = "label"
					params.Aggregation.Alias = column
				}
				if kind == "implicit" {
					params.Aggregation = nil
					params.Having = &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}
				}
				params.GroupBy = []*interfaces.GroupByItem{{Property: column}}
				resource := &interfaces.Resource{Schema: "APP", SourceIdentifier: "values_table", SchemaDefinition: []*interfaces.Property{{Name: column, OriginalName: "SOURCE_LABEL", OriginalType: "varchar", Type: interfaces.DataType_String}}}
				result, err := connector.ExecuteQuery(context.Background(), resource, params)
				var conflict *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &conflict)
				assert.Equal(t, "aggregate alias conflicts with a group field", conflict.Reason)
				assert.Nil(t, result)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	})

	t.Run("aggregate aliases may match unprojected binary fields", func(t *testing.T) {
		metadata, content := interfaces.BinaryModeMetadata, interfaces.BinaryModeContent
		for _, mode := range []*string{nil, &metadata, &content} {
			modeName := "not requested"
			if mode != nil {
				modeName = *mode
			}
			for _, alias := range []string{"payload", ""} {
				t.Run(modeName+"/"+alias, func(t *testing.T) {
					db, mock, err := sqlmock.New()
					require.NoError(t, err)
					t.Cleanup(func() {
						mock.ExpectClose()
						require.NoError(t, db.Close())
						require.NoError(t, mock.ExpectationsWereMet())
					})
					connector := &MariaDBConnector{db: db, connected: true}
					column := alias
					if column == "" {
						column = "__value"
					}
					resource := &interfaces.Resource{Schema: "APP", SourceIdentifier: "values_table", SchemaDefinition: []*interfaces.Property{{Name: column, OriginalName: "PAYLOAD", OriginalType: "BINARY", Type: interfaces.DataType_Binary}}}
					mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{column}).AddRow(int64(7)))
					result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: alias}, BinaryMode: mode})
					require.NoError(t, err)
					assert.Equal(t, []map[string]any{{column: int64(7)}}, result.Entries)
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		}
	})

	t.Run("aggregate byte values do not inherit source binary content mode", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
		connector := &MariaDBConnector{db: db, connected: true}
		content := interfaces.BinaryModeContent
		resource := &interfaces.Resource{SourceIdentifier: "values_table", SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Binary}}}
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow([]byte("7")))
		result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "payload"}, BinaryMode: &content})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"payload": "7"}}, result.Entries)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("shared unavailable rule preserves readable Other and actual NULL", func(t *testing.T) {
		metadata := interfaces.BinaryModeMetadata
		for _, mode := range []*string{nil, &metadata} {
			modeName := "not requested"
			if mode != nil {
				modeName = *mode
			}
			t.Run(modeName, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() {
					mock.ExpectClose()
					require.NoError(t, db.Close())
					require.NoError(t, mock.ExpectationsWereMet())
				})
				connector := &MariaDBConnector{db: db, connected: true}
				resource := &interfaces.Resource{Schema: "APP", SourceIdentifier: "values_table", SchemaDefinition: []*interfaces.Property{
					{Name: "blob", Type: interfaces.DataType_Binary}, {Name: "other", Type: interfaces.DataType_Other},
				}}
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"blob", "other"}).AddRow(nil, "[1,2]").AddRow(nil, nil))
				result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{BinaryMode: mode})
				require.NoError(t, err)
				require.Len(t, result.Entries, 2)
				assert.Equal(t, "[1,2]", result.Entries[0]["other"])
				assert.Contains(t, result.Entries[1], "other")
				assert.Nil(t, result.Entries[1]["other"])
				for _, entry := range result.Entries {
					if mode == nil {
						assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable, Reason: interfaces.ResourceValueReasonNotRequested}, entry["blob"])
					} else {
						assert.Contains(t, entry, "blob")
						assert.Nil(t, entry["blob"])
					}
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	})

	t.Run("invalid projections fail before source execution", func(t *testing.T) {
		for _, connectorType := range []string{interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB} {
			t.Run(connectorType, func(t *testing.T) {
				for _, tc := range []struct {
					name   string
					schema []*interfaces.Property
					fields []string
					reason interfaces.OutputFieldErrorReason
					field  string
				}{
					{name: "unknown output", schema: []*interfaces.Property{{Name: "id"}}, fields: []string{"missing"}, reason: interfaces.OutputFieldUnknown, field: "missing"},
					{name: "mixed output", schema: []*interfaces.Property{{Name: "id"}}, fields: []string{"id", "missing"}, reason: interfaces.OutputFieldUnknown, field: "missing"},
					{name: "empty output name", schema: []*interfaces.Property{{Name: "id"}}, fields: []string{""}, reason: interfaces.OutputFieldUnknown},
					{name: "empty schema", reason: interfaces.OutputFieldNoneQueryable},
				} {
					t.Run(tc.name, func(t *testing.T) {
						db, mock, err := sqlmock.New()
						require.NoError(t, err)
						defer func() { _ = db.Close() }()
						connector := &MariaDBConnector{db: db, connected: true, connectorType: connectorType}
						resource := &interfaces.Resource{SourceIdentifier: "orders", SchemaDefinition: tc.schema}
						params := &interfaces.ResourceDataQueryParams{OutputFields: tc.fields, NeedTotal: true}
						before := append([]string(nil), tc.fields...)
						result, err := connector.ExecuteQuery(context.Background(), resource, params)
						var output *interfaces.OutputFieldError
						require.ErrorAs(t, err, &output)
						assert.Equal(t, tc.reason, output.Reason)
						assert.Equal(t, tc.field, output.Field)
						assert.Nil(t, result)
						assert.Equal(t, before, params.OutputFields)
						require.NoError(t, mock.ExpectationsWereMet())
					})
				}
			})
		}
	})
	for _, tc := range []struct {
		name      string
		context   string
		needTotal bool
		rowError  bool
	}{
		{name: "select references removed column", context: "failed to execute query"},
		{name: "row iteration returns unknown column", context: "failed to iterate result rows", rowError: true},
		{name: "count query returns unknown column", context: "failed to scan total", needTotal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			connector := &MariaDBConnector{db: db, connected: true}
			resource := &interfaces.Resource{SourceIdentifier: "`shop`.`orders`", SchemaDefinition: []*interfaces.Property{{Name: "missing"}}}
			params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}, NeedTotal: tc.needTotal}
			driverErr := &mysql.MySQLError{Number: 1054, Message: "private source column details"}
			if tc.needTotal {
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"missing"}).AddRow("value"))
				mock.ExpectQuery("SELECT COUNT").WillReturnError(driverErr)
			} else if tc.rowError {
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"missing"}).AddRow("value").RowError(0, driverErr))
			} else {
				mock.ExpectQuery("SELECT").WillReturnError(driverErr)
			}

			result, err := connector.ExecuteQuery(context.Background(), resource, params)

			assert.Nil(t, result)
			var invalid *interfaces.SourceQueryInvalidParameterError
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
			assert.ErrorIs(t, err, driverErr)
			assert.ErrorContains(t, err, tc.context)
			if tc.rowError {
				assert.ErrorContains(t, err, "failed to scan query rows")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	for _, preserveBinary := range []bool{false, true} {
		name := "byte values become strings"
		if preserveBinary {
			name = "binary content remains bytes"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			connector := &MariaDBConnector{db: db, connected: true}
			payloadType := interfaces.DataType_String
			if preserveBinary {
				payloadType = interfaces.DataType_Binary
			}
			resource := &interfaces.Resource{SourceIdentifier: "`shop`.`orders`", SchemaDefinition: []*interfaces.Property{
				{Name: "payload", Type: payloadType}, {Name: "name", Type: interfaces.DataType_String},
			}}
			params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}}
			var payload any = "data"
			if preserveBinary {
				mode := interfaces.BinaryModeContent
				params.BinaryMode = &mode
				payload = []byte("data")
			}
			mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"payload", "name"}).AddRow([]byte("data"), []byte("text")).AddRow(nil, nil))
			result, err := connector.ExecuteQuery(context.Background(), resource, params)
			require.NoError(t, err)
			assert.Equal(t, []string{"payload", "name"}, result.Columns)
			assert.Equal(t, []map[string]any{{"payload": payload, "name": "text"}, {"payload": nil, "name": nil}}, result.Entries)
			assert.Zero(t, result.Total)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}

	for _, emptyPage := range []bool{false, true} {
		name := "aggregate total counts all groups"
		if emptyPage {
			name = "aggregate total is available beyond last page"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(1)
			connector := &MariaDBConnector{db: db, connected: true}
			resource := &interfaces.Resource{SourceIdentifier: "`shop`.`orders`", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
			params := &interfaces.ResourceDataQueryParams{
				GroupBy:     []*interfaces.GroupByItem{{Property: "id"}},
				Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "total"},
				Having:      &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 2},
				Paging:      interfaces.PagingRequest{Offset: 10, Limit: 1}, NeedTotal: true,
			}
			statement := "SELECT `id` AS `id`, COUNT(*) AS `total` FROM `shop`.`orders` GROUP BY `id` HAVING `total` > ?"
			rows := sqlmock.NewRows([]string{"id", "total"})
			if !emptyPage {
				rows.AddRow(3, 4)
			}
			mock.ExpectQuery(statement + " LIMIT 1 OFFSET 10").WithArgs(2).WillReturnRows(rows).RowsWillBeClosed()
			mock.ExpectQuery("SELECT COUNT(*) FROM (" + statement + ") AS _query_groups").WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(8))
			result, err := connector.ExecuteQuery(context.Background(), resource, params)
			require.NoError(t, err)
			assert.EqualValues(t, 8, result.Total)
			if emptyPage {
				assert.Empty(t, result.Entries)
			} else {
				assert.Len(t, result.Entries, 1)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}

}

func TestMariaDBConnectorBuildPagedSQL(t *testing.T) {
	connector := &MariaDBConnector{}
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM orders\n) AS _raw_query_page LIMIT 10 OFFSET 20",
		connector.BuildPagedSQL("SELECT id FROM orders", 20, 10),
	)
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM orders -- trailing comment\n) AS _raw_query_page LIMIT 10 OFFSET 20",
		connector.BuildPagedSQL("SELECT id FROM orders -- trailing comment\n", 20, 10),
	)
}

func TestMariaDBConnectorBuildCountSQL(t *testing.T) {
	connector := &MariaDBConnector{}
	assert.Equal(t,
		"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT id FROM orders -- trailing comment\n) AS _raw_query_total",
		connector.BuildCountSQL("SELECT id FROM orders -- trailing comment"),
	)
}

func TestMariaDBConnectorBuildHavingCondition(t *testing.T) {
	connector := &MariaDBConnector{}
	field := &interfaces.Property{Type: interfaces.DataType_Integer}
	for _, tc := range []struct {
		name, operation, sql string
		value                any
		args                 []any
	}{
		{"equal", "==", "`total` = ?", 10, []any{10}},
		{"quoted string stays bound", "!=", "`total` <> ?", "a' OR 1=1 --", []any{"a' OR 1=1 --"}},
		{"greater equal", ">=", "`total` >= ?", 3, []any{3}},
		{"string list", "in", "`total` IN (?,?)", []string{"a'", "b"}, []any{"a'", "b"}},
		{"not in", "not_in", "`total` NOT IN (?,?)", []any{1, 2}, []any{1, 2}},
		{"range", "range", "(`total` >= ? AND `total` <= ?)", []any{1, 3}, []any{1, 3}},
		{"out range", "out_range", "(`total` < ? OR `total` > ?)", []any{1, 3}, []any{1, 3}},
		{"empty in", "in", "1=0", []any{}, nil},
		{"empty not in", "not_in", "1=1", []any{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression, err := connector.buildHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: tc.operation, Value: tc.value}, "`total`", field)
			require.NoError(t, err)
			sql, args, err := expression.ToSql()
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sql)
			assert.Equal(t, tc.args, args)
		})
	}
	for _, having := range []*interfaces.HavingClause{
		{Field: "name", Operation: "==", Value: 1},
		{Field: "__value", Operation: "like", Value: 1},
		{Field: "__value", Operation: "range", Value: []any{1}},
		{Field: "__value", Operation: "in", Value: 1},
		{Field: "__value", Operation: "=="},
	} {
		_, err := connector.buildHavingCondition(having, "`total`", field)
		require.Error(t, err)
	}
	t.Run("date bounds retain date parameter conversion", func(t *testing.T) {
		timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		field := &interfaces.Property{Type: interfaces.DataType_Datetime}
		expression, err := connector.buildHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{timestamp, timestamp}}, "MIN(`created`)", field)
		require.NoError(t, err)
		sql, args, err := expression.ToSql()
		require.NoError(t, err)
		assert.Equal(t, "(MIN(`created`) >= ? AND MIN(`created`) <= ?)", sql)
		assert.Equal(t, []any{timestamp, timestamp}, args)
	})

	for _, upper := range []bool{false, true} {
		name := "invalid date lower bound"
		bounds := []any{false, int64(0)}
		context := "failed to convert HAVING lower bound"
		if upper {
			name = "invalid date upper bound"
			bounds = []any{int64(0), false}
			context = "failed to convert HAVING upper bound"
		}
		t.Run(name, func(t *testing.T) {
			expression, err := connector.buildHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: "range", Value: bounds}, "MIN(`created`)", &interfaces.Property{Type: interfaces.DataType_Datetime})
			require.Error(t, err)
			assert.Nil(t, expression)
			assert.ErrorContains(t, err, context)
		})
	}

}

func TestBuildDateFormat(t *testing.T) {
	connector := &MariaDBConnector{}
	tests := []struct {
		name     string
		interval string
		want     string
	}{
		{name: "minute", interval: interfaces.CALENDAR_UNIT_MINUTE, want: "date_format(created_at,'%Y-%m-%d %H:%i')"},
		{name: "hour", interval: interfaces.CALENDAR_UNIT_HOUR, want: "date_format(created_at,'%Y-%m-%d %H')"},
		{name: "day", interval: interfaces.CALENDAR_UNIT_DAY, want: "date_format(created_at,'%Y-%m-%d')"},
		{name: "week", interval: interfaces.CALENDAR_UNIT_WEEK, want: "date_format(created_at,'%x-%v')"},
		{name: "month", interval: interfaces.CALENDAR_UNIT_MONTH, want: "date_format(created_at,'%Y-%m')"},
		{name: "quarter", interval: interfaces.CALENDAR_UNIT_QUARTER, want: "concat(year(created_at),'-Q',quarter(created_at))"},
		{name: "year", interval: interfaces.CALENDAR_UNIT_YEAR, want: "date_format(created_at,'%Y')"},
		{name: "unknown", interval: "unknown", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.buildDateFormat("created_at", tt.interval))
		})
	}
}

func TestBuildSelectBuilderQuotesIdentifiers(t *testing.T) {
	connector := &MariaDBConnector{}
	// The source column key is a reserved word and must be quoted in SELECT.
	resource := &interfaces.Resource{
		SourceIdentifier: "`yanfeng_kb`.`fact`",
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
			{Name: "key", OriginalName: "key", Type: interfaces.DataType_String},
			{Name: "created", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		},
	}
	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	t.Run("detail query quotes every column and the table", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id`, `key` AS `key`, `created_at` AS `created` FROM `yanfeng_kb`.`fact` LIMIT 10 OFFSET 0",
			sql)
	})

	t.Run("output_fields resolve to quoted original names", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{
				OutputFields: []string{"key", "created"},
				Paging:       interfaces.PagingRequest{Limit: 5},
			}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t, "SELECT `key` AS `key`, `created_at` AS `created` FROM `yanfeng_kb`.`fact` LIMIT 5 OFFSET 0", sql)
	})

	t.Run("sort quotes the column and maps to its original name", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"id"},
			Sort:         []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Paging:       interfaces.PagingRequest{Limit: 5},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id` FROM `yanfeng_kb`.`fact` ORDER BY `created_at` DESC LIMIT 5 OFFSET 0",
			sql)
	})

	// Generated vector fields have no OriginalName; use Name instead of
	// producing an empty quoted identifier.
	t.Run("a property without original_name falls back to the property name", func(t *testing.T) {
		schemaless := &interfaces.Resource{
			SourceIdentifier: "`yanfeng_kb`.`fact`",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
				{Name: "content_vector", Type: interfaces.DataType_Vector},
			},
		}
		schemalessFields := map[string]*interfaces.Property{}
		for _, prop := range schemaless.SchemaDefinition {
			schemalessFields[prop.Name] = prop
		}

		builder, err := connector.buildSelectBuilder(schemaless, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"content_vector"},
			Sort:         []*interfaces.SortField{{Field: "content_vector", Direction: interfaces.DESC_DIRECTION}},
			Paging:       interfaces.PagingRequest{Limit: 5},
		}, schemalessFields, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `content_vector` AS `content_vector` FROM `yanfeng_kb`.`fact` ORDER BY `content_vector` DESC LIMIT 5 OFFSET 0",
			sql)
	})

	// Default projection must use the same name fallback as explicit projection.
	t.Run("select all falls back for a property without original_name", func(t *testing.T) {
		schemaless := &interfaces.Resource{
			SourceIdentifier: "`yanfeng_kb`.`fact`",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", OriginalName: "id", Type: interfaces.DataType_String},
				{Name: "content_vector", Type: interfaces.DataType_Vector},
			},
		}
		schemalessFields := map[string]*interfaces.Property{}
		for _, prop := range schemaless.SchemaDefinition {
			schemalessFields[prop.Name] = prop
		}

		builder, err := connector.buildSelectBuilder(schemaless,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}}, schemalessFields, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id`, `content_vector` AS `content_vector` FROM `yanfeng_kb`.`fact` LIMIT 10 OFFSET 0",
			sql)
	})

	t.Run("aggregation quotes the aggregated column, group by and alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			Paging:      interfaces.PagingRequest{Limit: 20},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20 OFFSET 0",
			sql)
	})

	// Sorting must prefer an aggregate alias over a same-named source property.
	t.Run("sorting by an aggregate alias that shadows a property uses the alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "created"},
			Sort:        []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Paging:      interfaces.PagingRequest{Limit: 20},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `created` FROM `yanfeng_kb`.`fact` GROUP BY `key` "+
				"ORDER BY `created` DESC LIMIT 20 OFFSET 0",
			sql)
	})

	t.Run("aggregate output_fields do not alter the grouped and aggregate projection", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:      []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation:  &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			OutputFields: []string{"key", "cnt", "created"},
			Paging:       interfaces.PagingRequest{Limit: 20},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20 OFFSET 0",
			sql)
	})

	t.Run("count star having output_fields do not alter the aggregate projection", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			Having:       &interfaces.HavingClause{Field: "count(*)", Operation: ">=", Value: 3},
			OutputFields: []string{"created"},
			Paging:       interfaces.PagingRequest{Limit: 20},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, args, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT COUNT(*) AS `__value` FROM `yanfeng_kb`.`fact` HAVING COUNT(*) >= ? LIMIT 20 OFFSET 0",
			sql)
		assert.Equal(t, []any{3}, args)
	})

	t.Run("count star and custom alias bind WHERE before HAVING", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "total"},
			Having:      &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{2, 4}},
			Paging:      interfaces.PagingRequest{Limit: 20},
		}, fieldMap, sq.Expr("`id` > ?", 1))
		require.NoError(t, err)
		sql, args, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t, "SELECT COUNT(*) AS `total` FROM `yanfeng_kb`.`fact` WHERE `id` > ? HAVING (`total` >= ? AND `total` <= ?) LIMIT 20 OFFSET 0", sql)
		assert.Equal(t, []any{1, 2, 4}, args)
	})

	t.Run("calendar interval keeps the date_format expression over a quoted column", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{{Property: "created", CalendarInterval: interfaces.CALENDAR_UNIT_DAY}},
			Paging:  interfaces.PagingRequest{Limit: 20},
		}, fieldMap, nil)
		require.NoError(t, err)

		sql, _, err := builder.ToSql()
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT date_format(`created_at`,'%Y-%m-%d') AS `created` FROM `yanfeng_kb`.`fact` "+
				"GROUP BY date_format(`created_at`,'%Y-%m-%d') LIMIT 20 OFFSET 0",
			sql)
	})
}

func TestMariaDBConvertValue(t *testing.T) {
	t.Run("JSON text is opaque", func(t *testing.T) {
		for _, text := range []string{`{"n":9007199254740993}`, `[1,true]`, `"scalar"`, "", "true", "null", `{"incomplete":`, "引号\"反斜杠\\\n"} {
			for _, raw := range []any{text, []byte(text)} {
				assert.Equal(t, text, convertValue(raw, false))
			}
		}
	})

	t.Run("maria dbconvert value", func(t *testing.T) {
		assert.Equal(t, "hello", convertValue([]byte("hello"), false))
		assert.Equal(t, []byte("hello"), convertValue([]byte("hello"), true))
		assert.Equal(t, int64(1), convertValue(int64(1), false))
		assert.Nil(t, convertValue(nil, false))
	})
}
