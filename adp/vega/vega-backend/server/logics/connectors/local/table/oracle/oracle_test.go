// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package oracle

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestOracleConnectorMetadata(t *testing.T) {
	connector := &OracleConnector{}

	assert.Equal(t, interfaces.ConnectorTypeOracle, connector.GetType())
	assert.Equal(t, interfaces.ConnectorTypeOracle, connector.GetName())
	assert.Equal(t, interfaces.ConnectorModeLocal, connector.GetMode())
	assert.Equal(t, interfaces.ConnectorCategoryTable, connector.GetCategory())
	assert.Equal(t, []string{"password"}, connector.GetSensitiveFields())

	assert.False(t, connector.GetEnabled())
	connector.SetEnabled(true)
	assert.True(t, connector.GetEnabled())

	fields := connector.GetFieldConfig()
	require.Contains(t, fields, "password")
	assert.True(t, fields["password"].Encrypted)
	assert.True(t, fields["password"].Required)
	require.Contains(t, fields, "schemas")
	assert.False(t, fields["schemas"].Required)
}

func TestOracleConnectorNew(t *testing.T) {
	builder := &OracleConnector{}

	t.Run("success", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         1521,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
			"schemas":      []string{"APP"},
			"options":      map[string]any{"ssl": "false"},
		})

		require.NoError(t, err)
		require.IsType(t, &OracleConnector{}, connector)

		oracleConnector := connector.(*OracleConnector)
		require.NotNil(t, oracleConnector.config)
		assert.Equal(t, "127.0.0.1", oracleConnector.config.Host)
		assert.Equal(t, 1521, oracleConnector.config.Port)
		assert.Equal(t, "ORCL", oracleConnector.config.ServiceName)
		assert.Equal(t, []string{"APP"}, oracleConnector.config.Schemas)
	})

	t.Run("rejects incomplete config", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host": "127.0.0.1",
			"port": 1521,
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "config is incomplete")
	})

	t.Run("rejects invalid port", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         PORT_MAX + 1,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "out of valid range")
	})

	t.Run("rejects long schema name", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"host":         "127.0.0.1",
			"port":         1521,
			"service_name": "ORCL",
			"username":     "system",
			"password":     "secret",
			"schemas":      []string{strings.Repeat("A", SCHEMA_NAME_MAX_LENGTH+1)},
		})

		require.Error(t, err)
		assert.Nil(t, connector)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})
}

func TestOracleConnectorMapType(t *testing.T) {
	connector := &OracleConnector{}

	tests := []struct {
		name       string
		nativeType string
		want       string
	}{
		{
			name:       "integer",
			nativeType: "integer",
			want:       "integer",
		},
		{
			name:       "decimal",
			nativeType: "number",
			want:       "decimal",
		},
		{
			name:       "datetime",
			nativeType: "timestamp with time zone",
			want:       "datetime",
		},
		{
			name:       "binary",
			nativeType: "blob",
			want:       "binary",
		},
		{
			name:       "unknown type",
			nativeType: "geometry",
			want:       "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, connector.MapType(tt.nativeType))
		})
	}
}
