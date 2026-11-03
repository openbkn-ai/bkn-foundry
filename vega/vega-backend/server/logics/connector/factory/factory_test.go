// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/licverify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// metadataFilterConnector 验证 Oracle 转换签名接收到的源列元数据和原始条件。
type metadataFilterConnector struct {
	interfaces.TableConnector
	properties    []*interfaces.Property
	condition     interfaces.FilterCondition
	columns       map[string]interfaces.TableColumnMeta
	conversionErr error
}

func (c *metadataFilterConnector) ConvertFilterCondition(condition interfaces.FilterCondition, properties []*interfaces.Property, columns map[string]interfaces.TableColumnMeta) (sqlbuilder.SqlNode, error) {
	c.condition, c.properties, c.columns = condition, properties, columns
	return nil, c.conversionErr
}

func TestConnectorFactoryRegisterCoreLocalConnectors(t *testing.T) {
	t.Run("registers built-in local connectors directly with the factory", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)

		cf := &connectorFactory{
			connectors:                map[string]interfaces.Connector{},
			connectorRequiredEditions: map[string]licverify.Edition{},
		}

		cf.RegisterCoreLocalConnectors()

		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMySQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeMariaDB)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypePostgreSQL)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeOpenSearch)
		assert.Contains(t, cf.connectors, interfaces.ConnectorTypeAnyShare)
		assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeSQLServer)
		assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeOracle)
		assert.NotContains(t, cf.connectors, interfaces.ConnectorTypeDoris)
		assert.Equal(t, licverify.EditionCommunity, cf.connectorRequiredEditions[interfaces.ConnectorTypeMySQL])
		assert.NotSame(t, cf.connectors[interfaces.ConnectorTypeMySQL], cf.connectors[interfaces.ConnectorTypeMariaDB])
		assert.Empty(t, entitlement.Assembled())

		cf.SetConnectorEnabled(interfaces.ConnectorTypeMariaDB, true)
		cf.SetConnectorEnabled(interfaces.ConnectorTypeMySQL, false)

		assert.True(t, cf.connectors[interfaces.ConnectorTypeMariaDB].GetEnabled())
		assert.False(t, cf.connectors[interfaces.ConnectorTypeMySQL].GetEnabled())
	})
}

func TestConnectorFactoryApplyPersistedConnectorTypes(t *testing.T) {
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
		cta:                       connectorTypeAccess,
		connectors:                map[string]interfaces.Connector{},
		connectorRequiredEditions: map[string]licverify.Edition{},
	}

	assert.NotPanics(t, func() {
		cf.applyPersistedConnectorTypes()
	})
	assert.NotContains(t, cf.connectors, "future-local")
	require.Contains(t, cf.connectors, "remote-api")
	assert.True(t, cf.connectors["remote-api"].GetEnabled())

	connector, err := cf.CreateConnectorInstance(context.Background(), "future-local", nil)
	require.Error(t, err)
	assert.Nil(t, connector)
	assert.ErrorIs(t, err, ErrConnectorUnavailable)
}

func TestConnectorFactoryGetConnectorAvailability(t *testing.T) {
	cf := &connectorFactory{
		connectors: map[string]interfaces.Connector{
			"registered": nil,
		},
		connectorRequiredEditions: map[string]licverify.Edition{
			"sqlserver": licverify.EditionProfessional,
		},
	}

	registered := cf.GetConnectorAvailability("registered")
	assert.True(t, registered.Available)
	assert.Empty(t, registered.RequiredEdition)

	missing := cf.GetConnectorAvailability("missing")
	assert.False(t, missing.Available)
	assert.Empty(t, missing.RequiredEdition)
}

func TestConnectorFactoryPrivateConnectorUsesCurrentEntitlement(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)

	cf := &connectorFactory{
		connectors: map[string]interfaces.Connector{
			"sqlserver": nil,
		},
		connectorRequiredEditions: map[string]licverify.Edition{
			"sqlserver": licverify.EditionProfessional,
		},
	}

	availability := cf.GetConnectorAvailability("sqlserver")
	assert.False(t, availability.Available)
	assert.Equal(t, licverify.EditionProfessional, availability.RequiredEdition)
	connector, err := cf.CreateConnectorInstance(context.Background(), "sqlserver", nil)
	require.Error(t, err)
	assert.Nil(t, connector)
	assert.ErrorIs(t, err, ErrConnectorEntitlementDenied)

	_, err = cf.GetConnectorFieldConfig(context.Background(), &interfaces.ConnectorType{
		Type: "sqlserver",
		Mode: interfaces.ConnectorModeLocal,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConnectorEntitlementDenied)

	entitlement.SetGate(entitlement.FixedGate(licverify.EditionProfessional))
	assert.True(t, cf.GetConnectorAvailability("sqlserver").Available)
}

func TestConnectorFactoryRegisterConnector(t *testing.T) {
	ctx := context.Background()

	t.Run("updates existing local connector enabled state", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		local.EXPECT().SetEnabled(true)
		cf := &connectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
			},
		}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type:     "localdb",
			Name:     "localdb",
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
			Enabled:  true,
		})

		require.NoError(t, err)
	})

	t.Run("ignores stale local field config", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		local.EXPECT().SetEnabled(false)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}

		err := cf.RegisterConnector(ctx, "localdb", &interfaces.ConnectorType{
			Type:        "localdb",
			Name:        "localdb",
			Mode:        interfaces.ConnectorModeLocal,
			Category:    interfaces.ConnectorCategoryTable,
			FieldConfig: map[string]interfaces.ConnectorFieldConfig{"stale": {Type: "string"}},
		})

		require.NoError(t, err)
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

func TestConnectorFactoryValidateConnectorRegistration(t *testing.T) {
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

			cf := &connectorFactory{}
			err := cf.validateConnectorRegistration("localdb", request, connector)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.field+" mismatch")
		})
	}

	t.Run("allows mutable name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)
		connector.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)

		cf := &connectorFactory{}
		err := cf.validateConnectorRegistration("localdb", &interfaces.ConnectorType{
			Type: "localdb", Name: "Renamed Local DB", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		}, connector)

		require.NoError(t, err)
	})

	t.Run("rejects registration key mismatch", func(t *testing.T) {
		cf := &connectorFactory{}
		err := cf.validateConnectorRegistration("localdb", &interfaces.ConnectorType{Type: "other"}, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "key mismatch")
	})
}

func TestConnectorFactoryValidateConnectorTypeRegistration(t *testing.T) {

	t.Run("validates local registration", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		local.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"localdb": local}}
		request := &interfaces.ConnectorType{
			Type:     "localdb",
			Name:     "Local DB",
			Mode:     interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
			Enabled:  true,
		}

		err := cf.ValidateConnectorTypeRegistration(request)

		require.NoError(t, err)
	})

	t.Run("supports mysql registration through mariadb implementation", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)

		cf := &connectorFactory{
			connectors:                map[string]interfaces.Connector{},
			connectorRequiredEditions: map[string]licverify.Edition{},
		}
		cf.RegisterCoreLocalConnectors()
		request := &interfaces.ConnectorType{
			Type: interfaces.ConnectorTypeMySQL, Name: "MySQL", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable, Enabled: true,
		}

		err := cf.ValidateConnectorTypeRegistration(request)

		require.NoError(t, err)
		require.NoError(t, cf.RegisterConnector(context.Background(), request.Type, request))
	})

	t.Run("rejects local connector missing from binary", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}

		err := cf.ValidateConnectorTypeRegistration(&interfaces.ConnectorType{
			Type: "future-local",
			Name: "Future Local",
			Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
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

		err := cf.ValidateConnectorTypeRegistration(request)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "mode mismatch")
	})

	t.Run("reports unimplemented remote field definition", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}}
		request := &interfaces.ConnectorType{
			Type:     "remote-api",
			Name:     "Remote API",
			Mode:     interfaces.ConnectorModeRemote,
			Category: interfaces.ConnectorCategoryAPI,
			Endpoint: "https://connector.example",
			Enabled:  true,
		}

		got, err := cf.GetConnectorFieldConfig(context.Background(), request)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "not implemented")
	})
}

func TestConnectorFactoryDeleteConnector(t *testing.T) {
	t.Run("deletes remote connector and keeps local implementation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		local := vmock.NewMockConnector(ctrl)
		remote := vmock.NewMockConnector(ctrl)
		remote.EXPECT().GetMode().Return(interfaces.ConnectorModeRemote)
		local.EXPECT().GetMode().Return(interfaces.ConnectorModeLocal)
		cf := &connectorFactory{
			connectors: map[string]interfaces.Connector{
				"localdb": local,
				"remote":  remote,
			},
		}

		cf.DeleteConnector("remote")
		assert.NotContains(t, cf.connectors, "remote")

		cf.DeleteConnector("localdb")
		assert.Contains(t, cf.connectors, "localdb")

		cf.DeleteConnector("missing")
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
		assert.ErrorIs(t, err, ErrConnectorDisabled)

		cf.SetConnectorEnabled("localdb", true)
		got, err = cf.CreateConnectorInstance(ctx, "localdb", cfg)
		require.NoError(t, err)
		assert.Same(t, instance, got)

		assert.Equal(t, []string{"password"}, cf.GetSensitiveFields("localdb"))
		assert.Nil(t, cf.GetSensitiveFields("missing"))

		cf.SetConnectorEnabled("missing", true)

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

func TestConnectorFactoryValidateBuildSchema(t *testing.T) {
	t.Run("delegates without creating or connecting an instance", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "price", Type: interfaces.DataType_Decimal}}}
		want := errors.New("field price is unsupported")
		connector.EXPECT().GetEnabled().Return(true)
		connector.EXPECT().ValidateBuildSchema(resource).Return(want)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"custom-table": connector}}
		err := cf.ValidateBuildSchema("custom-table", resource)
		assert.ErrorIs(t, err, want)
	})
	t.Run("unavailable implementation", func(t *testing.T) {
		err := (&connectorFactory{}).ValidateBuildSchema("missing", &interfaces.Resource{})
		assert.ErrorIs(t, err, ErrConnectorUnavailable)
	})
	t.Run("disabled implementation", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		connector.EXPECT().GetEnabled().Return(false)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"custom-table": connector}}
		assert.ErrorIs(t, cf.ValidateBuildSchema("custom-table", &interfaces.Resource{}), ErrConnectorDisabled)
	})
	t.Run("edition rejected before schema validation", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"custom-table": connector}, connectorRequiredEditions: map[string]licverify.Edition{"custom-table": licverify.EditionEnterprise}}
		assert.ErrorIs(t, cf.ValidateBuildSchema("custom-table", &interfaces.Resource{}), ErrConnectorEntitlementDenied)
	})
	t.Run("delegates through the base connector interface", func(t *testing.T) {
		connector := vmock.NewMockConnector(gomock.NewController(t))
		connector.EXPECT().GetEnabled().Return(true)
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryFileset}
		connector.EXPECT().ValidateBuildSchema(resource).Return(nil)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"custom": connector}}
		require.NoError(t, cf.ValidateBuildSchema("custom", resource))
	})
}

func TestConnectorFactoryValidateFilterCondition(t *testing.T) {
	t.Run("uses actual table converter without creating a connection", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		for _, tc := range []struct {
			connector, unit string
			valid           bool
		}{
			{interfaces.ConnectorTypeMariaDB, "day", true},
			{interfaces.ConnectorTypeMySQL, "microsecond", true},
			{interfaces.ConnectorTypeMariaDB, "quarter", true},
			{interfaces.ConnectorTypeMariaDB, "days", false},
			{interfaces.ConnectorTypePostgreSQL, "days", true},
			{interfaces.ConnectorTypePostgreSQL, "weeks", true},
			{interfaces.ConnectorTypePostgreSQL, "microsecond", false},
			{interfaces.ConnectorTypePostgreSQL, "quarter", false},
			{interfaces.ConnectorTypeAnyShare, "day", false},
			{interfaces.ConnectorTypeOpenSearch, "day", true},
			{interfaces.ConnectorTypeOpenSearch, "microsecond", false},
		} {
			t.Run(tc.connector+"/"+tc.unit, func(t *testing.T) {
				cf.SetConnectorEnabled(tc.connector, true)
				cfg := &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationBefore, ValueFrom: interfaces.ValueFrom_Const, Value: []any{1, tc.unit}}
				cond, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"created": {Name: "created", Type: interfaces.DataType_Datetime}})
				require.NoError(t, err)
				for _, tree := range []interfaces.FilterCondition{
					cond,
					&filter_condition.AndCond{SubConds: []interfaces.FilterCondition{&filter_condition.OrCond{SubConds: []interfaces.FilterCondition{cond}}}},
				} {
					err = cf.ValidateFilterCondition(tc.connector, tree, nil, nil)
					if tc.valid {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
				}
			})
		}
	})
	t.Run("delegates non-before operators to the connector", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		cf.SetConnectorEnabled(interfaces.ConnectorTypeMariaDB, true)
		cfg := &interfaces.FilterCondCfg{Name: "content", Operation: filter_condition.OperationMatch, ValueFrom: interfaces.ValueFrom_Const, Value: "search"}
		condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"content": {Name: "content", Type: interfaces.DataType_Text}})
		require.NoError(t, err)
		err = cf.ValidateFilterCondition(interfaces.ConnectorTypeMariaDB, condition, nil, nil)
		var unsupported *interfaces.UnsupportedOperationError
		require.ErrorAs(t, err, &unsupported)
	})
	t.Run("passes index schema to the full converter", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		cf.SetConnectorEnabled(interfaces.ConnectorTypeOpenSearch, true)
		field := &interfaces.Property{Name: "content", OriginalName: "physical.content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 4}}}}
		source := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{field}}
		for _, value := range []string{"okay", "longer"} {
			cfg := &interfaces.FilterCondCfg{Name: field.Name, Operation: filter_condition.OperationEqual, ValueFrom: interfaces.ValueFrom_Const, Value: value}
			cond, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{field.Name: field})
			require.NoError(t, err)
			err = cf.ValidateFilterCondition(interfaces.ConnectorTypeOpenSearch, cond, source.SchemaDefinition, nil)
			if value == "okay" {
				require.NoError(t, err)
			} else {
				var limitErr *interfaces.KeywordLimitError
				require.ErrorAs(t, err, &limitErr)
				assert.Equal(t, 4, limitErr.Limit)
			}
		}
	})
	t.Run("passes all table inputs and preserves the converter error", func(t *testing.T) {
		builder := vmock.NewMockTableConnector(gomock.NewController(t))
		builder.EXPECT().GetEnabled().Return(true)
		want := errors.New("conversion failed")
		connector := &metadataFilterConnector{TableConnector: builder, conversionErr: want}
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"oracle": connector}}
		condition := &filter_condition.AndCond{}
		properties := []*interfaces.Property{{Name: "flag", OriginalType: "NUMBER", Type: interfaces.DataType_Decimal}}
		columns := map[string]interfaces.TableColumnMeta{"flag": {Name: "flag", Type: "NUMBER", NumPrecision: 1}}
		err := cf.ValidateFilterCondition("oracle", condition, properties, columns)
		assert.Same(t, want, err)
		assert.Same(t, condition, connector.condition)
		assert.Equal(t, properties, connector.properties)
		assert.Equal(t, columns, connector.columns)
	})
	t.Run("concurrent registry updates", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		cf.SetConnectorEnabled(interfaces.ConnectorTypeMariaDB, true)
		property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
		condition := &filter_condition.BeforeCond{Lfield: property, Value: []any{int64(1), "day"}}
		remote := &interfaces.ConnectorType{Type: "review_remote", Mode: interfaces.ConnectorModeRemote, Category: interfaces.ConnectorCategoryAPI, Enabled: true}
		start := make(chan struct{})
		failures := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			for i := range 2000 {
				if err := cf.RegisterConnector(t.Context(), remote.Type, remote); err != nil {
					failures <- err
					return
				}
				cf.SetConnectorEnabled(interfaces.ConnectorTypeMariaDB, i%2 == 0)
			}
		}()
		go func() {
			defer group.Done()
			<-start
			for range 2000 {
				err := cf.ValidateFilterCondition(interfaces.ConnectorTypeMariaDB, condition, []*interfaces.Property{property}, nil)
				if err != nil && !errors.Is(err, ErrConnectorDisabled) {
					failures <- err
					return
				}
			}
		}()
		done := make(chan struct{})
		go func() { group.Wait(); close(done) }()
		close(start)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent validation and registry updates did not finish")
		}
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
	})
	t.Run("absent condition needs no connector", func(t *testing.T) {
		require.NoError(t, (&connectorFactory{}).ValidateFilterCondition("", nil, nil, nil))
	})
	t.Run("preserves connector availability errors", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)
		for _, tc := range []struct {
			name string
			want error
		}{
			{"missing", ErrConnectorUnavailable}, {"disabled", ErrConnectorDisabled}, {"private", ErrConnectorEntitlementDenied},
		} {
			t.Run(tc.name, func(t *testing.T) {
				connector := vmock.NewMockConnector(gomock.NewController(t))
				cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
				if tc.name != "missing" {
					cf.connectors[tc.name] = connector
				}
				if tc.name == "private" {
					cf.connectorRequiredEditions[tc.name] = licverify.EditionProfessional
				}
				if tc.name == "disabled" {
					connector.EXPECT().GetEnabled().Return(false)
				}
				assert.ErrorIs(t, cf.ValidateFilterCondition(tc.name, &filter_condition.BeforeCond{}, nil, nil), tc.want)
			})
		}
	})
}

func TestConnectorFactoryGetConnectorFieldConfig(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
	cf.RegisterCoreLocalConnectors()
	ct := &interfaces.ConnectorType{Type: interfaces.ConnectorTypePostgreSQL, Mode: interfaces.ConnectorModeLocal, Category: interfaces.ConnectorCategoryTable}
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		ctx := context.WithValue(context.Background(), i18n.LanguageKey, language)
		fields, err := cf.GetConnectorFieldConfig(ctx, ct)
		require.NoError(t, err)
		assert.Equal(t, "Connection options", fields["options"].Name)
		assert.Contains(t, fields["options"].Description, "Optional; only")
		assert.Contains(t, fields["options"].Description, "timezone")
		assert.Contains(t, fields["options"].Description, "idle_in_transaction_session_timeout")
		assert.Equal(t, "object", fields["options"].Type)
		assert.False(t, fields["options"].Required)
		assert.False(t, fields["options"].Encrypted)
	}
}
