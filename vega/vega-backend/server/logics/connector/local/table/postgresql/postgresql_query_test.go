package postgresql

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestPostgresqlConnectorExecuteRawSQLInvalidParameter(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	connector := &PostgresqlConnector{db: db, connected: true}
	driverErr := &pq.Error{Code: "42703", Message: `column "missing" does not exist`}
	mock.ExpectQuery("SELECT missing FROM orders").WillReturnError(driverErr)
	result, err := connector.ExecuteRawSQL(context.Background(), "SELECT missing FROM orders")
	assert.Nil(t, result)
	var invalid *interfaces.SourceQueryInvalidParameterError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
	assert.True(t, errors.Is(err, driverErr))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorExecuteQuery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		needTotal bool
		rowError  bool
	}{
		{name: "select references removed column"},
		{name: "row iteration returns unknown column", rowError: true},
		{name: "count query returns unknown column", needTotal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			connector := &PostgresqlConnector{db: db, connected: true}
			resource := &interfaces.Resource{SourceIdentifier: "\"public\".\"orders\"", SchemaDefinition: []*interfaces.Property{{Name: "missing"}}}
			params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 10}, NeedTotal: tc.needTotal}
			driverErr := &pq.Error{Code: "42703", Message: "private source column details"}
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
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	for _, tc := range []struct {
		name        string
		groups      []*interfaces.GroupByItem
		aggregation *interfaces.Aggregation
		having      *interfaces.HavingClause
		statement   string
		args        []driver.Value
		total       int64
	}{
		{name: "groups without a metric", groups: []*interfaces.GroupByItem{{Property: "label"}}, statement: `SELECT "label" AS "label" FROM "public"."orders" GROUP BY "label"`, total: 8},
		{name: "group count after HAVING", groups: []*interfaces.GroupByItem{{Property: "label"}}, aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "total"}, having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 2}, statement: `SELECT "label" AS "label", COUNT("id") AS "total" FROM "public"."orders" GROUP BY "label" HAVING COUNT("id") > $1`, args: []driver.Value{2}, total: 3},
		{name: "ungrouped aggregate is one result row", aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count"}, statement: `SELECT COUNT(*) AS "__value" FROM "public"."orders"`, total: 1},
		{name: "implicit count HAVING without grouping", having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 100}, statement: `SELECT COUNT(*) AS "__value" FROM "public"."orders" HAVING COUNT(*) > $1`, args: []driver.Value{100}, total: 0},
		{name: "timestamp max HAVING milliseconds", aggregation: &interfaces.Aggregation{Property: "created", Aggr: "max"}, having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: json.Number("1767225600000")}, statement: `SELECT MAX("created_at") AS "__value" FROM "public"."orders" HAVING MAX("created_at") > (to_timestamp($1::double precision / 1000.0) AT TIME ZONE current_setting('TimeZone'))`, args: []driver.Value{int64(1767225600000)}, total: 1},
		{name: "date min HAVING milliseconds", aggregation: &interfaces.Aggregation{Property: "day", Aggr: "min"}, having: &interfaces.HavingClause{Field: "__value", Operation: "==", Value: float64(1767225600000)}, statement: `SELECT MIN("day") AS "__value" FROM "public"."orders" HAVING MIN("day") = to_timestamp($1::double precision / 1000.0)`, args: []driver.Value{int64(1767225600000)}, total: 1},
		{name: "zoned max HAVING list", aggregation: &interfaces.Aggregation{Property: "zoned", Aggr: "max"}, having: &interfaces.HavingClause{Field: "__value", Operation: "in", Value: []any{int64(1767225600000), "2026-01-02T00:00:00Z"}}, statement: `SELECT MAX("zoned") AS "__value" FROM "public"."orders" HAVING MAX("zoned") IN (to_timestamp($1::double precision / 1000.0), $2)`, args: []driver.Value{int64(1767225600000), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}, total: 1},
		{name: "date min HAVING not in", aggregation: &interfaces.Aggregation{Property: "day", Aggr: "min"}, having: &interfaces.HavingClause{Field: "__value", Operation: "not_in", Value: []any{int64(1767225600000)}}, statement: `SELECT MIN("day") AS "__value" FROM "public"."orders" HAVING MIN("day") NOT IN (to_timestamp($1::double precision / 1000.0))`, args: []driver.Value{int64(1767225600000)}, total: 1},
		{name: "date min HAVING range", aggregation: &interfaces.Aggregation{Property: "day", Aggr: "min"}, having: &interfaces.HavingClause{Field: "__value", Operation: "range", Value: []any{int64(1767225600000), int64(1767312000000)}}, statement: `SELECT MIN("day") AS "__value" FROM "public"."orders" HAVING (MIN("day") >= to_timestamp($1::double precision / 1000.0) AND MIN("day") <= to_timestamp($2::double precision / 1000.0))`, args: []driver.Value{int64(1767225600000), int64(1767312000000)}, total: 1},
		{name: "date max HAVING out range", aggregation: &interfaces.Aggregation{Property: "day", Aggr: "max"}, having: &interfaces.HavingClause{Field: "__value", Operation: "out_range", Value: []any{int64(1767225600000), int64(1767312000000)}}, statement: `SELECT MAX("day") AS "__value" FROM "public"."orders" HAVING (MAX("day") < to_timestamp($1::double precision / 1000.0) OR MAX("day") > to_timestamp($2::double precision / 1000.0))`, args: []driver.Value{int64(1767225600000), int64(1767312000000)}, total: 1},
		{name: "timestamp min HAVING RFC3339", aggregation: &interfaces.Aggregation{Property: "created", Aggr: "min"}, having: &interfaces.HavingClause{Field: "__value", Operation: ">=", Value: "2026-01-01T00:00:00Z"}, statement: `SELECT MIN("created_at") AS "__value" FROM "public"."orders" HAVING MIN("created_at") >= $1::timestamp`, args: []driver.Value{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, total: 1},
		{name: "count HAVING beside date aggregate stays numeric", aggregation: &interfaces.Aggregation{Property: "day", Aggr: "min"}, having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 2}, statement: `SELECT MIN("day") AS "__value" FROM "public"."orders" HAVING COUNT(*) > $1`, args: []driver.Value{2}, total: 1},
	} {
		for _, needTotal := range []bool{false, true} {
			name := tc.name + " without total"
			if needTotal {
				name = tc.name + " with total"
			}
			t.Run(name, func(t *testing.T) {
				db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
				require.NoError(t, err)
				defer func() { _ = db.Close() }()
				db.SetMaxOpenConns(1)
				connector := &PostgresqlConnector{db: db, connected: true}
				resource := &interfaces.Resource{SourceIdentifier: `"public"."orders"`, SchemaDefinition: []*interfaces.Property{{Name: "label", Type: interfaces.DataType_String}, {Name: "id", Type: interfaces.DataType_Integer}, {Name: "created", OriginalName: "created_at", Type: interfaces.DataType_Datetime, OriginalType: "timestamp without time zone"}, {Name: "day", Type: interfaces.DataType_Date, OriginalType: "date"}, {Name: "zoned", Type: interfaces.DataType_Datetime, OriginalType: "timestamptz"}}}
				params := &interfaces.ResourceDataQueryParams{GroupBy: tc.groups, Aggregation: tc.aggregation, Having: tc.having, NeedTotal: needTotal, Paging: interfaces.PagingRequest{Offset: 10, Limit: 1}}
				// An empty page must still return the full aggregate total when requested.
				mock.ExpectQuery(tc.statement + " LIMIT 1 OFFSET 10").WithArgs(tc.args...).WillReturnRows(sqlmock.NewRows([]string{"value"})).RowsWillBeClosed()
				if needTotal {
					mock.ExpectQuery(`SELECT COUNT(*) FROM (` + tc.statement + `) AS _query_groups`).WithArgs(tc.args...).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.total))
				}
				result, err := connector.ExecuteQuery(context.Background(), resource, params)
				require.NoError(t, err)
				assert.Empty(t, result.Entries)
				wantTotal := int64(0)
				if needTotal {
					wantTotal = tc.total
				}
				assert.Equal(t, wantTotal, result.Total)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}

	t.Run("implicit count cannot overwrite the grouped __value field", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		connector := &PostgresqlConnector{db: db, connected: true}
		resource := &interfaces.Resource{SourceIdentifier: "orders", SchemaDefinition: []*interfaces.Property{{Name: "__value", Type: interfaces.DataType_String}}}
		result, err := connector.ExecuteQuery(context.Background(), resource, &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{{Property: "__value"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0},
		})
		require.ErrorContains(t, err, "aggregate alias conflicts with a group field")
		assert.Nil(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

}

func TestPostgresqlConnectorBuildPagedSQL(t *testing.T) {
	connector := &PostgresqlConnector{}
	assert.Equal(t,
		"SELECT * FROM (SELECT id FROM orders) AS _raw_query_page LIMIT 10 OFFSET 20",
		connector.BuildPagedSQL("SELECT id FROM orders", 20, 10),
	)
}

func TestPostgresqlConnectorBuildDateFormat(t *testing.T) {
	connector := &PostgresqlConnector{}
	tests := []struct {
		name     string
		interval string
		want     string
	}{
		{name: "minute", interval: interfaces.CALENDAR_UNIT_MINUTE, want: "to_char(date_trunc('minute',created_at),'YYYY-MM-DD HH24:MI')"},
		{name: "hour", interval: interfaces.CALENDAR_UNIT_HOUR, want: "to_char(date_trunc('hour',created_at),'YYYY-MM-DD HH24')"},
		{name: "day", interval: interfaces.CALENDAR_UNIT_DAY, want: "to_char(date_trunc('day',created_at),'YYYY-MM-DD')"},
		{name: "week", interval: interfaces.CALENDAR_UNIT_WEEK, want: "to_char(date_trunc('week',created_at),'IYYY-IW')"},
		{name: "month", interval: interfaces.CALENDAR_UNIT_MONTH, want: "to_char(date_trunc('month',created_at),'YYYY-MM')"},
		{name: "quarter", interval: interfaces.CALENDAR_UNIT_QUARTER, want: "to_char(date_trunc('quarter',created_at),'YYYY-\"Q\"Q')"},
		{name: "year", interval: interfaces.CALENDAR_UNIT_YEAR, want: "to_char(date_trunc('year',created_at),'YYYY')"},
		{name: "unknown", interval: "unknown", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.buildDateFormat("created_at", tt.interval))
		})
	}
}

func TestPostgresqlConnectorExecuteQueryQuotesCalendarIntervalIdentifiers(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	connector := &PostgresqlConnector{db: db, connected: true}
	resource := &interfaces.Resource{
		SourceIdentifier: "\"public\".\"events\"",
		SchemaDefinition: []*interfaces.Property{
			{Name: "createdAt", OriginalName: "createdAt"},
			{Name: "id", OriginalName: "id"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		GroupBy:     []*interfaces.GroupByItem{{Property: "createdAt", CalendarInterval: interfaces.CALENDAR_UNIT_DAY}},
		Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"},
		Sort:        []*interfaces.SortField{{Field: "createdAt", Direction: interfaces.ASC_DIRECTION}},
		Paging:      interfaces.PagingRequest{Limit: 20},
	}
	expectedQuery := "SELECT to_char(date_trunc('day',\"createdAt\"),'YYYY-MM-DD') AS \"createdAt\", COUNT(\"id\") AS \"__value\" " +
		"FROM \"public\".\"events\" GROUP BY to_char(date_trunc('day',\"createdAt\"),'YYYY-MM-DD') ORDER BY to_char(date_trunc('day',\"createdAt\"),'YYYY-MM-DD') ASC LIMIT 20 OFFSET 0"
	mock.ExpectQuery(expectedQuery).
		WillReturnRows(sqlmock.NewRows([]string{"createdAt", "__value"}).AddRow("2024-01-01", 1))

	result, err := connector.ExecuteQuery(context.Background(), resource, params)
	require.NoError(t, err)
	assert.Equal(t, []string{"createdAt", "__value"}, result.Columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorExecuteQueryAliasesGroupByFields(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	connector := &PostgresqlConnector{db: db, connected: true}
	resource := &interfaces.Resource{
		SourceIdentifier: "\"public\".\"events\"",
		SchemaDefinition: []*interfaces.Property{
			{Name: "groupKey", OriginalName: "group_key"},
			{Name: "id", OriginalName: "id"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		GroupBy:     []*interfaces.GroupByItem{{Property: "groupKey"}},
		Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"},
		Paging:      interfaces.PagingRequest{Limit: 20},
	}
	expectedQuery := "SELECT \"group_key\" AS \"groupKey\", COUNT(\"id\") AS \"__value\" " +
		"FROM \"public\".\"events\" GROUP BY \"group_key\" LIMIT 20 OFFSET 0"
	mock.ExpectQuery(expectedQuery).
		WillReturnRows(sqlmock.NewRows([]string{"groupKey", "__value"}).AddRow("priority", 1))

	result, err := connector.ExecuteQuery(context.Background(), resource, params)
	require.NoError(t, err)
	assert.Equal(t, []string{"groupKey", "__value"}, result.Columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorExecuteQueryConvertsAliasedTimezoneField(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	connector := &PostgresqlConnector{db: db, connected: true}
	resource := &interfaces.Resource{
		SourceIdentifier: "\"public\".\"events\"",
		SchemaDefinition: []*interfaces.Property{
			{Name: "createdAt", OriginalName: "created_at", OriginalType: "timestamptz"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Limit: 1}}
	utc := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT \"created_at\" AS \"createdAt\" FROM \"public\".\"events\" LIMIT 1 OFFSET 0").
		WillReturnRows(sqlmock.NewRows([]string{"createdAt"}).AddRow(utc))

	result, err := connector.ExecuteQuery(context.Background(), resource, params)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, utc.Local(), result.Entries[0]["createdAt"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresqlConnectorBuildHavingCondition(t *testing.T) {
	connector := &PostgresqlConnector{}
	for _, tc := range []struct {
		name, operation, sql string
		value                any
		args                 []any
	}{
		{"equal", "==", "COUNT(id) = ?", 10, []any{10}},
		{"quoted string stays bound", "!=", "COUNT(id) <> ?", "a' OR 1=1 --", []any{"a' OR 1=1 --"}},
		{"greater equal", ">=", "COUNT(id) >= ?", 3, []any{3}},
		{"string list", "in", "COUNT(id) IN (?,?)", []string{"a'", "b"}, []any{"a'", "b"}},
		{"not in", "not_in", "COUNT(id) NOT IN (?,?)", []any{1, 2}, []any{1, 2}},
		{"range", "range", "(COUNT(id) >= ? AND COUNT(id) <= ?)", []any{1, 3}, []any{1, 3}},
		{"out range", "out_range", "(COUNT(id) < ? OR COUNT(id) > ?)", []any{1, 3}, []any{1, 3}},
		{"empty in", "in", "1=0", []any{}, nil},
		{"empty not in", "not_in", "1=1", []any{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression, err := connector.buildHavingCondition(&interfaces.HavingClause{Field: "__value", Operation: tc.operation, Value: tc.value}, "COUNT(id)", nil)
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
		_, err := connector.buildHavingCondition(having, "COUNT(id)", nil)
		require.Error(t, err)
	}
}

func TestPostgresqlConvertValue(t *testing.T) {
	t.Run("postgresql convert value", func(t *testing.T) {
		utc := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
		converted := convertValue(utc, "created_at", map[string]string{
			"created_at": "timestamptz",
		}, false)

		require.IsType(t, time.Time{}, converted)
		assert.Equal(t, utc.Local(), converted)
		assert.Equal(t, "hello", convertValue([]byte("hello"), "name", map[string]string{"name": "varchar"}, false))
		assert.Equal(t, []byte("hello"), convertValue([]byte("hello"), "name", map[string]string{"name": "bytea"}, true))
		assert.Equal(t, int64(1), convertValue(int64(1), "count", map[string]string{"count": "int8"}, false))
		assert.Nil(t, convertValue(nil, "name", map[string]string{"name": "varchar"}, false))
	})
}

func TestPostgresqlConnectorConnectionString(t *testing.T) {
	t.Run("postgresql build conn string", func(t *testing.T) {
		connector := &PostgresqlConnector{
			config: &postgresqlConfig{
				Host:     "postgres",
				Port:     5432,
				Username: "user",
				Password: "pa ss",
				Database: "/app",
				Options: map[string]any{
					"search_path": "public",
					"sslmode":     "require",
				},
			},
		}

		got := connector.connectionString()

		assert.Contains(t, got, "postgres://user:pa%20ss@postgres:5432/app?")
		assert.Contains(t, got, "search_path=public")
		assert.Contains(t, got, "sslmode=require")

		connector.config.Options = nil
		assert.Contains(t, connector.connectionString(), "sslmode=disable")
	})
}
