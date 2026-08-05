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
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}

		cf.InitLocalConnectors()

		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMySQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMariaDB)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypePostgreSQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeSQLServer)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeOpenSearch)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeAnyShare)
		assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeOracle)
	})
}

func TestConnectorFactoryRegisterAllConnectors(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	connectorTypeAccess := vmock.NewMockConnectorTypeAccess(ctrl)
	connectorTypeAccess.EXPECT().List(gomock.Any(), interfaces.ConnectorTypesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
	}).Return([]*interfaces.ConnectorType{
		{
			Type: "future-local",
			Name: "Future Local",
			Mode: interfaces.ConnectorModeLocal,
		},
		{
			Type:     "remote-api",
			Name:     "Remote API",
			Mode:     interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryAPI,
			Enabled:  true,
		},
	}, int64(2), nil)
	cf := &connectorFactory{
		cta:        connectorTypeAccess,
		connectors: map[string]interfaces.Connector{},
	}

	assert.NotPanics(t, func() {
		cf.RegisterAllConnectors(nil)
	})
	assert.NotContains(t, cf.connectors, "future-local")
	require.Contains(t, cf.connectors, "remote-api")
	assert.True(t, cf.connectors["remote-api"].GetEnabled())

	connector, err := cf.CreateConnectorInstance(context.Background(), "future-local", nil)
	require.Error(t, err)
	assert.Nil(t, connector)
	assert.ErrorIs(t, err, ErrConnectorUnavailable)
}

func TestConnectorFactoryRegisterConnector(t *testing.T) {
	ctx := context.Background()

	t.Run("updates existing local connector enabled state", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		local.EXPECT().GetFieldConfig().Return(testConnectorFieldConfig())
		local.EXPECT().SetEnabled(true)
		cf := &connectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
			},
		}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "localdb",
			Mode:        interfaces.ConnectorModeLocal,
			Category:    interfaces.ConnectorCategoryTable,
			FieldConfig: testConnectorFieldConfig(),
			Enabled:     true,
		})

		require.NoError(t, err)
	})

	t.Run("returns error instead of exiting for mismatched local field config", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		local.EXPECT().GetFieldConfig().Return(testConnectorFieldConfig())
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "localdb",
			Mode:        interfaces.ConnectorModeLocal,
			Category:    interfaces.ConnectorCategoryTable,
			FieldConfig: map[string]interfaces.ConnectorFieldConfig{"stale": {Type: "string"}},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "field config mismatch")
	})

	t.Run("rejects mode change for existing connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type: "localdb", Name: "localdb", Mode: interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryTable,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode mismatch")
		assert.Same(t, local, cf.connectors["localdb"])
	})

	t.Run("registers remote connector", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}

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
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}

		err := cf.RegisterConnector(ctx, "missing-local", &interfaces.ConnectorType{
			Type: "missing-local",
			Name: "Missing Local",
			Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not implemented")
		assert.ErrorIs(t, err, ErrConnectorUnavailable)
	})
}

func TestValidateConnectorRegistration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*interfaces.ConnectorType)
		field  string
	}{
		{name: "mode mismatch", mutate: func(ct *interfaces.ConnectorType) { ct.Mode = interfaces.ConnectorModeRemote }, field: "mode"},
		{name: "category mismatch", mutate: func(ct *interfaces.ConnectorType) { ct.Category = interfaces.ConnectorCategoryAPI }, field: "category"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			connector := vmock.NewMockConnector(ctrl)
			connector.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
			request := &interfaces.ConnectorType{
				Type: "localdb", Name: "Local DB", Mode: interfaces.ConnectorModeLocal,
				Category: interfaces.ConnectorCategoryTable,
			}
			test.mutate(request)

			err := validateConnectorRegistration("localdb", request, connector)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.field+" mismatch")
		})
	}

	t.Run("allows mutable name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)
		connector.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)

		err := validateConnectorRegistration("localdb", &interfaces.ConnectorType{
			Type: "localdb", Name: "Renamed Local DB", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		}, connector)

		require.NoError(t, err)
	})

	t.Run("rejects registration key mismatch", func(t *testing.T) {
		err := validateConnectorRegistration("localdb", &interfaces.ConnectorType{Type: "other"}, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "key mismatch")
	})
}

func TestConnectorFactoryResolveConnectorTypeRegistration(t *testing.T) {
	ctx := context.Background()

	t.Run("uses code definition for local connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		local.EXPECT().GetFieldConfig().Return(testConnectorFieldConfig())
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}
		request := &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "Local DB",
			Mode:        interfaces.ConnectorModeLocal,
			Category:    interfaces.ConnectorCategoryTable,
			FieldConfig: map[string]interfaces.ConnectorFieldConfig{"stale": {Type: "string"}},
			Enabled:     true,
		}

		got, err := cf.ResolveConnectorTypeRegistration(ctx, request)

		require.NoError(t, err)
		assert.NotSame(t, request, got)
		assert.Equal(t, interfaces.ConnectorModeLocal, got.Mode)
		assert.Equal(t, interfaces.ConnectorCategoryTable, got.Category)
		assert.Equal(t, testConnectorFieldConfig(), got.FieldConfig)
		assert.Contains(t, request.FieldConfig, "stale")
	})

	t.Run("supports mysql registration through mariadb implementation", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}
		cf.InitLocalConnectors()
		request := &interfaces.ConnectorType{
			Type: interfaces.ConnectorTypeMySQL, Name: "MySQL", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable, Enabled: true,
			FieldConfig: map[string]interfaces.ConnectorFieldConfig{"stale": {Type: "string"}},
		}

		resolved, err := cf.ResolveConnectorTypeRegistration(ctx, request)

		require.NoError(t, err)
		assert.Equal(t, "MySQL", resolved.Name)
		assert.Equal(t, cf.connectors[interfaces.ConnectorTypeMySQL].GetFieldConfig(), resolved.FieldConfig)
		require.NoError(t, cf.RegisterConnector(ctx, resolved.Type, resolved))
	})

	t.Run("rejects local connector missing from binary", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}

		got, err := cf.ResolveConnectorTypeRegistration(ctx, &interfaces.ConnectorType{
			Type: "future-local",
			Name: "Future Local",
			Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, ErrConnectorUnavailable)
	})

	t.Run("rejects non-local registration over local implementation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}
		request := &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "Remote API",
			Mode:        interfaces.ConnectorModeRemote,
			Category:    interfaces.ConnectorCategoryAPI,
			Endpoint:    "https://connector.example",
			FieldConfig: testConnectorFieldConfig(),
			Enabled:     true,
		}

		got, err := cf.ResolveConnectorTypeRegistration(ctx, request)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "mode mismatch")
	})

	t.Run("keeps non-local user definition", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}
		request := &interfaces.ConnectorType{
			Type:        "remote-api",
			Name:        "Remote API",
			Mode:        interfaces.ConnectorModeRemote,
			Category:    interfaces.ConnectorCategoryAPI,
			Endpoint:    "https://connector.example",
			FieldConfig: testConnectorFieldConfig(),
			Enabled:     true,
		}

		got, err := cf.ResolveConnectorTypeRegistration(ctx, request)

		require.NoError(t, err)
		assert.NotSame(t, request, got)
		assert.Equal(t, request, got)
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
		cf := &connectorFactory{
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
		cf := &connectorFactory{
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
		assert.ErrorIs(t, err, ErrConnectorUnavailable)

		got, err = cf.CreateConnectorInstance(ctx, "missing", nil)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "not found")
		assert.ErrorIs(t, err, ErrConnectorUnavailable)
	})
}

func testConnectorFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "Host", Type: "string", Required: true},
		"password": {Name: "Password", Type: "string", Required: true, Encrypted: true},
	}
}
