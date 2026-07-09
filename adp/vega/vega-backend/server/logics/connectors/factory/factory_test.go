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

	"vega-backend/interfaces"
	"vega-backend/logics/connectors"
)

type fakeConnector struct {
	tp              string
	name            string
	mode            string
	category        string
	enabled         bool
	sensitiveFields []string
	fieldConfig     map[string]interfaces.ConnectorFieldConfig
	config          interfaces.ConnectorConfig
}

func newFakeConnector(tp, mode string, enabled bool) *fakeConnector {
	return &fakeConnector{
		tp:              tp,
		name:            tp,
		mode:            mode,
		category:        interfaces.ConnectorCategoryTable,
		enabled:         enabled,
		sensitiveFields: []string{"password"},
		fieldConfig: map[string]interfaces.ConnectorFieldConfig{
			"host":     {Name: "Host", Type: "string", Required: true},
			"password": {Name: "Password", Type: "string", Required: true, Encrypted: true},
		},
	}
}

func (f *fakeConnector) GetType() string {
	return f.tp
}

func (f *fakeConnector) GetName() string {
	return f.name
}

func (f *fakeConnector) GetMode() string {
	return f.mode
}

func (f *fakeConnector) GetCategory() string {
	return f.category
}

func (f *fakeConnector) GetEnabled() bool {
	return f.enabled
}

func (f *fakeConnector) SetEnabled(enabled bool) {
	f.enabled = enabled
}

func (f *fakeConnector) GetSensitiveFields() []string {
	return f.sensitiveFields
}

func (f *fakeConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return f.fieldConfig
}

func (f *fakeConnector) New(cfg interfaces.ConnectorConfig) (connectors.Connector, error) {
	return &fakeConnector{
		tp:              f.tp,
		name:            f.name,
		mode:            f.mode,
		category:        f.category,
		enabled:         f.enabled,
		sensitiveFields: f.sensitiveFields,
		fieldConfig:     f.fieldConfig,
		config:          cfg,
	}, nil
}

func (f *fakeConnector) Connect(ctx context.Context) error {
	return nil
}

func (f *fakeConnector) Ping(ctx context.Context) error {
	return nil
}

func (f *fakeConnector) Close(ctx context.Context) error {
	return nil
}

func (f *fakeConnector) TestConnection(ctx context.Context) error {
	return nil
}

func (f *fakeConnector) GetMetadata(ctx context.Context) (map[string]any, error) {
	return map[string]any{"type": f.tp}, nil
}

func TestConnectorFactoryInitLocalConnectors(t *testing.T) {
	cf := &ConnectorFactory{connectors: map[string]connectors.Connector{}}

	cf.InitLocalConnectors()

	assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMySQL)
	assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMariaDB)
	assert.Contains(t, cf.connectors, interfaces.ConnectorTypePostgreSQL)
	assert.Contains(t, cf.connectors, interfaces.ConnectorTypeOpenSearch)
	assert.Contains(t, cf.connectors, interfaces.ConnectorTypeAnyShare)
	assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeOracle)
}

func TestConnectorFactoryRegisterConnector(t *testing.T) {
	ctx := context.Background()
	local := newFakeConnector("localdb", interfaces.ConnectorModeLocal, false)
	cf := &ConnectorFactory{
		connectors: map[string]connectors.Connector{
			local.GetType(): local,
		},
	}

	t.Run("updates existing local connector enabled state", func(t *testing.T) {
		err := cf.RegisterConnector(ctx, local.GetType(), &interfaces.ConnectorType{
			Type:        local.GetType(),
			Name:        local.GetName(),
			Mode:        interfaces.ConnectorModeLocal,
			FieldConfig: local.GetFieldConfig(),
			Enabled:     true,
		})

		require.NoError(t, err)
		assert.True(t, local.GetEnabled())
	})

	t.Run("registers remote connector", func(t *testing.T) {
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
	ctx := context.Background()
	cf := &ConnectorFactory{
		connectors: map[string]connectors.Connector{
			"localdb": newFakeConnector("localdb", interfaces.ConnectorModeLocal, true),
			"remote":  newFakeConnector("remote", interfaces.ConnectorModeRemote, true),
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
}

func TestConnectorFactorySetEnabledCreateAndSensitiveFields(t *testing.T) {
	ctx := context.Background()
	local := newFakeConnector("localdb", interfaces.ConnectorModeLocal, false)
	cf := &ConnectorFactory{
		connectors: map[string]connectors.Connector{
			local.GetType(): local,
		},
	}

	instance, err := cf.CreateConnectorInstance(ctx, local.GetType(), interfaces.ConnectorConfig{"host": "db"})
	require.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "is disabled")

	require.NoError(t, cf.SetConnectorEnabled(ctx, local.GetType(), true))
	instance, err = cf.CreateConnectorInstance(ctx, local.GetType(), interfaces.ConnectorConfig{"host": "db"})
	require.NoError(t, err)
	require.IsType(t, &fakeConnector{}, instance)
	assert.Equal(t, "db", instance.(*fakeConnector).config["host"])

	assert.Equal(t, []string{"password"}, cf.GetSensitiveFields(local.GetType()))
	assert.Nil(t, cf.GetSensitiveFields("missing"))

	err = cf.SetConnectorEnabled(ctx, "missing", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	instance, err = cf.CreateConnectorInstance(ctx, "missing", nil)
	require.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "not found")
}
