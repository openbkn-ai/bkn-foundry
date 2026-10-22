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
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
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
		defer func() { _ = rows.Close() }()

		got, err := ScanRows(rows)

		require.NoError(t, err)
		assert.Equal(t, []string{"id", "name"}, got.Columns)
		assert.Equal(t, int64(2), got.Total)
		assert.Equal(t, []map[string]any{
			{"id": int64(1), "name": "alice"},
			{"id": int64(2), "name": "bob"},
		}, got.Entries)
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
		defer func() { _ = rows.Close() }()

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

func TestNormalizeQueryResult(t *testing.T) {
	t.Run("JSON null normalizes without changing string content", func(t *testing.T) {
		for _, binaryMode := range []*string{nil, new(interfaces.BinaryModeContent)} {
			for _, outputFields := range [][]string{nil, {"payload", "label"}} {
				result := &interfaces.QueryResult{Entries: []map[string]any{
					{"payload": nil, "label": "null"},
					{"payload": "null", "label": "null"},
					{"payload": []byte(" \nnull\t"), "label": "null"},
					{"payload": `"null"`, "label": "null"},
					{"payload": `""`, "label": ""},
				}}
				resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}, {Name: "label", Type: interfaces.DataType_String}}}
				NormalizeQueryResult(result, resource, &interfaces.ResourceDataQueryParams{OutputFields: outputFields, BinaryMode: binaryMode})
				assert.Equal(t, []map[string]any{
					{"payload": nil, "label": "null"},
					{"payload": nil, "label": "null"},
					{"payload": nil, "label": "null"},
					{"payload": `"null"`, "label": "null"},
					{"payload": `""`, "label": ""},
				}, result.Entries)
			}
		}
	})
	metadata := interfaces.BinaryModeMetadata
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "id", Type: interfaces.DataType_Integer},
		{Name: "blob", Type: interfaces.DataType_Binary},
		{Name: "other", Type: interfaces.DataType_Other},
		{Name: "missing", Type: interfaces.DataType_Other},
	}}
	for _, tc := range []struct {
		name        string
		params      interfaces.ResourceDataQueryParams
		unavailable bool
	}{
		{name: "default detail", unavailable: true},
		{name: "explicit supported projection", params: interfaces.ResourceDataQueryParams{OutputFields: []string{"id", "other"}}},
		{name: "aggregate", params: interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count"}}},
		{name: "group", params: interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "id"}}}},
		{name: "having", params: interfaces.ResourceDataQueryParams{Having: &interfaces.HavingClause{Field: "count(*)"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &interfaces.QueryResult{Columns: []string{"id", "blob", "other"}, Total: 3, Entries: []map[string]any{
				{"id": 1, "blob": nil, "other": []any{1, 2}},
				{"id": 2, "blob": nil, "other": []any{}},
				{"id": 3, "blob": nil, "other": nil},
			}}
			NormalizeQueryResult(result, resource, &tc.params)
			for _, entry := range result.Entries {
				if tc.unavailable {
					assert.Equal(t, interfaces.ResourceValue{Mode: interfaces.ResourceValueModeUnavailable, Reason: "not_requested"}, entry["blob"])
				} else {
					assert.Nil(t, entry["blob"])
				}
				assert.NotContains(t, entry, "missing")
			}
			assert.Equal(t, []any{1, 2}, result.Entries[0]["other"])
			assert.Equal(t, []any{}, result.Entries[1]["other"])
			assert.Contains(t, result.Entries[2], "other")
			assert.Nil(t, result.Entries[2]["other"])
			assert.Equal(t, int64(3), result.Total)
		})
	}
	t.Run("missing Binary fields are not manufactured", func(t *testing.T) {
		result := &interfaces.QueryResult{Entries: []map[string]any{{}}}
		NormalizeQueryResult(result, resource, &interfaces.ResourceDataQueryParams{})
		assert.Empty(t, result.Entries[0])
		assert.Empty(t, result.Columns)
	})
	t.Run("returned binary NULL remains NULL when requested", func(t *testing.T) {
		result := &interfaces.QueryResult{Columns: []string{"blob"}, Entries: []map[string]any{{"blob": nil}}}
		NormalizeQueryResult(result, resource, &interfaces.ResourceDataQueryParams{BinaryMode: &metadata})
		assert.Contains(t, result.Entries[0], "blob")
		assert.Nil(t, result.Entries[0]["blob"])
	})
}

func TestValidateAggregateAlias(t *testing.T) {
	for _, tc := range []struct {
		name, alias, group string
		conflict           bool
	}{
		{"explicit collision", "label", "label", true},
		{"default collision", "__value", "__value", true},
		{"distinct columns", "total", "label", false},
		{"case-sensitive result names", "Label", "label", false},
		{"group only", "", "label", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAggregateAlias(tc.alias, []*interfaces.GroupByItem{{Property: tc.group}})
			if !tc.conflict {
				require.NoError(t, err)
				return
			}
			var conflict *interfaces.ConditionBuildError
			require.ErrorAs(t, err, &conflict)
			assert.Equal(t, "aggregate alias conflicts with a group field", conflict.Reason)
		})
	}
}

func TestQueryBinaryContentColumns(t *testing.T) {
	content, metadata := interfaces.BinaryModeContent, interfaces.BinaryModeMetadata
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{
		{Name: "payload", Type: interfaces.DataType_Binary},
		{Name: "label", Type: interfaces.DataType_String},
	}}
	for _, tc := range []struct {
		name   string
		params interfaces.ResourceDataQueryParams
		want   map[string]bool
	}{
		{"content detail", interfaces.ResourceDataQueryParams{BinaryMode: &content}, map[string]bool{"payload": true}},
		{"metadata detail", interfaces.ResourceDataQueryParams{BinaryMode: &metadata}, map[string]bool{}},
		{"unrequested detail", interfaces.ResourceDataQueryParams{}, map[string]bool{}},
		{"count alias matching binary", interfaces.ResourceDataQueryParams{BinaryMode: &content, Aggregation: &interfaces.Aggregation{Property: "*", Aggr: "count", Alias: "payload"}}, map[string]bool{}},
		{"group projection", interfaces.ResourceDataQueryParams{BinaryMode: &content, GroupBy: []*interfaces.GroupByItem{{Property: "payload"}}}, map[string]bool{"payload": true}},
		{"min binary", interfaces.ResourceDataQueryParams{BinaryMode: &content, Aggregation: &interfaces.Aggregation{Property: "payload", Aggr: "min", Alias: "first"}}, map[string]bool{"first": true}},
		{"max binary default alias", interfaces.ResourceDataQueryParams{BinaryMode: &content, Aggregation: &interfaces.Aggregation{Property: "payload", Aggr: "max"}}, map[string]bool{"__value": true}},
		{"min string with binary alias", interfaces.ResourceDataQueryParams{BinaryMode: &content, Aggregation: &interfaces.Aggregation{Property: "label", Aggr: "min", Alias: "payload"}}, map[string]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, QueryBinaryContentColumns(resource, &tc.params)) })
	}
}
