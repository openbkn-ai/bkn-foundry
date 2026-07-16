// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package remote

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestRemoteConnectorMetadataAndConfig(t *testing.T) {
	t.Run("remote connector metadata and config", func(t *testing.T) {
		connectorType := &interfaces.ConnectorType{
			Type:     "remote-api",
			Name:     "Remote API",
			Mode:     interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryAPI,
			FieldConfig: map[string]interfaces.ConnectorFieldConfig{
				"token": {Name: "Token", Type: "string", Required: true, Encrypted: true},
			},
			Enabled: true,
		}

		connector := NewRemoteConnector(connectorType)

		assert.Equal(t, "remote-api", connector.GetType())
		assert.Equal(t, "Remote API", connector.GetName())
		assert.Equal(t, interfaces.ConnectorModeRemote, connector.GetMode())
		assert.Equal(t, interfaces.ConnectorCategoryAPI, connector.GetCategory())
		assert.True(t, connector.GetEnabled())
		assert.Equal(t, []string{"password"}, connector.GetSensitiveFields())
		assert.Equal(t, connectorType.FieldConfig, connector.GetFieldConfig())

		connector.SetEnabled(false)
		assert.False(t, connector.GetEnabled())
	})
}

func TestRemoteConnectorNewAndLifecycle(t *testing.T) {
	t.Run("remote connector new and lifecycle", func(t *testing.T) {
		ctx := context.Background()
		builder := NewRemoteConnector(&interfaces.ConnectorType{
			Type:     "remote-api",
			Name:     "Remote API",
			Mode:     interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryAPI,
			Enabled:  true,
		})

		instance, err := builder.New(interfaces.ConnectorConfig{"endpoint": "http://remote"})
		require.NoError(t, err)
		require.IsType(t, &RemoteConnector{}, instance)

		remoteInstance := instance.(*RemoteConnector)
		assert.True(t, remoteInstance.GetEnabled())
		assert.Equal(t, "http://remote", remoteInstance.config["endpoint"])

		assert.NoError(t, remoteInstance.Connect(ctx))
		assert.NoError(t, remoteInstance.Ping(ctx))
		assert.NoError(t, remoteInstance.TestConnection(ctx))
		assert.NoError(t, remoteInstance.Close(ctx))

		metadata, err := remoteInstance.GetMetadata(ctx)
		require.NoError(t, err)
		assert.Nil(t, metadata)
	})
}
