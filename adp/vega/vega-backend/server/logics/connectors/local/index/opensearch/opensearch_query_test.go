// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package opensearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestNestedTermsSize(t *testing.T) {
	tests := []struct {
		name       string
		levelIndex int
		numLevels  int
		limit      int
		want       int
	}{
		{name: "single level default", levelIndex: 0, numLevels: 1, limit: 0, want: 10},
		{name: "single level uses limit", levelIndex: 0, numLevels: 1, limit: 7, want: 7},
		{name: "inner level uses limit", levelIndex: 1, numLevels: 2, limit: 5, want: 5},
		{name: "outer level minimum", levelIndex: 0, numLevels: 2, limit: 0, want: 1000},
		{name: "outer level scaled lower bound", levelIndex: 0, numLevels: 2, limit: 1, want: 100},
		{name: "outer level scaled upper bound", levelIndex: 0, numLevels: 2, limit: 200, want: 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nestedTermsSize(tt.levelIndex, tt.numLevels, tt.limit))
		})
	}
}

func TestOpenSearchApplyTermsOrderToGroupAggNode(t *testing.T) {
	conn := &OpenSearchConnector{}

	t.Run("applies key and metric order recursively", func(t *testing.T) {
		node := map[string]any{
			"terms": map[string]any{"field": "country"},
			"aggs": map[string]any{
				"group_by_city": map[string]any{
					"terms": map[string]any{"field": "city"},
					"aggs": map[string]any{
						"total": map[string]any{"sum": map[string]any{"field": "amount"}},
					},
				},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			Aggregation: &interfaces.Aggregation{Alias: "total"},
			Sort: []*interfaces.SortField{
				{Field: "country", Direction: "DESC"},
				{Field: "total", Direction: "sideways"},
				{Field: "city", Direction: "asc"},
			},
		}

		conn.applyTermsOrderToGroupAggNode(node, params, "total")

		assert.Equal(t, []map[string]any{{"_key": "desc"}}, node["terms"].(map[string]any)["order"])
		child := node["aggs"].(map[string]any)["group_by_city"].(map[string]any)
		assert.Equal(t, []map[string]any{{"total": "asc"}, {"_key": "asc"}}, child["terms"].(map[string]any)["order"])
	})

	t.Run("skips having filter child", func(t *testing.T) {
		node := map[string]any{
			"terms": map[string]any{"field": "country"},
			"aggs": map[string]any{
				"having_filter": map[string]any{"bucket_selector": map[string]any{}},
			},
		}

		conn.applyTermsOrderToGroupAggNode(node, &interfaces.ResourceDataQueryParams{
			Sort: []*interfaces.SortField{{Field: "country", Direction: "asc"}},
		}, "total")

		assert.Equal(t, []map[string]any{{"_key": "asc"}}, node["terms"].(map[string]any)["order"])
	})
}

func TestOpenSearchBuildHavingBucketSelector(t *testing.T) {
	conn := &OpenSearchConnector{}
	tests := []struct {
		name       string
		having     *interfaces.HavingClause
		wantScript string
	}{
		{name: "eq", having: &interfaces.HavingClause{Operation: "==", Value: 10}, wantScript: "params.total == 10"},
		{name: "gte", having: &interfaces.HavingClause{Operation: ">=", Value: 10}, wantScript: "params.total >= 10"},
		{name: "in", having: &interfaces.HavingClause{Operation: "in", Value: []any{"a", 2}}, wantScript: "['a', 2].contains(params.total.toString())"},
		{name: "not in", having: &interfaces.HavingClause{Operation: "not_in", Value: []any{"a", 2}}, wantScript: "!['a', 2].contains(params.total.toString())"},
		{name: "range", having: &interfaces.HavingClause{Operation: "range", Value: []any{1, 5}}, wantScript: "params.total >= 1 && params.total <= 5"},
		{name: "out range", having: &interfaces.HavingClause{Operation: "out_range", Value: []any{1, 5}}, wantScript: "params.total < 1 || params.total > 5"},
		{name: "unsupported", having: &interfaces.HavingClause{Operation: "like", Value: "x"}, wantScript: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conn.buildHavingBucketSelector(tt.having, "total")

			selector := got["bucket_selector"].(map[string]any)
			assert.Equal(t, map[string]any{"total": "total"}, selector["buckets_path"])
			assert.Equal(t, tt.wantScript, selector["script"].(map[string]any)["source"])
		})
	}
}

func TestFormatInValuesForScript(t *testing.T) {
	assert.Equal(t, "[]", formatInValuesForScript(nil))
	assert.Equal(t, "['a', 2, true]", formatInValuesForScript([]any{"a", 2, true}))
}

func TestOpenSearchFlattenNestedGroupByRowsAdditional(t *testing.T) {
	conn := &OpenSearchConnector{}

	t.Run("returns empty rows for malformed buckets", func(t *testing.T) {
		rows := conn.flattenNestedGroupByRows(map[string]any{"buckets": "bad"}, &interfaces.ResourceDataQueryParams{}, "__value")

		assert.Empty(t, rows)
	})

	t.Run("uses key_as_string and truncates by limit", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			Limit: 1,
			Aggregation: &interfaces.Aggregation{
				Alias: "__value",
			},
			GroupBy: []*interfaces.GroupByItem{
				{Property: "created_at"},
			},
		}
		rootAgg := map[string]any{
			"buckets": []any{
				map[string]any{"key_as_string": "2026-07-09", "__value": map[string]any{"value": float64(3)}},
				map[string]any{"key_as_string": "2026-07-10", "__value": map[string]any{"value": float64(4)}},
			},
		}

		rows := conn.flattenNestedGroupByRows(rootAgg, params, "__value")

		require.Len(t, rows, 1)
		assert.Equal(t, "2026-07-09", rows[0]["created_at"])
		assert.Equal(t, float64(3), rows[0]["__value"])
	})

	t.Run("returns parent row when child aggregation is missing", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{
			GroupBy: []*interfaces.GroupByItem{
				{Property: "country"},
				{Property: "city"},
			},
		}
		rootAgg := map[string]any{
			"buckets": []any{
				map[string]any{"key": "CN"},
			},
		}

		rows := conn.flattenNestedGroupByRows(rootAgg, params, "")

		require.Len(t, rows, 1)
		assert.Equal(t, "CN", rows[0]["country"])
		assert.NotContains(t, rows[0], "city")
	})
}

func TestOpenSearchBuildFieldMappingsAdditional(t *testing.T) {
	conn := &OpenSearchConnector{}

	t.Run("maps decimal vector geo and text keyword feature", func(t *testing.T) {
		properties, hasVector, err := conn.buildFieldMappings([]*interfaces.Property{
			{Name: "price", Type: interfaces.DataType_Decimal},
			{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{
				{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 3}},
			}},
			{Name: "location", Type: interfaces.DataType_Point},
			{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "raw", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 128}},
			}},
		})

		require.NoError(t, err)
		assert.True(t, hasVector)
		assert.Equal(t, "scaled_float", properties["price"].(map[string]any)["type"])
		assert.Equal(t, 1000000000000000000.0, properties["price"].(map[string]any)["scaling_factor"])
		assert.Equal(t, "knn_vector", properties["embedding"].(map[string]any)["type"])
		assert.Equal(t, 3, properties["embedding"].(map[string]any)["dimension"])
		assert.Equal(t, "geo_point", properties["location"].(map[string]any)["type"])
		bodyFields := properties["body"].(map[string]any)["fields"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "keyword", "ignore_above": 128}, bodyFields["raw"])
	})

	t.Run("rejects unsupported feature type with config", func(t *testing.T) {
		properties, hasVector, err := conn.buildFieldMappings([]*interfaces.Property{
			{Name: "name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureType: "unknown", Config: map[string]any{"x": 1}},
			}},
		})

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.False(t, hasVector)
		assert.ErrorContains(t, err, "unsupported feature type")
	})
}

func TestFlattenNestedGroupByRows_TwoDimensions(t *testing.T) {
	conn := &OpenSearchConnector{}
	params := &interfaces.ResourceDataQueryParams{
		Limit: 10,
		Aggregation: &interfaces.Aggregation{
			Property: "id",
			Aggr:     "count",
			Alias:    "__value",
		},
		GroupBy: []*interfaces.GroupByItem{
			{Property: "kn_id"},
			{Property: "module_type"},
		},
	}

	rootAgg := map[string]any{
		"buckets": []any{
			map[string]any{
				"key": "yzm_mock_system",
				"group_by_module_type": map[string]any{
					"buckets": []any{
						map[string]any{
							"key": "a",
							"__value": map[string]any{
								"value": float64(3),
							},
						},
						map[string]any{
							"key": "b",
							"__value": map[string]any{
								"value": float64(2),
							},
						},
					},
				},
			},
		},
	}

	rows := conn.flattenNestedGroupByRows(rootAgg, params, "__value")
	require.Len(t, rows, 2)

	assert.Equal(t, "yzm_mock_system", rows[0]["kn_id"])
	assert.Equal(t, "a", rows[0]["module_type"])
	assert.Equal(t, float64(3), rows[0]["__value"])
	assert.Equal(t, "yzm_mock_system", rows[1]["kn_id"])
	assert.Equal(t, "b", rows[1]["module_type"])
	assert.Equal(t, float64(2), rows[1]["__value"])
}
