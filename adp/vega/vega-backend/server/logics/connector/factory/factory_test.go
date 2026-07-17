// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestConnectorFactoryInitLocalConnectors(t *testing.T) {
	t.Run("connector factory init local connectors", func(t *testing.T) {
		cf := &ConnectorFactory{connectors: map[string]interfaces.Connector{}}

		cf.InitLocalConnectors()

		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMySQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMariaDB)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypePostgreSQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeOpenSearch)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeAnyShare)
		assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeOracle)
	})
}

func TestConnectorFactoryRegisterConnector(t *testing.T) {
	ctx := context.Background()

	t.Run("updates existing local connector enabled state", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetFieldConfig().Return(testConnectorFieldConfig())
		local.EXPECT().SetEnabled(true)
		cf := &ConnectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
			},
		}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "localdb",
			Mode:        interfaces.ConnectorModeLocal,
			FieldConfig: testConnectorFieldConfig(),
			Enabled:     true,
		})

		require.NoError(t, err)
	})

	t.Run("registers remote connector", func(t *testing.T) {
		cf := &ConnectorFactory{connectors: map[string]interfaces.Connector{}}

		err := cf.RegisterConnector(ctx, "remote-api", &interfaces.ConnectorType{
			Type:     "remote-api",
			Name:     "Remote API",
			Mode:     interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryAPI,
			Enabled:  true,
		})

		require.NoError(t, err)
		require.Contains(t, cf.connectors, "remote-api")
		assert.Equal(t, interfaces.ConnectorModeRemote, cf.connectors["remote-api"].GetMode())
		assert.True(t, cf.connectors["remote-api"].GetEnabled())
	})

	t.Run("rejects unimplemented local connector", func(t *testing.T) {
		cf := &ConnectorFactory{connectors: map[string]interfaces.Connector{}}

		err := cf.RegisterConnector(ctx, "missing-local", &interfaces.ConnectorType{
			Type: "missing-local",
			Name: "Missing Local",
			Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not implemented")
	})
}

func TestConnectorFactoryDeleteConnector(t *testing.T) {
	t.Run("connector factory delete connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		local := vmock.NewMockConnector(ctrl)
		remote := vmock.NewMockConnector(ctrl)
		remote.EXPECT().GetMode().Return(interfaces.ConnectorModeRemote)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetName().Return("localdb").Times(2)
		cf := &ConnectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
				"remote":  remote,
			},
		}

		require.NoError(t, cf.DeleteConnector(ctx, "remote"))
		assert.NotContains(t, cf.connectors, "remote")

		err := cf.DeleteConnector(ctx, "localdb")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can not delete local connector")

		err = cf.DeleteConnector(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not implemented")
	})
}

func TestConnectorFactorySetEnabledCreateAndSensitiveFields(t *testing.T) {
	t.Run("connector factory set enabled create and sensitive fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		local := vmock.NewMockConnector(ctrl)
		instance := vmock.NewMockConnector(ctrl)
		cfg := interfaces.ConnectorConfig{"host": "db"}
		gomock.InOrder(
			local.EXPECT().GetEnabled().Return(false),
			local.EXPECT().SetEnabled(true),
			local.EXPECT().GetEnabled().Return(true),
			local.EXPECT().New(cfg).Return(instance, nil),
			local.EXPECT().GetSensitiveFields().Return([]string{"password"}),
		)
		cf := &ConnectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
			},
		}

		got, err := cf.CreateConnectorInstance(ctx, "localdb", cfg)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "is disabled")

		require.NoError(t, cf.SetConnectorEnabled(ctx, "localdb", true))
		got, err = cf.CreateConnectorInstance(ctx, "localdb", cfg)
		require.NoError(t, err)
		assert.Same(t, instance, got)

		assert.Equal(t, []string{"password"}, cf.GetSensitiveFields("localdb"))
		assert.Nil(t, cf.GetSensitiveFields("missing"))

		err = cf.SetConnectorEnabled(ctx, "missing", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not implemented")

		got, err = cf.CreateConnectorInstance(ctx, "missing", nil)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "not found")
	})
}

func testConnectorFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "Host", Type: "string", Required: true},
		"password": {Name: "Password", Type: "string", Required: true, Encrypted: true},
	}
}
