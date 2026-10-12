// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package opensearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestOpenSearchConnectorMetadataAndNew(t *testing.T) {
	connector := &OpenSearchConnector{}

	assert.Equal(t, interfaces.ConnectorTypeOpenSearch, connector.GetType())
	assert.Equal(t, interfaces.ConnectorTypeOpenSearch, connector.GetName())
	assert.Equal(t, interfaces.ConnectorModeLocal, connector.GetMode())
	assert.Equal(t, interfaces.ConnectorCategoryIndex, connector.GetCategory())
	assert.Equal(t, []string{"password"}, connector.GetSensitiveFields())

	assert.False(t, connector.GetEnabled())
	connector.SetEnabled(true)
	assert.True(t, connector.GetEnabled())
	fields := connector.GetFieldConfig()
	assert.Equal(t, map[string]interfaces.ConnectorFieldConfig{
		"host":           {Name: "主机地址", Type: "string", Description: "OpenSearch 服务器主机地址", Required: true, Encrypted: false},
		"port":           {Name: "端口号", Type: "integer", Description: "OpenSearch 服务器端口", Required: true, Encrypted: false},
		"username":       {Name: "用户名", Type: "string", Description: "认证用户名", Required: false, Encrypted: false},
		"password":       {Name: "密码", Type: "string", Description: "认证密码", Required: false, Encrypted: true},
		"index_patterns": {Name: "索引模式", Type: "array", Description: "索引匹配模式列表（可选，如 log-*）", Required: false, Encrypted: false},
	}, fields)
	require.Contains(t, fields, "password")
	assert.True(t, fields["password"].Encrypted)
	require.Contains(t, fields, "index_patterns")
	assert.False(t, fields["index_patterns"].Required)

	instance, err := connector.New(interfaces.ConnectorConfig{
		"host":           "127.0.0.1",
		"port":           9200,
		"username":       "admin",
		"password":       "secret",
		"index_patterns": []string{"log-*", "metric-*"},
	})
	require.NoError(t, err)
	require.IsType(t, &OpenSearchConnector{}, instance)
	osConnector := instance.(*OpenSearchConnector)
	require.NotNil(t, osConnector.Config)
	assert.Equal(t, "127.0.0.1", osConnector.Config.Host)
	assert.Equal(t, 9200, osConnector.Config.Port)
	assert.Equal(t, []string{"log-*", "metric-*"}, osConnector.Config.IndexPatterns)
	require.NoError(t, osConnector.Close(t.Context()))
	assert.Nil(t, osConnector.client)
}

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
		cfg := &interfaces.FilterCondCfg{Name: property.Name, ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const, Value: "prefix.*",
		}}
		condition, err := (&filter_condition.RegexCond{}).New(t.Context(), cfg,
			map[string]*interfaces.Property{property.Name: property})
		require.NoError(t, err)
		require.IsType(t, &filter_condition.RegexCond{}, condition)
	})

	t.Run("structured features reject scalar ranges", func(t *testing.T) {
		property := &interfaces.Property{Name: "features", Type: connector.MapType("rank_features")}
		cfg := &interfaces.FilterCondCfg{Name: property.Name, ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const, Value: []any{1, 10},
		}}
		condition, err := (&filter_condition.RangeCond{}).New(t.Context(), cfg,
			map[string]*interfaces.Property{property.Name: property})
		require.ErrorContains(t, err, "not a date/number field")
		assert.Nil(t, condition)
	})

	t.Run("unsupported vectors reject OpenSearch knn conditions", func(t *testing.T) {
		for _, nativeType := range []string{"dense_vector", "sparse_vector"} {
			property := &interfaces.Property{Name: "embedding", Type: connector.MapType(nativeType)}
			cfg := &interfaces.FilterCondCfg{Name: property.Name, ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const, Value: []float32{0.1, 0.2, 0.3},
			}}
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
			Name:      property.Name,
			Operation: filter_condition.OperationKnnVector,
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     []float32{0.1, 0.2, 0.3},
			},
		}

		condition, err := (&filter_condition.KnnVectorCond{}).New(context.Background(), cfg,
			map[string]*interfaces.Property{property.Name: property})

		require.NoError(t, err)
		require.IsType(t, &filter_condition.KnnVectorCond{}, condition)
		assert.Equal(t, property.Name, condition.(*filter_condition.KnnVectorCond).FilterFieldName)
		assert.Equal(t, cfg, condition.(*filter_condition.KnnVectorCond).Cfg)
	})
}
