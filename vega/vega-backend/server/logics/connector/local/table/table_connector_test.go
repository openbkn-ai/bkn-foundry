// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package table

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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
		assert.Equal(t, int64(2), got.TotalCount)
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
				result := &interfaces.ResourceDataQueryResult{Entries: []map[string]any{
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
			result := &interfaces.ResourceDataQueryResult{Columns: []string{"id", "blob", "other"}, TotalCount: 3, Entries: []map[string]any{
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
			assert.Equal(t, int64(3), result.TotalCount)
		})
	}
	t.Run("missing Binary fields are not manufactured", func(t *testing.T) {
		result := &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{}}}
		NormalizeQueryResult(result, resource, &interfaces.ResourceDataQueryParams{})
		assert.Empty(t, result.Entries[0])
		assert.Empty(t, result.Columns)
	})
	t.Run("returned binary NULL remains NULL when requested", func(t *testing.T) {
		result := &interfaces.ResourceDataQueryResult{Columns: []string{"blob"}, Entries: []map[string]any{{"blob": nil}}}
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

func TestScanSQLRow(t *testing.T) {
	t.Run("preserves binary and decimal text across retained reads", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("SELECT payload,amount,note").WillReturnRows(sqlmock.NewRows([]string{"payload", "amount", "note"}).
			AddRow([]byte{0, 255}, []byte("12345678901234567890.123400"), []byte("text")).AddRow([]byte{}, nil, nil)).RowsWillBeClosed()
		rows, err := db.Query("SELECT payload,amount,note")
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		columns := []interfaces.ColumnInfo{{Name: "payload", Type: interfaces.DataType_Binary}, {Name: "amount", Type: interfaces.DataType_Decimal}, {Name: "note", Type: interfaces.DataType_String}}
		first, err := ScanSQLRow(rows, columns)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"payload": []byte{0, 255}, "amount": "12345678901234567890.123400", "note": "text"}, first)
		second, err := ScanSQLRow(rows, columns)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"payload": []byte{}, "amount": nil, "note": nil}, second)
		end, err := ScanSQLRow(rows, columns)
		require.NoError(t, err)
		assert.Nil(t, end)
		require.NoError(t, rows.Err())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("propagates source iteration errors", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		cause := errors.New("source result interrupted")
		mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, cause)).RowsWillBeClosed()
		rows, err := db.Query("SELECT id")
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		row, err := ScanSQLRow(rows, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}})
		require.ErrorIs(t, err, cause)
		assert.Nil(t, row)
		require.ErrorIs(t, rows.Err(), cause)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestQuerySortFields(t *testing.T) {
	t.Run("resolves aggregate placeholder without mutating input", func(t *testing.T) {
		original := &interfaces.SortField{Field: "__value", Direction: interfaces.DESC_DIRECTION}
		params := &interfaces.ResourceDataQueryParams{Sort: []*interfaces.SortField{original}, GroupBy: []*interfaces.GroupByItem{{Property: "key"}, {Property: "secondary"}}}
		fields := QuerySortFields(params, "total")
		assert.Equal(t, []*interfaces.SortField{{Field: "total", Direction: interfaces.DESC_DIRECTION}, {Field: "key", Direction: interfaces.ASC_DIRECTION}, {Field: "secondary", Direction: interfaces.ASC_DIRECTION}}, fields)
		assert.Equal(t, "__value", original.Field)
		require.Len(t, params.Sort, 1)
		assert.NotSame(t, original, fields[0])
	})
	t.Run("retains explicit group ordering and appends only remaining keys", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Sort: []*interfaces.SortField{{Field: "secondary", Direction: interfaces.DESC_DIRECTION}}, GroupBy: []*interfaces.GroupByItem{{Property: "key"}, {Property: "secondary"}}}
		assert.Equal(t, []*interfaces.SortField{{Field: "secondary", Direction: interfaces.DESC_DIRECTION}, {Field: "key", Direction: interfaces.ASC_DIRECTION}}, QuerySortFields(params, ""))
	})
	t.Run("detail field named value stays a source field", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Sort: []*interfaces.SortField{{Field: "__value", Direction: interfaces.ASC_DIRECTION}}}
		assert.Equal(t, params.Sort, QuerySortFields(params, ""))
	})
	t.Run("group-only queries default to declared group order", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}, {Property: "secondary"}}}
		assert.Equal(t, []*interfaces.SortField{{Field: "key", Direction: interfaces.ASC_DIRECTION}, {Field: "secondary", Direction: interfaces.ASC_DIRECTION}}, QuerySortFields(params, ""))
	})
}

func TestValidateFilterability(t *testing.T) {
	flag := func(value bool) *bool { return &value }
	t.Run("closed flags do not invoke connector capability checks", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "off", Filterable: flag(false)}, {Name: "legacy"}}}
		require.NoError(t, ValidateFilterability(resource, connector, nil))
	})
	t.Run("uses externally parsed columns without decoding source metadata", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		column := interfaces.TableColumnMeta{Name: "native_id", Type: "NUMBER", NumPrecision: 38, NumScale: 6, Nullable: true, Filterable: flag(false)}
		columns := map[string]interfaces.TableColumnMeta{column.Name: column}
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": "not read by validator"}, SchemaDefinition: []*interfaces.Property{{Name: "public_id", OriginalName: "native_id", OriginalType: "stale", Filterable: flag(true)}, {Name: "closed", Filterable: flag(false)}, {Name: "legacy"}}}
		before, err := json.Marshal(resource)
		require.NoError(t, err)
		connector.EXPECT().IsFilterable(gomock.Eq(&column)).Return(true)
		require.NoError(t, ValidateFilterability(resource, connector, columns))
		after, err := json.Marshal(resource)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
		assert.Equal(t, column, columns[column.Name])
		assert.Same(t, column.Filterable, columns[column.Name].Filterable)
	})
	t.Run("missing source columns reject supported schema declarations", func(t *testing.T) {
		for _, tc := range []struct {
			name, originalName string
			columns            map[string]interfaces.TableColumnMeta
		}{
			{name: "missing metadata", originalName: "native_field"},
			{name: "empty columns", originalName: "native_field", columns: map[string]interfaces.TableColumnMeta{}},
			{name: "different source column", originalName: "native_field", columns: map[string]interfaces.TableColumnMeta{"other": {Name: "other", Type: "NATIVE"}}},
			{name: "property name without matching column", columns: map[string]interfaces.TableColumnMeta{"other": {Name: "other", Type: "NATIVE"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				connector := vmock.NewMockTableConnector(gomock.NewController(t))
				property := &interfaces.Property{Name: "field", OriginalName: tc.originalName, OriginalType: "NATIVE", Type: interfaces.DataType_String, Filterable: flag(true), Attributes: map[string]any{"type": "NATIVE", "num_precision": 12}}
				resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{property}}
				before, err := json.Marshal(resource)
				require.NoError(t, err)
				err = ValidateFilterability(resource, connector, tc.columns)
				var invalid *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &invalid)
				assert.Contains(t, invalid.Reason, "field")
				after, err := json.Marshal(resource)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
				assert.Same(t, property, resource.SchemaDefinition[0])
			})
		}
	})
	t.Run("missing native metadata and unsupported native types are request errors", func(t *testing.T) {
		for _, native := range []string{"", "unsupported"} {
			t.Run(map[bool]string{false: "unsupported", true: "missing native type"}[native == ""], func(t *testing.T) {
				connector := vmock.NewMockTableConnector(gomock.NewController(t))
				if native != "" {
					connector.EXPECT().IsFilterable(gomock.Any()).DoAndReturn(func(column *interfaces.TableColumnMeta) bool {
						assert.Equal(t, native, column.Type)
						return false
					})
				}
				err := ValidateFilterability(&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "public_field", Type: interfaces.DataType_Integer, OriginalType: "ignored", Filterable: flag(true)}}}, connector, map[string]interfaces.TableColumnMeta{"public_field": {Name: "public_field", Type: native}})
				var invalid *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &invalid)
				assert.Contains(t, invalid.Reason, "public_field")
			})
		}
	})
}

func TestColumnMetadata(t *testing.T) {
	t.Run("typed columns retain native attributes without JSON round trip", func(t *testing.T) {
		enabled := false
		column := interfaces.TableColumnMeta{Name: "FLAG", Type: "NUMBER", AliasType: "native_alias", Nullable: true, NumPrecision: 38, NumScale: 6, Filterable: &enabled}
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.TableColumnMeta{column}, "unrelated": make(chan int)}}
		got, err := ColumnMetadata(resource)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, map[string]interfaces.TableColumnMeta{"FLAG": column}) {
			t.Fatalf("column metadata = %#v", got)
		}
		if got["FLAG"].Filterable != column.Filterable {
			t.Fatal("typed metadata must retain the original flag pointer")
		}
		if enabled {
			t.Fatal("parsing must not change source flags")
		}
	})
	t.Run("persisted JSON restores full native column attributes", func(t *testing.T) {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []any{map[string]any{"name": "AMOUNT", "type": "NUMBER", "nullable": true, "num_precision": json.Number("38"), "num_scale": json.Number("6"), "filterable": false}}}}
		before, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ColumnMetadata(resource)
		if err != nil {
			t.Fatal(err)
		}
		column := got["AMOUNT"]
		if column.Name != "AMOUNT" || column.Type != "NUMBER" || !column.Nullable || column.NumPrecision != 38 || column.NumScale != 6 || column.Filterable == nil || *column.Filterable {
			t.Fatalf("restored column = %#v", column)
		}
		after, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("parsing must not change source metadata")
		}
	})
	t.Run("missing metadata is distinct from explicit empty columns", func(t *testing.T) {
		for _, resource := range []*interfaces.Resource{nil, {}, {SourceMetadata: map[string]any{}}} {
			got, err := ColumnMetadata(resource)
			if err != nil || got != nil {
				t.Fatalf("absent metadata = %#v, %v", got, err)
			}
		}
		for _, raw := range []any{[]interfaces.TableColumnMeta{}, []any{}} {
			got, err := ColumnMetadata(&interfaces.Resource{SourceMetadata: map[string]any{"columns": raw}})
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("empty columns = %#v, %v", got, err)
			}
		}
	})
	t.Run("corrupt stored columns return errors rather than missing metadata", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			raw  any
		}{
			{"explicit nil", nil}, {"typed nil", []interfaces.TableColumnMeta(nil)}, {"JSON null", json.RawMessage("null")},
			{"wrong shape", "corrupt"}, {"marshal failure", make(chan int)}, {"wrong native type", []any{map[string]any{"name": "field", "type": 4}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": tc.raw}}
				got, err := ColumnMetadata(resource)
				if err == nil || got != nil {
					t.Fatalf("corrupt metadata = %#v, %v", got, err)
				}
			})
		}
	})
}
