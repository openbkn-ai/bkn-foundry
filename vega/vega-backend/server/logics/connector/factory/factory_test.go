// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/remote"
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

		assert.Contains(t, cf.connectors, "mysql")
		assert.Contains(t, cf.connectors, "mariadb")
		assert.Contains(t, cf.connectors, "postgresql")
		assert.Contains(t, cf.connectors, "opensearch")
		assert.Contains(t, cf.connectors, "anyshare")
		assert.NotContains(t, cf.connectors, "sqlserver")
		assert.NotContains(t, cf.connectors, "oracle")
		assert.NotContains(t, cf.connectors, "doris")
		assert.Equal(t, licverify.EditionCommunity, cf.connectorRequiredEditions["mysql"])
		assert.NotSame(t, cf.connectors["mysql"], cf.connectors["mariadb"])
		assert.Empty(t, entitlement.Assembled())

		cf.SetConnectorEnabled("mariadb", true)
		cf.SetConnectorEnabled("mysql", false)

		assert.True(t, cf.connectors["mariadb"].GetEnabled())
		assert.False(t, cf.connectors["mysql"].GetEnabled())
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
	connector := vmock.NewMockConnector(gomock.NewController(t))
	connector.EXPECT().GetEnabled().Return(true)
	cf := &connectorFactory{
		connectors: map[string]interfaces.Connector{
			"registered": connector,
		},
		connectorRequiredEditions: map[string]licverify.Edition{
			"sqlserver": licverify.EditionProfessional,
		},
	}

	registered := cf.GetConnectorAvailability("registered")
	assert.True(t, registered.Available)
	assert.Empty(t, registered.RequiredEdition)
	assert.False(t, registered.Disabled)

	missing := cf.GetConnectorAvailability("missing")
	assert.False(t, missing.Available)
	assert.Empty(t, missing.RequiredEdition)
	t.Run("enabled state changes independently of edition availability", func(t *testing.T) {
		connector := mariadb.NewMySQLConnector()
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"mysql": connector}}
		for _, enabled := range []bool{true, false, true} {
			cf.SetConnectorEnabled("mysql", enabled)
			availability := cf.GetConnectorAvailability("mysql")
			assert.True(t, availability.Available)
			assert.Equal(t, !enabled, availability.Disabled)
		}
	})
}

func TestConnectorFactoryPrivateConnectorUsesCurrentEntitlement(t *testing.T) {
	connectorBuilder := vmock.NewMockConnector(gomock.NewController(t))
	connectorBuilder.EXPECT().GetEnabled().Return(true).AnyTimes()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)

	cf := &connectorFactory{
		connectors: map[string]interfaces.Connector{
			"sqlserver": connectorBuilder,
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
			Type: "mysql", Name: "MySQL", Mode: interfaces.ConnectorModeLocal,
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
			local.EXPECT().GetSensitiveFields(gomock.Any()).Return([]string{"password"}, nil),
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

		fields, err := cf.GetSensitiveFields("localdb", nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"password"}, fields)
		fields, err = cf.GetSensitiveFields("missing", nil)
		require.ErrorIs(t, err, ErrConnectorUnavailable)
		assert.Nil(t, fields)

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
	t.Run("corrupt table metadata stops before converting the condition", func(t *testing.T) {
		for _, raw := range []any{nil, []interfaces.TableColumnMeta(nil), "corrupt"} {
			connector := vmock.NewMockTableConnector(gomock.NewController(t))
			connector.EXPECT().GetEnabled().Return(true)
			cf := &connectorFactory{connectors: map[string]interfaces.Connector{"table": connector}}
			resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": raw}}
			err := cf.ValidateFilterCondition("table", &filter_condition.AndCond{}, resource)
			require.ErrorContains(t, err, "invalid stored source column metadata")
			var invalid *interfaces.ConditionBuildError
			assert.NotErrorAs(t, err, &invalid)
		}
	})
	t.Run("uses actual table converter without creating a connection", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		for _, tc := range []struct {
			connector, unit string
			valid           bool
		}{
			{"mariadb", "day", true},
			{"mysql", "microsecond", true},
			{"mariadb", "quarter", true},
			{"mariadb", "days", false},
			{"postgresql", "days", true},
			{"postgresql", "weeks", true},
			{"postgresql", "microsecond", false},
			{"postgresql", "quarter", false},
			{"anyshare", "day", false},
			{"opensearch", "day", true},
			{"opensearch", "microsecond", false},
		} {
			t.Run(tc.connector+"/"+tc.unit, func(t *testing.T) {
				cf.SetConnectorEnabled(tc.connector, true)
				cfg := &interfaces.FilterCondCfg{Field: "created", Operation: filter_condition.OperationBefore, ValueFrom: interfaces.ValueFrom_Const, Value: []any{1, tc.unit}}
				cond, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"created": {Name: "created", Type: interfaces.DataType_Datetime}})
				require.NoError(t, err)
				for _, tree := range []interfaces.FilterCondition{
					cond,
					&filter_condition.AndCond{SubConds: []interfaces.FilterCondition{&filter_condition.OrCond{SubConds: []interfaces.FilterCondition{cond}}}},
				} {
					err = cf.ValidateFilterCondition(tc.connector, tree, &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "created", Type: interfaces.DataType_Datetime}}})
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
		cf.SetConnectorEnabled("mariadb", true)
		cfg := &interfaces.FilterCondCfg{Field: "content", Operation: filter_condition.OperationMatch, ValueFrom: interfaces.ValueFrom_Const, Value: "search"}
		condition, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{"content": {Name: "content", Type: interfaces.DataType_Text}})
		require.NoError(t, err)
		err = cf.ValidateFilterCondition("mariadb", condition, &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text}}})
		var unsupported *interfaces.UnsupportedOperationError
		require.ErrorAs(t, err, &unsupported)
	})
	t.Run("passes index schema to the full converter", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		cf.SetConnectorEnabled("opensearch", true)
		field := &interfaces.Property{Name: "content", OriginalName: "physical.content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 4}}}}
		source := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{field}}
		for _, value := range []string{"okay", "longer"} {
			cfg := &interfaces.FilterCondCfg{Field: field.Name, Operation: filter_condition.OperationEqual, ValueFrom: interfaces.ValueFrom_Const, Value: value}
			cond, err := filter_condition.NewFilterCondition(t.Context(), cfg, map[string]*interfaces.Property{field.Name: field})
			require.NoError(t, err)
			err = cf.ValidateFilterCondition("opensearch", cond, source)
			if value == "okay" {
				require.NoError(t, err)
			} else {
				var limitErr *interfaces.KeywordLimitError
				require.ErrorAs(t, err, &limitErr)
				assert.Equal(t, 4, limitErr.Limit)
			}
		}
	})

	t.Run("passes table schema and source column metadata without mutation", func(t *testing.T) {
		for _, persisted := range []bool{false, true} {
			t.Run(map[bool]string{false: "discovery metadata", true: "persisted JSON metadata"}[persisted], func(t *testing.T) {
				builder := vmock.NewMockTableConnector(gomock.NewController(t))
				builder.EXPECT().GetEnabled().Return(true)
				want := errors.New("conversion failed")
				connector := &metadataFilterConnector{TableConnector: builder, conversionErr: want}
				cf := &connectorFactory{connectors: map[string]interfaces.Connector{"oracle": connector}}
				condition := &filter_condition.AndCond{SubConds: []interfaces.FilterCondition{&filter_condition.OrCond{}}}
				source := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "flag", OriginalType: "NUMBER", Type: interfaces.DataType_Decimal}}, SourceMetadata: map[string]any{"columns": []interfaces.TableColumnMeta{{Name: "flag", Type: "NUMBER", NumPrecision: 1, Nullable: true}}}}
				if persisted {
					encoded, err := json.Marshal(source)
					require.NoError(t, err)
					var stored interfaces.Resource
					require.NoError(t, json.Unmarshal(encoded, &stored))
					source = &stored
				}
				before, err := json.Marshal(source)
				require.NoError(t, err)
				err = cf.ValidateFilterCondition("oracle", condition, source)
				assert.Same(t, want, err)
				assert.Same(t, condition, connector.condition)
				require.Len(t, connector.properties, 1)
				assert.Same(t, source.SchemaDefinition[0], connector.properties[0])
				assert.Same(t, &source.SchemaDefinition[0], &connector.properties[0])
				assert.Equal(t, map[string]interfaces.TableColumnMeta{"flag": {Name: "flag", Type: "NUMBER", NumPrecision: 1, Nullable: true}}, connector.columns)
				after, err := json.Marshal(source)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})
	t.Run("native index receives original schema without source table assumptions", func(t *testing.T) {
		connector := vmock.NewMockIndexConnector(gomock.NewController(t))
		connector.EXPECT().GetEnabled().Return(true)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"index": connector}}
		field := &interfaces.Property{Name: "body", OriginalName: "native.body", OriginalType: "text", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "native_keyword"}}}
		source := &interfaces.Resource{Category: interfaces.ResourceCategoryIndex, SchemaDefinition: []*interfaces.Property{field}, SourceMetadata: map[string]any{"columns": "index metadata does not define table columns"}}
		condition := &filter_condition.AndCond{}
		before, err := json.Marshal(source)
		require.NoError(t, err)
		connector.EXPECT().ConvertFilterCondition(condition, gomock.Any()).DoAndReturn(func(got interfaces.FilterCondition, schema []*interfaces.Property) (map[string]any, error) {
			assert.Same(t, condition, got)
			require.Len(t, schema, 1)
			assert.Same(t, field, schema[0])
			assert.Same(t, &source.SchemaDefinition[0], &schema[0])
			assert.Equal(t, "native.body", schema[0].OriginalName)
			assert.Equal(t, "text", schema[0].OriginalType)
			return map[string]any{"match_all": map[string]any{}}, nil
		})
		require.NoError(t, cf.ValidateFilterCondition("index", condition, source))
		after, err := json.Marshal(source)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	t.Run("concurrent registry updates", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		cf.SetConnectorEnabled("mariadb", true)
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
				cf.SetConnectorEnabled("mariadb", i%2 == 0)
			}
		}()
		go func() {
			defer group.Done()
			<-start
			for range 2000 {
				err := cf.ValidateFilterCondition("mariadb", condition, &interfaces.Resource{SchemaDefinition: []*interfaces.Property{property}})
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
		require.NoError(t, (&connectorFactory{}).ValidateFilterCondition("", nil, nil))
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
				assert.ErrorIs(t, cf.ValidateFilterCondition(tc.name, &filter_condition.BeforeCond{}, &interfaces.Resource{}), tc.want)
			})
		}
	})
}

func TestConnectorFactoryConvertSQLFilterCondition(t *testing.T) {
	t.Run("returns the registered converter AST without opening a connection", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		connector.EXPECT().GetEnabled().Return(true)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"table": connector}}
		condition := &filter_condition.AndCond{}
		source := &interfaces.Resource{Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{{Name: "tenant", OriginalName: "tenant_id", Type: interfaces.DataType_String}},
			SourceMetadata:   map[string]any{"columns": []interfaces.TableColumnMeta{{Name: "tenant_id", Type: "VARCHAR"}}}}
		want := &sqlbuilder.Eq{Left: &sqlbuilder.Column{Name: "tenant_id"}, Right: &sqlbuilder.Param{Value: "fixed"}}
		connector.EXPECT().ConvertFilterCondition(condition, source.SchemaDefinition,
			map[string]interfaces.TableColumnMeta{"tenant_id": {Name: "tenant_id", Type: "VARCHAR"}}).Return(want, nil)
		got, err := cf.ConvertSQLFilterCondition("table", condition, source)
		require.NoError(t, err)
		assert.Same(t, want, got)
	})
	t.Run("literal source names do not require column metadata", func(t *testing.T) {
		connector := mariadb.NewMySQLConnector()
		connector.SetEnabled(true)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"table": connector}}
		field := &interfaces.Property{Name: "tenant", OriginalName: "tenant.id", Type: interfaces.DataType_String}
		source := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{field}}
		condition, err := filter_condition.NewFilterCondition(t.Context(),
			&interfaces.FilterCondCfg{Field: field.Name, Operation: "==", Value: "fixed"},
			map[string]*interfaces.Property{field.Name: field})
		require.NoError(t, err)
		got, err := cf.ConvertSQLFilterCondition("table", condition, source)
		require.NoError(t, err)
		assert.Equal(t, &sqlbuilder.Eq{Left: &sqlbuilder.Column{Name: "tenant.id"}, Right: &sqlbuilder.Param{Value: "fixed"}}, got)
	})

	t.Run("preserves typed conversion failures", func(t *testing.T) {
		connector := vmock.NewMockTableConnector(gomock.NewController(t))
		connector.EXPECT().GetEnabled().Return(true)
		want := interfaces.NewConditionBuildError("unsupported source filter")
		connector.EXPECT().ConvertFilterCondition(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, want)
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"table": connector}}
		got, err := cf.ConvertSQLFilterCondition("table", &filter_condition.AndCond{}, &interfaces.Resource{Category: interfaces.ResourceCategoryTable})
		assert.Nil(t, got)
		assert.ErrorIs(t, err, want)
	})
	t.Run("nil condition needs no connector", func(t *testing.T) {
		got, err := (&connectorFactory{}).ConvertSQLFilterCondition("", nil, nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("availability checked before conversion", func(t *testing.T) {
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
				got, err := cf.ConvertSQLFilterCondition(tc.name, &filter_condition.AndCond{}, &interfaces.Resource{Category: interfaces.ResourceCategoryTable})
				assert.Nil(t, got)
				assert.ErrorIs(t, err, tc.want)
			})
		}
	})
	t.Run("non table sources and malformed metadata fail before conversion", func(t *testing.T) {
		for _, source := range []*interfaces.Resource{nil, {Category: interfaces.ResourceCategoryIndex},
			{Category: interfaces.ResourceCategoryTable, SourceMetadata: map[string]any{"columns": "invalid"}}} {
			connector := vmock.NewMockTableConnector(gomock.NewController(t))
			connector.EXPECT().GetEnabled().Return(true)
			cf := &connectorFactory{connectors: map[string]interfaces.Connector{"table": connector}}
			got, err := cf.ConvertSQLFilterCondition("table", &filter_condition.AndCond{}, source)
			assert.Nil(t, got)
			require.Error(t, err)
		}
	})
}

func TestConnectorFactoryGetConnectorFieldConfig(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
	cf.RegisterCoreLocalConnectors()
	ct := &interfaces.ConnectorType{Type: "postgresql", Mode: interfaces.ConnectorModeLocal, Category: interfaces.ConnectorCategoryTable}
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

func TestConnectorFactoryValidateFilterability(t *testing.T) {
	flag := func(value bool) *bool { return &value }
	t.Run("closed flags do not require a connector", func(t *testing.T) {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": "corrupt"}, SchemaDefinition: []*interfaces.Property{{Name: "closed", Filterable: flag(false)}, {Name: "omitted"}}}
		require.NoError(t, (&connectorFactory{}).ValidateFilterability("unavailable", resource))
	})
	t.Run("table directly uses shared validation with registered connector", func(t *testing.T) {
		for _, result := range []string{"supported", "persisted JSON", "unsupported", "corrupt columns"} {
			t.Run(result, func(t *testing.T) {
				connector := vmock.NewMockTableConnector(gomock.NewController(t))
				connector.EXPECT().GetEnabled().Return(true)
				column := interfaces.TableColumnMeta{Name: "native", Type: "NATIVE", NumPrecision: 12, NumScale: 4, Filterable: flag(false)}
				resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable,
					SchemaDefinition: []*interfaces.Property{{Name: "enabled", OriginalName: "native", OriginalType: "stale", Filterable: flag(true)}, {Name: "closed", Filterable: flag(false)}, {Name: "legacy"}},
					SourceMetadata:   map[string]any{"columns": []interfaces.TableColumnMeta{column}, "mapping": "not table metadata"}}
				if result == "corrupt columns" {
					resource.SourceMetadata["columns"] = "corrupt"
				} else {
					connector.EXPECT().IsFilterable(gomock.Eq(&column)).Return(result != "unsupported")
				}
				if result == "persisted JSON" {
					data, err := json.Marshal(resource)
					require.NoError(t, err)
					var stored interfaces.Resource
					require.NoError(t, json.Unmarshal(data, &stored))
					resource = &stored
				}
				before, err := json.Marshal(resource)
				require.NoError(t, err)
				cf := &connectorFactory{connectors: map[string]interfaces.Connector{"source": connector}}
				err = cf.ValidateFilterability("source", resource)
				var invalid *interfaces.ConditionBuildError
				switch result {
				case "supported", "persisted JSON":
					require.NoError(t, err)
				case "unsupported":
					require.ErrorAs(t, err, &invalid)
					assert.Contains(t, invalid.Reason, "enabled")
				case "corrupt columns":
					require.Error(t, err)
					assert.NotErrorAs(t, err, &invalid)
				}
				after, err := json.Marshal(resource)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})
	t.Run("index and fileset delegate complete resource without interpreting metadata", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryIndex, interfaces.ResourceCategoryFileset} {
			for _, result := range []string{"supported", "unsupported", "provider failure"} {
				t.Run(category+"/"+result, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					var connector interfaces.Connector
					var validation *gomock.Call
					switch category {
					case interfaces.ResourceCategoryIndex:
						index := vmock.NewMockIndexConnector(ctrl)
						connector = index
						index.EXPECT().GetEnabled().Return(true)
						validation = index.EXPECT().ValidateFilterability(gomock.Any())
					case interfaces.ResourceCategoryFileset:
						fileset := vmock.NewMockFilesetConnector(ctrl)
						connector = fileset
						fileset.EXPECT().GetEnabled().Return(true)
						validation = fileset.EXPECT().ValidateFilterability(gomock.Any())
					}
					cf := &connectorFactory{connectors: map[string]interfaces.Connector{"source": connector}}
					resource := &interfaces.Resource{Category: category,
						SchemaDefinition: []*interfaces.Property{{Name: "enabled", OriginalName: "native", Filterable: flag(true)}, {Name: "closed", Filterable: flag(false)}},
						SourceMetadata:   map[string]any{"columns": "provider-owned columns", "mapping": "provider-owned mapping"}}
					before, err := json.Marshal(resource)
					require.NoError(t, err)
					var want error
					switch result {
					case "unsupported":
						want = interfaces.NewConditionBuildError("provider rejects enabled field")
					case "provider failure":
						want = errors.New("provider metadata failure")
					}
					validation.DoAndReturn(func(got *interfaces.Resource) error { assert.Same(t, resource, got); return want })
					err = cf.ValidateFilterability("source", resource)
					if want == nil {
						require.NoError(t, err)
					} else {
						assert.Same(t, want, err)
					}
					after, err := json.Marshal(resource)
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after))
				})
			}
		}
	})
	t.Run("availability is checked before provider validation", func(t *testing.T) {
		entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
		t.Cleanup(entitlement.ResetForTest)
		for _, tc := range []struct {
			name string
			want error
		}{{"unavailable", ErrConnectorUnavailable}, {"disabled", ErrConnectorDisabled}, {"unlicensed", ErrConnectorEntitlementDenied}} {
			t.Run(tc.name, func(t *testing.T) {
				connector := vmock.NewMockTableConnector(gomock.NewController(t))
				cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
				if tc.name != "unavailable" {
					cf.connectors[tc.name] = connector
				}
				if tc.name == "unlicensed" {
					cf.connectorRequiredEditions[tc.name] = licverify.EditionEnterprise
				}
				if tc.name == "disabled" {
					connector.EXPECT().GetEnabled().Return(false)
				}
				resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "enabled", Filterable: flag(true)}}}
				assert.ErrorIs(t, cf.ValidateFilterability(tc.name, resource), tc.want)
			})
		}
	})
	t.Run("unsupported providers reject enabled filtering without a connection", func(t *testing.T) {
		for _, provider := range []string{"remote", "basic"} {
			for _, tc := range []struct {
				name string
				flag *bool
			}{{"omitted", nil}, {"disabled", flag(false)}, {"enabled", flag(true)}} {
				t.Run(provider+"/"+tc.name, func(t *testing.T) {
					var connector interfaces.Connector
					if provider == "remote" {
						connector = remote.NewRemoteConnector(&interfaces.ConnectorType{Type: "remote", Enabled: true})
					} else {
						basic := vmock.NewMockConnector(gomock.NewController(t))
						connector = basic
						if tc.flag != nil && *tc.flag {
							basic.EXPECT().GetEnabled().Return(true)
						}
					}
					cf := &connectorFactory{connectors: map[string]interfaces.Connector{provider: connector}}
					resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "field", Type: interfaces.DataType_Integer, Filterable: tc.flag}}}
					before, err := json.Marshal(resource)
					require.NoError(t, err)
					err = cf.ValidateFilterability(provider, resource)
					if tc.flag != nil && *tc.flag {
						var unsupported *interfaces.ConditionBuildError
						require.ErrorAs(t, err, &unsupported)
					} else {
						require.NoError(t, err)
					}
					after, err := json.Marshal(resource)
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after))
					assert.Same(t, tc.flag, resource.SchemaDefinition[0].Filterable)
				})
			}
		}
	})
}

func TestConnectorFactoryGetSensitiveFields(t *testing.T) {
	t.Run("passes original config and propagates connector metadata errors without instantiation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)
		cfg := interfaces.ConnectorConfig{"options": map[string]any{"fixture_option": "fixture"}}
		metadataErr := errors.New("invalid sensitive member metadata")
		connector.EXPECT().GetSensitiveFields(gomock.Any()).DoAndReturn(func(got interfaces.ConnectorConfig) ([]string, error) {
			assert.Equal(t, cfg, got)
			got["observed"] = true
			return nil, metadataErr
		})
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{"present": connector}}
		got, err := cf.GetSensitiveFields("present", cfg)
		require.ErrorIs(t, err, metadataErr)
		assert.Nil(t, got)
		assert.Equal(t, true, cfg["observed"])
	})

	for _, fields := range [][]string{nil, {"token"}} {
		t.Run("existing metadata", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			connector := vmock.NewMockConnector(ctrl)
			connector.EXPECT().GetSensitiveFields(gomock.Any()).Return(fields, nil)
			cf := &connectorFactory{connectors: map[string]interfaces.Connector{"present": connector}}
			got, err := cf.GetSensitiveFields("present", nil)
			require.NoError(t, err)
			assert.Equal(t, fields, got)
			got, err = cf.GetSensitiveFields("missing", nil)
			require.ErrorIs(t, err, ErrConnectorUnavailable)
			assert.Nil(t, got)
		})
	}
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	ctrl := gomock.NewController(t)
	connector := vmock.NewMockConnector(ctrl)
	connector.EXPECT().GetSensitiveFields(gomock.Any()).Return([]string{"token"}, nil)
	cf := &connectorFactory{connectors: map[string]interfaces.Connector{"private": connector}, connectorRequiredEditions: map[string]licverify.Edition{"private": licverify.EditionProfessional}}
	fields, err := cf.GetSensitiveFields("private", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"token"}, fields)
	instance, err := cf.CreateConnectorInstance(context.Background(), "private", nil)
	require.ErrorIs(t, err, ErrConnectorEntitlementDenied)
	assert.Nil(t, instance)
}

func TestConnectorFactorySupportDiscover(t *testing.T) {
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(entitlement.ResetForTest)
	for _, support := range []bool{false, true} {
		t.Run(fmt.Sprintf("static discovery support=%t", support), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			builder := vmock.NewMockConnector(ctrl)
			builder.EXPECT().SupportDiscover().Return(support)
			cf := &connectorFactory{connectors: map[string]interfaces.Connector{"arbitrary": builder}, connectorRequiredEditions: map[string]licverify.Edition{"arbitrary": licverify.EditionProfessional}}
			got, err := cf.SupportDiscover("arbitrary")
			require.NoError(t, err)
			assert.Equal(t, support, got)
			instance, err := cf.CreateConnectorInstance(context.Background(), "arbitrary", nil)
			require.ErrorIs(t, err, ErrConnectorEntitlementDenied)
			assert.Nil(t, instance)
		})
	}
	t.Run("unknown connector", func(t *testing.T) {
		cf := &connectorFactory{}
		got, err := cf.SupportDiscover("unknown")
		require.ErrorIs(t, err, ErrConnectorUnavailable)
		assert.False(t, got)
	})
	t.Run("existing core connectors retain discovery", func(t *testing.T) {
		cf := &connectorFactory{connectors: map[string]interfaces.Connector{}, connectorRequiredEditions: map[string]licverify.Edition{}}
		cf.RegisterCoreLocalConnectors()
		for _, name := range []string{"mysql", "mariadb", "postgresql", "opensearch", "anyshare"} {
			got, err := cf.SupportDiscover(name)
			require.NoError(t, err)
			assert.True(t, got, name)
		}
	})
}
