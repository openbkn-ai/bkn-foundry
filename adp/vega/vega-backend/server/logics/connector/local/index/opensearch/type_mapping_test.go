// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

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
		{nativeType: "wildcard", want: interfaces.DataType_Other},
	}
	for _, test := range tests {
		t.Run(test.nativeType, func(t *testing.T) {
			assert.Equal(t, test.want, connector.MapType(test.nativeType))
		})
	}
}
