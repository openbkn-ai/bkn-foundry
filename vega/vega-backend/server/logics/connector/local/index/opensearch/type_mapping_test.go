// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package opensearch

import (
	"context"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestOpenSearchConnectorMapType(t *testing.T) {
	connector := &OpenSearchConnector{}
	tests := []struct {
		nativeType string
		want       string
	}{
		{nativeType: "text", want: interfaces.DataType_Text},
		{nativeType: "keyword", want: interfaces.DataType_String},
		{nativeType: "long", want: interfaces.DataType_Integer},
		{nativeType: "unsigned_long", want: interfaces.DataType_UnsignedInteger},
		{nativeType: "scaled_float", want: interfaces.DataType_Float},
		{nativeType: "nested", want: interfaces.DataType_Json},
		{nativeType: "knn_vector", want: interfaces.DataType_Vector},
		{nativeType: "wildcard", want: interfaces.DataType_String},
		{nativeType: "constant_keyword", want: interfaces.DataType_String},
		{nativeType: "icu_collation_keyword", want: interfaces.DataType_String},
		{nativeType: "match_only_text", want: interfaces.DataType_Text},
		{nativeType: "flat_object", want: interfaces.DataType_Json},
		{nativeType: "integer_range", want: interfaces.DataType_Json},
		{nativeType: "float_range", want: interfaces.DataType_Json},
		{nativeType: "long_range", want: interfaces.DataType_Json},
		{nativeType: "double_range", want: interfaces.DataType_Json},
		{nativeType: "date_range", want: interfaces.DataType_Json},
		{nativeType: "ip_range", want: interfaces.DataType_Json},
		{nativeType: "rank_feature", want: interfaces.DataType_Float},
		{nativeType: "rank_features", want: interfaces.DataType_Json},
		{nativeType: "percolator", want: interfaces.DataType_Json},
		{nativeType: "join", want: interfaces.DataType_Json},
		{nativeType: "ip", want: interfaces.DataType_Other},
		{nativeType: "geo_point", want: interfaces.DataType_Other},
		{nativeType: "geo_shape", want: interfaces.DataType_Other},
		{nativeType: "dense_vector", want: interfaces.DataType_Other},
		{nativeType: "sparse_vector", want: interfaces.DataType_Json},
		{nativeType: "aggregate_metric_double", want: interfaces.DataType_Json},
		{nativeType: "shape", want: interfaces.DataType_Other},
		{nativeType: "double_precision", want: interfaces.DataType_Other},
		{nativeType: "alias", want: interfaces.DataType_Other},
		{nativeType: "unknown", want: interfaces.DataType_Other},
	}
	for _, test := range tests {
		t.Run(test.nativeType, func(t *testing.T) {
			assert.Equal(t, test.want, connector.MapType(test.nativeType))
		})
	}

	t.Run("wildcard accepts regex conditions", func(t *testing.T) {
		property := &interfaces.Property{Name: "name", Type: connector.MapType("wildcard")}
		cfg := &interfaces.FilterCondCfg{Field: property.Name, ValueFrom: interfaces.ValueFrom_Const, Value: "prefix.*"}
		condition, err := (&filter_condition.RegexCond{}).New(t.Context(), cfg,
			map[string]*interfaces.Property{property.Name: property})
		require.NoError(t, err)
		require.IsType(t, &filter_condition.RegexCond{}, condition)
	})

	t.Run("structured features reject scalar ranges", func(t *testing.T) {
		property := &interfaces.Property{Name: "features", Type: connector.MapType("rank_features")}
		cfg := &interfaces.FilterCondCfg{Field: property.Name, ValueFrom: interfaces.ValueFrom_Const, Value: []any{1, 10}}
		condition, err := (&filter_condition.RangeCond{}).New(t.Context(), cfg,
			map[string]*interfaces.Property{property.Name: property})
		require.ErrorContains(t, err, "not a date/number field")
		assert.Nil(t, condition)
	})

	t.Run("unsupported vectors reject OpenSearch knn conditions", func(t *testing.T) {
		for _, nativeType := range []string{"dense_vector", "sparse_vector"} {
			property := &interfaces.Property{Name: "embedding", Type: connector.MapType(nativeType)}
			cfg := &interfaces.FilterCondCfg{Field: property.Name, ValueFrom: interfaces.ValueFrom_Const, Value: []float32{0.1, 0.2, 0.3}}
			condition, err := (&filter_condition.KnnVectorCond{}).New(t.Context(), cfg,
				map[string]*interfaces.Property{property.Name: property})
			require.ErrorContains(t, err, "type must be vector", nativeType)
			assert.Nil(t, condition, nativeType)
		}
	})

	t.Run("discovered knn vector supports vector conditions", func(t *testing.T) {
		property := &interfaces.Property{
			Name:         "embedding",
			Type:         connector.MapType("knn_vector"),
			OriginalType: "knn_vector",
		}
		cfg := &interfaces.FilterCondCfg{
			Field:     property.Name,
			Operation: filter_condition.OperationKnnVector,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2, 0.3},
		}

		condition, err := (&filter_condition.KnnVectorCond{}).New(context.Background(), cfg,
			map[string]*interfaces.Property{property.Name: property})

		require.NoError(t, err)
		require.IsType(t, &filter_condition.KnnVectorCond{}, condition)
		assert.Equal(t, property.Name, condition.(*filter_condition.KnnVectorCond).FilterFieldName)
		assert.Equal(t, cfg, condition.(*filter_condition.KnnVectorCond).Cfg)
	})
}

func TestOpenSearchConnectorIsFilterable(t *testing.T) {
	connector := &OpenSearchConnector{}

	for _, tc := range []struct {
		nativeType string
		want       bool
	}{
		{"keyword", true},
		{"text", true},
		{"long", true},
		{"unsigned_long", true},
		{"scaled_float", true},
		{"boolean", true},
		{"date_nanos", true},
		{"knn_vector", true},
		{"binary", false},
		{"nested", false},
		{"integer_range", false},
		{"sparse_vector", false},
		{"completion", false},
		{"murmur3", false},
		{"rank_feature", false},
		{"alias", false},
		{"ip", false},
		{"geo_point", false},
		{"dense_vector", false},
		{"future_type", false},
		{"", false},
	} {
		t.Run(tc.nativeType, func(t *testing.T) {
			assert.Equal(t, tc.want, connector.IsFilterable(&interfaces.IndexFieldMeta{Type: tc.nativeType}))
		})
	}
	t.Run("uses complete mapping attributes and leaves metadata unchanged", func(t *testing.T) {
		assert.False(t, connector.IsFilterable(nil))
		for _, tc := range []struct {
			name  string
			field interfaces.IndexFieldMeta
			want  bool
		}{
			{name: "unindexed text", field: interfaces.IndexFieldMeta{Type: "text", Attributes: map[string]any{"index": false}}},
			{name: "text ignores doc values", field: interfaces.IndexFieldMeta{Type: "text", Attributes: map[string]any{"index": false, "doc_values": true}}},
			{name: "keyword doc values", field: interfaces.IndexFieldMeta{Type: "keyword", Attributes: map[string]any{"index": false}}, want: true},
			{name: "keyword neither indexed nor doc values", field: interfaces.IndexFieldMeta{Type: "keyword", Attributes: map[string]any{"index": false, "doc_values": false}}},
			{name: "numeric doc values", field: interfaces.IndexFieldMeta{Type: "long", Attributes: map[string]any{"index": false, "doc_values": true}}, want: true},
			{name: "numeric no query path", field: interfaces.IndexFieldMeta{Type: "long", Attributes: map[string]any{"index": false, "doc_values": false}}},
			{name: "date doc values", field: interfaces.IndexFieldMeta{Type: "date_nanos", Attributes: map[string]any{"index": false}}, want: true},
			{name: "boolean doc values", field: interfaces.IndexFieldMeta{Type: "boolean", Attributes: map[string]any{"index": false}}, want: true},
			{name: "vector has no doc value filter path", field: interfaces.IndexFieldMeta{Type: "knn_vector", Attributes: map[string]any{"index": false, "doc_values": true}}},
			{name: "indexed without doc values", field: interfaces.IndexFieldMeta{Type: "keyword", Attributes: map[string]any{"doc_values": false}}, want: true},
			{name: "disabled mapping", field: interfaces.IndexFieldMeta{Type: "keyword", Attributes: map[string]any{"enabled": false}}},
			{name: "searchable child does not enable parent", field: interfaces.IndexFieldMeta{Type: "text", Attributes: map[string]any{"index": false}, SubFields: []interfaces.IndexSubFieldMeta{{Name: "raw", Type: "keyword"}}}},
			{name: "keyword child unsearchable", field: interfaces.IndexFieldMeta{Type: "text", Attributes: map[string]any{"index": false}, SubFields: []interfaces.IndexSubFieldMeta{{Name: "raw", Type: "keyword", Attributes: map[string]any{"index": false, "doc_values": false}}}}},
			{name: "unsearchable child does not disable parent", field: interfaces.IndexFieldMeta{Type: "text", SubFields: []interfaces.IndexSubFieldMeta{{Name: "raw", Type: "keyword", Attributes: map[string]any{"index": false, "doc_values": false}}}}, want: true},
			{name: "independent searchable child", field: interfaces.IndexFieldMeta{Name: "title.raw", Type: "keyword", Attributes: map[string]any{"index": false}}, want: true},
			{name: "independent unsearchable child", field: interfaces.IndexFieldMeta{Name: "title.raw", Type: "keyword", Attributes: map[string]any{"index": false, "doc_values": false}}},
			{name: "only resolved alias type", field: interfaces.IndexFieldMeta{Type: "alias", ResolvedType: "keyword"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, stored := range []bool{false, true} {
					tc.field.Filterable = &stored
					before, err := sonic.Marshal(tc.field)
					require.NoError(t, err)
					assert.Equal(t, tc.want, connector.IsFilterable(&tc.field))
					after, err := sonic.Marshal(tc.field)
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after))
					assert.Equal(t, stored, *tc.field.Filterable)
				}
			})
		}
	})
}
