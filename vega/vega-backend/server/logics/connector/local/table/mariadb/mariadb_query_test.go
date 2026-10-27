// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
)

type unknownNode struct{ *sb.Column }

func TestMariaDBConnectorExecuteRawSQL(t *testing.T) {
	for _, native := range []string{"BINARY", "VARBINARY", "TINYBLOB", "BLOB", "MEDIUMBLOB", "LONGBLOB"} {
		t.Run(native+" preserves binary content alongside text", func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			connector := &MariaDBConnector{db: db, connected: true}
			mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
				sqlmock.NewColumn("payload").OfType(native, []byte{}),
				sqlmock.NewColumn("label").OfType("TEXT", ""),
			).AddRow([]byte{0xff, 0x00}, []byte("text")).AddRow([]byte{}, []byte{}).AddRow(nil, nil))
			result, err := connector.ExecuteRawSQL(context.Background(), "SELECT payload, label FROM orders")
			require.NoError(t, err)
			assert.Equal(t, []interfaces.ColumnInfo{{Name: "payload", Type: interfaces.DataType_Binary}, {Name: "label", Type: interfaces.DataType_Text}}, result.Columns)
			assert.Equal(t, []map[string]any{{"payload": []byte{0xff, 0x00}, "label": "text"}, {"payload": []byte{}, "label": ""}, {"payload": nil, "label": nil}}, result.Entries)
			wire, err := json.Marshal(result.Entries)
			require.NoError(t, err)
			assert.JSONEq(t, `[{"payload":"/wA=","label":"text"},{"payload":"","label":""},{"payload":null,"label":null}]`, string(wire))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("BIT raw results preserve bytes and declare binary", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		connector := &MariaDBConnector{db: db, connected: true}
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
			sqlmock.NewColumn("bits").OfType("BIT", []byte{}),
			sqlmock.NewColumn("flag").OfType("TINYINT", int64(0)),
		).AddRow([]byte{0xff, 0x00}, int64(127)).AddRow([]byte{0}, int64(0)).AddRow(nil, nil))
		result, err := connector.ExecuteRawSQL(context.Background(), "SELECT bits, flag FROM orders")
		require.NoError(t, err)
		assert.Equal(t, []interfaces.ColumnInfo{{Name: "bits", Type: interfaces.DataType_Binary}, {Name: "flag", Type: interfaces.DataType_Integer}}, result.Columns)
		assert.Equal(t, []map[string]any{{"bits": []byte{0xff, 0x00}, "flag": int64(127)}, {"bits": []byte{0}, "flag": int64(0)}, {"bits": nil, "flag": nil}}, result.Entries)
		wire, err := json.Marshal(result.Entries)
		require.NoError(t, err)
		assert.JSONEq(t, `[{"bits":"/wA=","flag":127},{"bits":"AA==","flag":0},{"bits":null,"flag":null}]`, string(wire))
		require.NoError(t, mock.ExpectationsWereMet())
	})

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
	t.Run("discovered BIT uses existing binary modes and retains integer values", func(t *testing.T) {
		metadata, content := interfaces.BinaryModeMetadata, interfaces.BinaryModeContent
		for _, native := range []string{"bit(1)", "bit(8)", "bit(17)"} {
			for _, mode := range []*string{nil, &metadata, &content} {
				modeName := "not requested"
				if mode != nil {
					modeName = *mode
				}
				t.Run(native+"/"+modeName, func(t *testing.T) {
					db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
					require.NoError(t, err)
					defer func() { _ = db.Close() }()
					connector := &MariaDBConnector{db: db, connected: true}
					resource := &interfaces.Resource{SourceIdentifier: "orders", SchemaDefinition: []*interfaces.Property{
						{Name: "bits", OriginalType: native, Type: connector.MapType(native)},
						{Name: "flag", OriginalType: "tinyint(1)", Type: connector.MapType("tinyint(1)")},
					}}
					nonzero, zero := []byte{1}, []byte{0}
					switch native {
					case "bit(8)":
						nonzero = []byte{0xff}
					case "bit(17)":
						nonzero, zero = []byte{1, 0xff, 0xff}, []byte{0, 0, 0}
					}
					projection := "NULL"
					rows := sqlmock.NewRows([]string{"bits", "flag"})
					switch modeName {
					case interfaces.BinaryModeContent:
						projection = "`bits`"
						rows.AddRow(nonzero, int64(127)).AddRow(zero, int64(0)).AddRow(nil, nil)
					case interfaces.BinaryModeMetadata:
						projection = "OCTET_LENGTH(`bits`)"
						rows.AddRow(int64(len(nonzero)), int64(127)).AddRow(int64(len(zero)), int64(0)).AddRow(nil, nil)
					default:
						rows.AddRow(nil, int64(127)).AddRow(nil, int64(0)).AddRow(nil, nil)
					}
					mock.ExpectQuery("SELECT " + projection + " AS `bits`, `flag` AS `flag` FROM orders LIMIT 10 OFFSET 0").WillReturnRows(rows)
					result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{BinaryMode: mode, Paging: interfaces.PagingRequest{Limit: 10}})
					require.NoError(t, err)
					require.Len(t, result.Entries, 3)
					assert.Equal(t, int64(127), result.Entries[0]["flag"])
					assert.Equal(t, int64(0), result.Entries[1]["flag"])
					assert.Nil(t, result.Entries[2]["flag"])
					switch modeName {
					case interfaces.BinaryModeContent:
						assert.Equal(t, nonzero, result.Entries[0]["bits"])
						assert.Equal(t, zero, result.Entries[1]["bits"])
						assert.Nil(t, result.Entries[2]["bits"])
					case interfaces.BinaryModeMetadata:
						assert.Equal(t, int64(len(nonzero)), result.Entries[0]["bits"])
						assert.Equal(t, int64(len(zero)), result.Entries[1]["bits"])
						assert.Nil(t, result.Entries[2]["bits"])
					default:
						for _, entry := range result.Entries {
							assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable, Reason: interfaces.ResourceValueReasonNotRequested}, entry["bits"])
						}
					}
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		}
	})

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
		{name: "select references removed column", context: "failed to execute SQL builder query"},
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
			statement := "SELECT `id` AS `id`, COUNT(*) AS `total` FROM `shop`.`orders` GROUP BY `id` HAVING (`total` > ?)"
			rows := sqlmock.NewRows([]string{"id", "total"})
			if !emptyPage {
				rows.AddRow(3, 4)
			}
			mock.ExpectQuery(statement + " LIMIT 1 OFFSET 10").WithArgs(2).WillReturnRows(rows).RowsWillBeClosed()
			mock.ExpectQuery("SELECT COUNT(*) FROM (" + statement + ") AS `_resource_total`").WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(8))
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
	t.Run("invalid temporal value is a request error", func(t *testing.T) {
		field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
		_, err := sqlHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: ">", Value: "2026-10-19"}, &sb.Function{Name: "MIN", Args: []sb.SqlNode{&sb.Column{Name: "created"}}}, field)
		var conditionErr *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &conditionErr)
		_, request := interfaces.RequestSideQueryError(err)
		assert.True(t, request)
	})

	field := &interfaces.Property{Type: interfaces.DataType_Integer}
	for _, tc := range []struct {
		name, operation, sql string
		value                any
		args                 []any
	}{
		{"equal", "==", "(`total` = ?)", 10, []any{10}},
		{"quoted string stays bound", "!=", "(`total` <> ?)", "a' OR 1=1 --", []any{"a' OR 1=1 --"}},
		{"greater equal", ">=", "(`total` >= ?)", 3, []any{3}},
		{"string list", "in", "(`total` IN (?, ?))", []string{"a'", "b"}, []any{"a'", "b"}},
		{"not in", "not_in", "(`total` NOT IN (?, ?))", []any{1, 2}, []any{1, 2}},
		{"range", "range", "((`total` >= ?) AND (`total` <= ?))", []any{1, 3}, []any{1, 3}},
		{"out range", "out_range", "((`total` < ?) OR (`total` > ?))", []any{1, 3}, []any{1, 3}},
		{"empty in", "in", "(1=0)", []any{}, nil},
		{"empty not in", "not_in", "(1=1)", []any{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression, err := sqlHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: tc.operation, Value: tc.value}, &sb.Column{Name: "total"}, field)
			require.NoError(t, err)
			sql, args, err := render(expression)
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
		_, err := sqlHavingCondition(having, &sb.Column{Name: "total"}, field)
		require.Error(t, err)
	}
	t.Run("date bounds retain date parameter conversion", func(t *testing.T) {
		timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		field := &interfaces.Property{Type: interfaces.DataType_Datetime}
		expression, err := sqlHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{timestamp, timestamp}}, &sb.Function{Name: "MIN", Args: []sb.SqlNode{&sb.Column{Name: "created"}}}, field)
		require.NoError(t, err)
		sql, args, err := render(expression)
		require.NoError(t, err)
		assert.Equal(t, "((MIN(`created`) >= ?) AND (MIN(`created`) <= ?))", sql)
		assert.Equal(t, []any{timestamp, timestamp}, args)
	})

	for _, upper := range []bool{false, true} {
		name := "invalid date lower bound"
		bounds := []any{false, int64(0)}
		context := "failed to convert range lower bound"
		if upper {
			name = "invalid date upper bound"
			bounds = []any{int64(0), false}
			context = "failed to convert range upper bound"
		}
		t.Run(name, func(t *testing.T) {
			expression, err := sqlHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: "range", Value: bounds}, &sb.Function{Name: "MIN", Args: []sb.SqlNode{&sb.Column{Name: "created"}}}, &interfaces.Property{Type: interfaces.DataType_Datetime})
			require.Error(t, err)
			assert.Nil(t, expression)
			assert.ErrorContains(t, err, context)
		})
	}

}

func TestSqlCalendarGroup(t *testing.T) {
	for _, format := range []struct{ unit, format string }{
		{interfaces.CALENDAR_UNIT_MINUTE, "%Y-%m-%d %H:%i"}, {interfaces.CALENDAR_UNIT_HOUR, "%Y-%m-%d %H"},
		{interfaces.CALENDAR_UNIT_DAY, "%Y-%m-%d"}, {interfaces.CALENDAR_UNIT_WEEK, "%x-%v"},
		{interfaces.CALENDAR_UNIT_MONTH, "%Y-%m"}, {interfaces.CALENDAR_UNIT_YEAR, "%Y"},
	} {
		node, err := sqlCalendarGroup(&sb.Column{Name: "created_at"}, format.unit)
		require.NoError(t, err)
		sql, args, err := render(node)
		require.NoError(t, err)
		assert.Equal(t, "DATE_FORMAT(`created_at`, ?)", sql)
		assert.Equal(t, []any{format.format}, args)
	}
	node, err := sqlCalendarGroup(&sb.Column{Name: "created_at"}, interfaces.CALENDAR_UNIT_QUARTER)
	require.NoError(t, err)
	sql, args, err := render(node)
	require.NoError(t, err)
	assert.Equal(t, "CONCAT(YEAR(`created_at`), ?, QUARTER(`created_at`))", sql)
	assert.Equal(t, []any{"-Q"}, args)
	_, err = sqlCalendarGroup(&sb.Column{Name: "created_at"}, "unknown")
	require.Error(t, err)
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

	t.Run("detail query quotes every column and the table", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id`, `key` AS `key`, `created_at` AS `created` FROM `yanfeng_kb`.`fact` LIMIT 10",
			sql)
	})

	t.Run("output_fields resolve to quoted original names", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource,
			&interfaces.ResourceDataQueryParams{
				OutputFields: []string{"key", "created"},
				Paging:       interfaces.PagingRequest{Limit: 5},
			})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `key` AS `key`, `created_at` AS `created` FROM `yanfeng_kb`.`fact` LIMIT 5", sql)
	})

	t.Run("sort quotes the column and maps to its original name", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"id"},
			Sort:         []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Paging:       interfaces.PagingRequest{Limit: 5},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id` FROM `yanfeng_kb`.`fact` ORDER BY `created_at` DESC LIMIT 5",
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

		builder, err := connector.buildSelectBuilder(schemaless, &interfaces.ResourceDataQueryParams{
			OutputFields: []string{"content_vector"},
			Sort:         []*interfaces.SortField{{Field: "content_vector", Direction: interfaces.DESC_DIRECTION}},
			Paging:       interfaces.PagingRequest{Limit: 5},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `content_vector` AS `content_vector` FROM `yanfeng_kb`.`fact` ORDER BY `content_vector` DESC LIMIT 5",
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

		builder, err := connector.buildSelectBuilder(schemaless,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `id` AS `id`, `content_vector` AS `content_vector` FROM `yanfeng_kb`.`fact` LIMIT 10",
			sql)
	})

	t.Run("aggregation quotes the aggregated column, group by and alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			Paging:      interfaces.PagingRequest{Limit: 20},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20",
			sql)
	})

	// Sorting must prefer an aggregate alias over a same-named source property.
	t.Run("sorting by an aggregate alias that shadows a property uses the alias", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:     []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "created"},
			Sort:        []*interfaces.SortField{{Field: "created", Direction: interfaces.DESC_DIRECTION}},
			Paging:      interfaces.PagingRequest{Limit: 20},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `created` FROM `yanfeng_kb`.`fact` GROUP BY `key` "+
				"ORDER BY `created` DESC LIMIT 20",
			sql)
	})

	t.Run("aggregate output_fields do not alter the grouped and aggregate projection", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy:      []*interfaces.GroupByItem{{Property: "key"}},
			Aggregation:  &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "cnt"},
			OutputFields: []string{"key", "cnt", "created"},
			Paging:       interfaces.PagingRequest{Limit: 20},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT `key` AS `key`, COUNT(`id`) AS `cnt` FROM `yanfeng_kb`.`fact` GROUP BY `key` LIMIT 20",
			sql)
	})

	t.Run("count star having output_fields do not alter the aggregate projection", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			Having:       &interfaces.HavingClause{Field: "count(*)", Operation: ">=", Value: 3},
			OutputFields: []string{"created"},
			Paging:       interfaces.PagingRequest{Limit: 20},
		})
		require.NoError(t, err)

		sql, args, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT COUNT(*) AS `__value` FROM `yanfeng_kb`.`fact` HAVING (COUNT(*) >= ?) LIMIT 20",
			sql)
		assert.Equal(t, []any{3}, args)
	})

	t.Run("count star and custom alias bind WHERE before HAVING", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			Aggregation:      &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "total"},
			Having:           &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{2, 4}},
			Paging:           interfaces.PagingRequest{Limit: 20},
			ActualFilterCond: &filter_condition.GtCond{Lfield: &interfaces.Property{Name: "id"}, Value: 1}})
		require.NoError(t, err)
		sql, args, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t, "SELECT COUNT(*) AS `total` FROM `yanfeng_kb`.`fact` WHERE (`id` > ?) HAVING ((`total` >= ?) AND (`total` <= ?)) LIMIT 20", sql)
		assert.Equal(t, []any{1, 2, 4}, args)
	})

	t.Run("calendar interval keeps the date_format expression over a quoted column", func(t *testing.T) {
		builder, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{{Property: "created", CalendarInterval: interfaces.CALENDAR_UNIT_DAY}},
			Paging:  interfaces.PagingRequest{Limit: 20},
		})
		require.NoError(t, err)

		sql, _, err := ToSql(builder)
		require.NoError(t, err)
		assert.Equal(t,
			"SELECT DATE_FORMAT(`created_at`, ?) AS `created` FROM `yanfeng_kb`.`fact` "+
				"GROUP BY DATE_FORMAT(`created_at`, ?) LIMIT 20",
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

func TestMariadbQueryError(t *testing.T) {
	for _, code := range []uint16{1052, 1054, 1055, 1056, 1060, 1064, 1111, 1140, 1210, 1221, 1241, 1582, 1583, 1584, 3065} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cause := &mysql.MySQLError{Number: code, Message: "private source SQL and values"}
			err := mariadbQueryError(fmt.Errorf("query failed: %w", cause))
			var failure *interfaces.SourceQueryInvalidParameterError
			require.ErrorAs(t, err, &failure)
			assert.ErrorIs(t, err, cause)
			detail, ok := interfaces.RequestSideQueryError(err)
			assert.True(t, ok)
			assert.NotContains(t, detail, "private")
			if code == 1054 {
				assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, failure.Reason)
			} else {
				assert.Equal(t, "invalid source query parameter", detail)
			}
		})
	}
	for _, cause := range []error{context.Canceled, driver.ErrBadConn, &mysql.MySQLError{Number: 1040, Message: "connection limit"}, &mysql.MySQLError{Number: 1146, Message: "missing physical table"}, &mysql.MySQLError{Number: 1205, Message: "lock timeout"}} {
		err := mariadbQueryError(cause)
		assert.ErrorIs(t, err, cause)
		_, ok := interfaces.RequestSideQueryError(err)
		assert.False(t, ok)
	}
}

func TestMariaDBConnectorExecuteSQLResourceQuery(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	defer func() {
		mock.ExpectClose()
		assert.NoError(t, db.Close())
		assert.NoError(t, mock.ExpectationsWereMet())
	}()
	connector := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL, db: db, connected: true}
	resource := &interfaces.Resource{SourceIdentifier: "`orders`", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "name", Type: interfaces.DataType_String}}}
	params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id", "name"}, Paging: interfaces.PagingRequest{Limit: 2, Offset: 1}, NeedTotal: true, Sort: []*interfaces.SortField{{Field: "id"}}, ActualFilterCond: &filter_condition.GtCond{Cfg: &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Const}, Lfield: resource.SchemaDefinition[0], Value: int64(3)}}
	mock.ExpectQuery("SELECT `id` AS `id`, `name` AS `name` FROM `orders` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 2 OFFSET 1").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(5, []byte("five"))).RowsWillBeClosed()
	mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT `id` AS `id`, `name` AS `name` FROM `orders` WHERE (`id` > ?)) AS `_resource_total`").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(8))
	result, err := connector.ExecuteQuery(context.Background(), resource, params)
	require.NoError(t, err)
	assert.Equal(t, int64(8), result.Total)
	assert.Equal(t, []map[string]any{{"id": int64(5), "name": "five"}}, result.Entries)
}

func TestMariaDBConnectorBuildTypedSelectBuilder(t *testing.T) {
	connector := &MariaDBConnector{}
	resource := &interfaces.Resource{SourceIdentifier: "`orders`", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "created", Type: interfaces.DataType_Datetime}, {Name: "blob", Type: interfaces.DataType_Binary}}}
	t.Run("grouping having and metric sorting are typed", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "n"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 2}, Sort: []*interfaces.SortField{{Field: "n", Direction: interfaces.DESC_DIRECTION}}, Paging: interfaces.PagingRequest{Limit: 3}}
		query, err := connector.buildSelectBuilder(resource, params)
		require.NoError(t, err)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `id` AS `id`, COUNT(*) AS `n` FROM `orders` GROUP BY `id` HAVING (`n` > ?) ORDER BY `n` DESC LIMIT 3", text)
		assert.Equal(t, []any{2}, args)
	})
	t.Run("calendar projection and sorting use the same expression", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "created", CalendarInterval: interfaces.CALENDAR_UNIT_MONTH}}, Sort: []*interfaces.SortField{{Field: "created"}}, Paging: interfaces.PagingRequest{Limit: 2, Offset: 4}, CursorEncoded: "keyset"}
		query, err := connector.buildSelectBuilder(resource, params)
		require.NoError(t, err)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DATE_FORMAT(`created`, ?) AS `created` FROM `orders` GROUP BY DATE_FORMAT(`created`, ?) ORDER BY DATE_FORMAT(`created`, ?) ASC LIMIT 2", text)
		assert.Equal(t, []any{"%Y-%m", "%Y-%m", "%Y-%m"}, args)
	})
	t.Run("binary metadata does not fetch the value", func(t *testing.T) {
		mode := interfaces.BinaryModeMetadata
		query, err := connector.buildSelectBuilder(resource, &interfaces.ResourceDataQueryParams{OutputFields: []string{"blob"}, BinaryMode: &mode, Paging: interfaces.PagingRequest{Limit: 1}})
		require.NoError(t, err)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT OCTET_LENGTH(`blob`) AS `blob` FROM `orders` LIMIT 1", text)
		assert.Empty(t, args)
	})
}

func TestToSql(t *testing.T) {
	t.Run("clauses compose in SQL parameter order", func(t *testing.T) {
		query := sb.Select(&sb.Alias{Expr: &sb.Param{Value: "projection"}, Name: "label"}, &sb.Column{Qualifier: "a", Name: "id"}).
			From(&sb.Table{SourceIdentifier: "`sales.eu`.`order.items`", Alias: "a"}).
			LeftJoin(&sb.Table{SourceIdentifier: "`customers`", Alias: "b"}, &sb.Eq{Left: &sb.Column{Qualifier: "b", Name: "id"}, Right: &sb.Param{Value: "join"}}).
			Where(&sb.Gt{Left: &sb.Column{Qualifier: "a", Name: "id"}, Right: &sb.Param{Value: "where"}}).
			GroupBy(&sb.Column{Qualifier: "a", Name: "id"}).
			Having(&sb.Gt{Left: &sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}, Right: &sb.Param{Value: "having"}}).
			OrderBy(sb.Order{Expr: &sb.Param{Value: "order"}, Descending: true}).Limit(10).Offset(2)
		before, err := json.Marshal(query)
		require.NoError(t, err)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT ? AS `label`, `a`.`id` FROM `sales.eu`.`order.items` AS `a` LEFT JOIN `customers` AS `b` ON (`b`.`id` = ?) WHERE (`a`.`id` > ?) GROUP BY `a`.`id` HAVING (COUNT(*) > ?) ORDER BY ? DESC LIMIT 10 OFFSET 2", text)
		assert.Equal(t, []any{"projection", "join", "where", "having", "order"}, args)
		after, err := json.Marshal(query)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	t.Run("structured expressions compose without SQL fragments", func(t *testing.T) {
		amount := &sb.Cast{Expr: &sb.Case{Operand: &sb.Column{Name: "kind"}, WhenParts: []sb.When{{Condition: &sb.Param{Value: 1}, Result: &sb.Arithmetic{Left: &sb.Param{Value: "1.20"}, Op: sb.Add, Right: &sb.Param{Value: "0.30"}}}}, Else: &sb.Param{Value: nil}}, Type: sb.CastDecimal, Parameters: []uint64{12, 2}}
		query := sb.Select(&sb.Alias{Expr: amount, Name: "amount"}).From(&sb.Table{SourceIdentifier: "`orders`"}).
			Where(&sb.In{Expr: &sb.Column{Name: "id"}, Values: []sb.SqlNode{&sb.Param{Value: 1}, &sb.Param{Value: 2}}},
				&sb.Between{Expr: &sb.Column{Name: "cost"}, Lower: &sb.Param{Value: 3}, Upper: &sb.Param{Value: 4}},
				&sb.Like{Expr: &sb.Column{Name: "name"}, Pattern: &sb.Param{Value: "p%"}, Escape: new("!")},
				&sb.Contains{Expr: &sb.Column{Name: "note"}, Value: &sb.Param{Value: "_50%"}})
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT CAST(CASE `kind` WHEN ? THEN (? + ?) ELSE ? END AS DECIMAL(12,2)) AS `amount` FROM `orders` WHERE (`id` IN (?, ?)) AND (`cost` BETWEEN ? AND ?) AND (`name` LIKE ? ESCAPE ?) AND (`note` LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(?, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')", text)
		assert.Equal(t, []any{1, "1.20", "0.30", nil, 1, 2, 3, 4, "p%", "!", "_50%"}, args)
	})
	t.Run("chain methods modify the same builder", func(t *testing.T) {
		query := sb.Select(&sb.Column{Name: "id"})
		query.From(&sb.Table{SourceIdentifier: "`orders`"})
		query.Columns(&sb.Alias{Expr: &sb.Param{Value: "label"}, Name: "label"})
		query.Where(&sb.Gt{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 1}})
		query.Where(&sb.Lt{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 10}})
		query.GroupBy(&sb.Column{Name: "id"})
		query.Having(&sb.Param{Value: true})
		query.OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}})
		query.Distinct()
		query.Limit(2)
		query.Offset(1)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT `id`, ? AS `label` FROM `orders` WHERE (`id` > ?) AND (`id` < ?) GROUP BY `id` HAVING ? ORDER BY `id` ASC LIMIT 2 OFFSET 1", text)
		assert.Equal(t, []any{"label", 1, 10, true}, args)
		assert.Same(t, query, query.Having(nil).OrderBy())
		again, againArgs, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, text, again)
		assert.Equal(t, args, againArgs)
	})
	t.Run("method order follows SELECT clause semantics", func(t *testing.T) {
		condition := &sb.Eq{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 7}}
		first := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		first.Where(condition).Limit(2).Offset(1)
		second := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		second.Offset(1).Limit(2).Where(condition)
		left, leftArgs, err := ToSql(first)
		require.NoError(t, err)
		right, rightArgs, err := ToSql(second)
		require.NoError(t, err)
		assert.Equal(t, left, right)
		assert.Equal(t, leftArgs, rightArgs)
	})
	t.Run("subquery retains limit and lexical parameters", func(t *testing.T) {
		inner := sb.Select(&sb.Alias{Expr: &sb.Param{Value: "inner projection"}, Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}).
			Where(&sb.Eq{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: "inner filter"}}).OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}}).Limit(2)
		outer := sb.Select(&sb.Param{Value: "outer projection"}).FromSelect(inner, "q").Where(&sb.Eq{Left: &sb.Column{Qualifier: "q", Name: "id"}, Right: &sb.Param{Value: "outer filter"}})
		text, args, err := ToSql(outer)
		require.NoError(t, err)
		assert.Equal(t, "SELECT ? FROM (SELECT ? AS `id` FROM `orders` WHERE (`id` = ?) ORDER BY `id` ASC LIMIT 2) AS `q` WHERE (`q`.`id` = ?)", text)
		assert.Equal(t, []any{"outer projection", "inner projection", "inner filter", "outer filter"}, args)
	})
	t.Run("zero limit offset only and empty conditions", func(t *testing.T) {
		query := sb.Select(&sb.Param{Value: 1})
		query.Where(nil).Having(nil).Distinct().Limit(0)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ? LIMIT 0", text)
		assert.Equal(t, []any{1}, args)
		query.RemoveLimit().Offset(4)
		text, _, err = ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ? LIMIT 18446744073709551615 OFFSET 4", text)
		query.Limit(0).RemoveLimit().RemoveOffset()
		text, _, err = ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ?", text)
	})
	t.Run("malformed inputs produce no partial SQL", func(t *testing.T) {
		var missing *sb.Column
		var missingTable *sb.Table
		var missingCondition *sb.Eq
		var missingParam *sb.Param
		var missingAnd *sb.And
		for _, query := range []*sb.SelectBuilder{
			nil, {}, sb.Select(nil), sb.Select(missing), sb.Select(&sb.Star{}).From(&sb.Table{ResourceID: "unresolved"}),
			sb.Select(unknownNode{}),
			sb.Select(&sb.Star{}).From(missingTable),
			sb.Select(&sb.Param{Value: 1}).Where(missingCondition),
			sb.Select(missingParam),
			sb.Select(&sb.Param{Value: 1}).Having(missingAnd),
			sb.Select(&sb.Param{Value: 1}).OrderBy(sb.Order{Expr: missing}),
			sb.Select(&sb.Star{}).LeftJoin(&sb.Table{SourceIdentifier: "`b`"}, &sb.Param{Value: true}),
			sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).Join(&sb.Table{SourceIdentifier: "`b`"}, nil),
			{ColumnsPart: []sb.SqlNode{&sb.Column{Name: "id"}}, WherePart: []sb.SqlNode{nil}},
		} {
			text, args, err := ToSql(query)
			require.Error(t, err)
			assert.Empty(t, text)
			assert.Nil(t, args)
		}
		text, args, err := render(sb.Select(&sb.Param{Value: 1}).Where(&sb.Column{}))
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	})
	t.Run("join convenience methods retain their connection kinds", func(t *testing.T) {
		text, args, err := render(sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).
			Join(&sb.Table{SourceIdentifier: "`b`"}, &sb.Param{Value: 1}).
			RightJoin(&sb.Table{SourceIdentifier: "`c`"}, &sb.Param{Value: 2}))
		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM `a` INNER JOIN `b` ON ? RIGHT JOIN `c` ON ?", text)
		assert.Equal(t, []any{1, 2}, args)
	})
}

func TestTableToSql(t *testing.T) {
	text, args, err := render((&sb.Table{SourceIdentifier: "`a.b`.`x``y`", Alias: "q`r"}))
	require.NoError(t, err)
	assert.Equal(t, "`a.b`.`x``y` AS `q``r`", text)
	assert.Empty(t, args)
	text, args, err = render(&sb.Table{SourceIdentifier: "orders"})
	require.NoError(t, err)
	assert.Equal(t, "orders", text)
	assert.Empty(t, args)

}

func TestSubqueryToSql(t *testing.T) {
	inner := sb.Select(&sb.Param{Value: 7})
	outer := sb.Select(&sb.Star{}).FromSelect(inner, "q")
	assert.Same(t, inner, outer.FromPart.(*sb.Subquery).Query)
	inner.Where(&sb.Param{Value: true})
	text, args, err := ToSql(outer)
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM (SELECT ? WHERE ?) AS `q`", text)
	assert.Equal(t, []any{7, true}, args)
	for _, subquery := range []*sb.Subquery{{}, {Query: &sb.SelectBuilder{}}} {
		text, args, err := render(subquery)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
	invalid := sb.Select()
	text, args, err = render((&sb.Subquery{Query: invalid, Alias: "q"}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestJoinToSql(t *testing.T) {
	for _, tc := range []struct {
		kind    sb.JoinKind
		keyword string
	}{{sb.InnerJoin, "INNER JOIN"}, {sb.LeftJoin, "LEFT JOIN"}, {sb.RightJoin, "RIGHT JOIN"}, {sb.CrossJoin, "CROSS JOIN"}} {
		join := sb.Join{Kind: tc.kind, Source: &sb.Table{SourceIdentifier: "`b`"}}
		want := tc.keyword + " `b`"
		var expected []any
		if tc.kind != sb.CrossJoin {
			join.On = &sb.Param{Value: true}
			want += " ON ?"
			expected = []any{true}
		}
		text, args, err := render(&join)
		require.NoError(t, err)
		assert.Equal(t, want, text)
		assert.Equal(t, expected, args)
	}
	for _, join := range []sb.Join{{Kind: "invalid", Source: &sb.Table{SourceIdentifier: "`b`"}}, {Kind: sb.CrossJoin, Source: &sb.Table{SourceIdentifier: "`b`"}, On: &sb.Param{Value: true}}} {
		text, args, err := render(&join)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestJoinedTableToSql(t *testing.T) {
	source := &sb.JoinedTable{From: &sb.Table{SourceIdentifier: "`b`"}, Joins: []sb.Join{{Kind: sb.InnerJoin, Source: &sb.Table{SourceIdentifier: "`c`"}, On: &sb.Eq{Left: &sb.Column{Qualifier: "b", Name: "id"}, Right: &sb.Param{Value: 2}}}}}
	text, args, err := render(sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).LeftJoin(source, &sb.Eq{Left: &sb.Column{Qualifier: "a", Name: "id"}, Right: &sb.Param{Value: 3}}))
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM `a` LEFT JOIN (`b` INNER JOIN `c` ON (`b`.`id` = ?)) ON (`a`.`id` = ?)", text)
	assert.Equal(t, []any{2, 3}, args)
	text, args, err = render((&sb.JoinedTable{}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestOrderToSql(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order sb.Order
		want  string
		args  []any
	}{
		{"default asc", sb.Order{Expr: &sb.Column{Name: "id"}}, "`id` ASC", nil},
		{"default desc", sb.Order{Expr: &sb.Column{Name: "id"}, Descending: true}, "`id` DESC", nil},
		{"null first", sb.Order{Expr: &sb.Param{Value: 9}, Descending: true, Nulls: sb.NullsFirst}, "(? IS NULL) DESC, ? DESC", []any{9, 9}},
		{"null last", sb.Order{Expr: &sb.Param{Value: 9}, Nulls: sb.NullsLast}, "(? IS NULL) ASC, ? ASC", []any{9, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, args, err := render(&tc.order)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
			assert.Equal(t, tc.args, args)
		})
	}
	text, args, err := render((&sb.Order{Expr: &sb.Param{Value: 1}, Nulls: "invalid"}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestMariaDBConnectorExecuteSQLBuilder(t *testing.T) {
	for _, tc := range []struct {
		name, input, statement string
		args                   []driver.Value
	}{
		{"single table", "SELECT id,name FROM {{orders}} WHERE id>10 ORDER BY id LIMIT 2", "SELECT `id`, `name` FROM `orders` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 2", []driver.Value{int64(10)}},
		{"join", "SELECT a.id,b.name FROM {{orders}} a LEFT JOIN {{orders}} b ON a.id=b.id WHERE b.name='O''Reilly'", "SELECT `a`.`id`, `b`.`name` FROM `orders` AS `a` LEFT JOIN `orders` AS `b` ON (`a`.`id` = `b`.`id`) WHERE (`b`.`name` = ?)", []driver.Value{"O'Reilly"}},
		{"subquery limit", "SELECT q.id,q.name FROM (SELECT id,name FROM {{orders}} WHERE id>1 ORDER BY id LIMIT 2) q WHERE q.id<10", "SELECT `q`.`id`, `q`.`name` FROM (SELECT `id`, `name` FROM `orders` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 2) AS `q` WHERE (`q`.`id` < ?)", []driver.Value{int64(1), int64(10)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			connector := &MariaDBConnector{db: db, connected: true}
			query, err := compiler.ParseSQL(tc.input)
			require.NoError(t, err)
			// 模拟准备阶段填入已授权资源的 SourceIdentifier，字段保持解析结果。
			switch from := query.FromPart.(type) {
			case *sb.Table:
				from.SourceIdentifier = "`orders`"
			case *sb.Subquery:
				from.Query.FromPart.(*sb.Table).SourceIdentifier = "`orders`"
			}
			for _, join := range query.JoinsPart {
				join.Source.(*sb.Table).SourceIdentifier = "`orders`"
			}
			before, err := json.Marshal(query)
			require.NoError(t, err)
			mock.ExpectQuery(tc.statement).WithArgs(tc.args...).WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
				sqlmock.NewColumn("id").OfType("BIGINT", int64(0)),
				sqlmock.NewColumn("name").OfType("VARCHAR", ""),
			).AddRow(int64(11), []byte("alice")).AddRow(int64(12), nil)).RowsWillBeClosed()
			result, err := connector.ExecuteSQLBuilder(context.Background(), query)
			require.NoError(t, err)
			assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "name", Type: interfaces.DataType_String}}, result.Columns)
			assert.Equal(t, []map[string]any{{"id": int64(11), "name": "alice"}, {"id": int64(12), "name": nil}}, result.Entries)
			require.NotNil(t, result.TotalCount)
			assert.EqualValues(t, 2, *result.TotalCount)
			after, err := json.Marshal(query)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("generation and cancellation stop before connecting", func(t *testing.T) {
		connector := &MariaDBConnector{}
		result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select())
		assert.Nil(t, result)
		require.Error(t, err)
		assert.False(t, connector.connected)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = connector.ExecuteSQLBuilder(ctx, sb.Select(&sb.Param{Value: 1}))
		assert.Nil(t, result)
		assert.ErrorIs(t, err, context.Canceled)
		assert.False(t, connector.connected)
	})
	t.Run("empty results retain columns and zero returned count", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
		mock.ExpectQuery("SELECT `id` FROM `orders`").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("id").OfType("BIGINT", int64(0)))).RowsWillBeClosed()
		connector := &MariaDBConnector{db: db, connected: true}
		query := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		result, err := connector.ExecuteSQLBuilder(context.Background(), query)
		require.NoError(t, err)
		assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, result.Columns)
		assert.NotNil(t, result.Entries)
		assert.Empty(t, result.Entries)
		require.NotNil(t, result.TotalCount)
		assert.Zero(t, *result.TotalCount)
	})
	t.Run("duplicate columns do not overwrite object values", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
		mock.ExpectQuery("SELECT * FROM `orders`").WillReturnRows(sqlmock.NewRows([]string{"id", "id"}).AddRow(1, 2)).RowsWillBeClosed()
		connector := &MariaDBConnector{db: db, connected: true}
		result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`orders`"}))
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "duplicate column names")
	})
	for _, failure := range []struct {
		name       string
		duringRows bool
		cause      error
	}{
		{"query unknown column", false, &mysql.MySQLError{Number: 1054, Message: "private source detail"}},
		{"iteration unknown column", true, &mysql.MySQLError{Number: 1054, Message: "private source detail"}},
		{"query cancellation", false, context.Canceled},
		{"iteration cancellation", true, context.Canceled},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			expectation := mock.ExpectQuery("SELECT `id` FROM `orders`")
			if failure.duringRows {
				expectation.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).RowError(1, failure.cause)).RowsWillBeClosed()
			} else {
				expectation.WillReturnError(failure.cause)
			}
			connector := &MariaDBConnector{db: db, connected: true}
			result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}))
			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, failure.cause)
			var driverErr *mysql.MySQLError
			if errors.As(failure.cause, &driverErr) {
				var invalid *interfaces.SourceQueryInvalidParameterError
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
			}
		})
	}
}

func TestSQLBuilderArguments(t *testing.T) {
	args := []any{json.Number("1"), json.Number("18446744073709551615"), json.Number("1.2300"), json.Number("1e-3"), "1", nil}
	values, err := sqlBuilderArguments(args)
	require.NoError(t, err)
	assert.Equal(t, []any{int64(1), uint64(18446744073709551615), "1.2300", float64(0.001), "1", nil}, values)
	assert.Equal(t, json.Number("1"), args[0])
	_, err = sqlBuilderArguments([]any{json.Number("1e10000")})
	require.Error(t, err)
}

func TestMariaDBConnectorOpenSQLBuilder(t *testing.T) {
	t.Run("returns unread Rows and column metadata to caller", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		defer func() {
			mock.ExpectClose()
			assert.NoError(t, db.Close())
			assert.NoError(t, mock.ExpectationsWereMet())
		}()
		connector := &MariaDBConnector{db: db, connected: true}
		unread := errors.New("read happens only in caller")
		mock.ExpectQuery("SELECT `id` FROM `orders` WHERE (`id` > ?)").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("id").OfType("BIGINT", int64(0))).AddRow(2).RowError(0, unread)).RowsWillBeClosed()
		rows, columns, err := connector.OpenSQLBuilder(context.Background(), sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}).Where(&sb.Gt{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: json.Number("1")}}))
		require.NoError(t, err)
		assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: "integer"}}, columns)
		assert.False(t, rows.Next())
		assert.ErrorIs(t, rows.Err(), unread)
		defer func() { assert.NoError(t, rows.Close()) }()
	})
	t.Run("duplicate output columns close Rows before returning failure", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		defer func() {
			mock.ExpectClose()
			assert.NoError(t, db.Close())
			assert.NoError(t, mock.ExpectationsWereMet())
		}()
		connector := &MariaDBConnector{db: db, connected: true}
		mock.ExpectQuery("SELECT `id`, `id`").WillReturnRows(sqlmock.NewRows([]string{"id", "id"}).AddRow(1, 1)).RowsWillBeClosed()
		rows, columns, err := connector.OpenSQLBuilder(context.Background(), sb.Select(&sb.Column{Name: "id"}, &sb.Column{Name: "id"})) //nolint:rowserrcheck // 此失败路径返回 nil Rows，不能读取 Err。
		assert.Nil(t, rows)
		assert.Nil(t, columns)
		var invalid *interfaces.ConditionBuildError
		assert.ErrorAs(t, err, &invalid)
	})
}
