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
	require.Contains(t, fields, "password")
	assert.True(t, fields["password"].Encrypted)
	require.Contains(t, fields, "index_pattern")
	assert.False(t, fields["index_pattern"].Required)

	instance, err := connector.New(interfaces.ConnectorConfig{
		"host":          "127.0.0.1",
		"port":          9200,
		"username":      "admin",
		"password":      "secret",
		"index_pattern": "log-*",
	})

	require.NoError(t, err)
	require.IsType(t, &OpenSearchConnector{}, instance)
	osConnector := instance.(*OpenSearchConnector)
	require.NotNil(t, osConnector.Config)
	assert.Equal(t, "127.0.0.1", osConnector.Config.Host)
	assert.Equal(t, 9200, osConnector.Config.Port)
	assert.Equal(t, "log-*", osConnector.Config.IndexPattern)

	require.NoError(t, osConnector.Close(t.Context()))
	assert.Nil(t, osConnector.client)
}

func TestOpenSearchConnectorMapType(t *testing.T) {
	connector := &OpenSearchConnector{}

	tests := []struct {
		name       string
		nativeType string
		want       string
	}{
		{
			name:       "text",
			nativeType: "text",
			want:       interfaces.DataType_Text,
		},
		{
			name:       "keyword",
			nativeType: "keyword",
			want:       interfaces.DataType_String,
		},
		{
			name:       "integer",
			nativeType: "long",
			want:       interfaces.DataType_Integer,
		},
		{
			name:       "unsigned integer",
			nativeType: "unsigned_long",
			want:       interfaces.DataType_UnsignedInteger,
		},
		{
			name:       "float",
			nativeType: "scaled_float",
			want:       interfaces.DataType_Float,
		},
		{
			name:       "json object",
			nativeType: "nested",
			want:       interfaces.DataType_Json,
		},
		{
			name:       "unknown",
			nativeType: "wildcard",
			want:       interfaces.DataType_Other,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.MapType(tt.nativeType))
		})
	}
}

func TestOpenSearchBuildFieldMappingsBasicTypes(t *testing.T) {
	connector := &OpenSearchConnector{}

	properties, hasVector, err := connector.buildFieldMappings([]*interfaces.Property{
		{Name: "id", Type: interfaces.DataType_Integer},
		{Name: "amount", Type: interfaces.DataType_Decimal},
		{Name: "last_login_time", Type: interfaces.DataType_Timestamp},
		{Name: "payload", Type: interfaces.DataType_Json},
		{Name: "embedding", Type: interfaces.DataType_Vector},
		{
			Name: "body",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{
				{
					FeatureName: "raw",
					FeatureType: interfaces.PropertyFeatureType_Keyword,
					Config:      map[string]any{"ignore_above": 256},
				},
			},
		},
	})

	require.NoError(t, err)
	assert.True(t, hasVector)
	assert.Equal(t, map[string]any{"type": "long"}, properties["id"])
	assert.Equal(t, map[string]any{"type": "date"}, properties["last_login_time"])
	assert.Equal(t, map[string]any{"type": "object"}, properties["payload"])

	decimal := properties["amount"].(map[string]any)
	assert.Equal(t, "scaled_float", decimal["type"])
	assert.Equal(t, 1000000000000000000.0, decimal["scaling_factor"])

	assert.Equal(t, map[string]any{"type": "knn_vector"}, properties["embedding"])

	body := properties["body"].(map[string]any)
	assert.Equal(t, "text", body["type"])
	assert.Equal(t, map[string]any{
		"raw": map[string]any{
			"type":         "keyword",
			"ignore_above": 256,
		},
	}, body["fields"])
}

func TestOpenSearchBuildFieldMappingsRejectsUnsupportedFeature(t *testing.T) {
	connector := &OpenSearchConnector{}

	properties, hasVector, err := connector.buildFieldMappings([]*interfaces.Property{
		{
			Name: "name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{
				{
					FeatureName: "bad",
					FeatureType: "unsupported",
					Config:      map[string]any{"x": true},
				},
			},
		},
	})

	require.Error(t, err)
	assert.Nil(t, properties)
	assert.False(t, hasVector)
	assert.Contains(t, err.Error(), "unsupported feature type")
}
