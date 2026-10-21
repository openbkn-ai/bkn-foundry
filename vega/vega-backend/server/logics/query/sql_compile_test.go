// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func compileTestResource() *interfaces.Resource {
	return &interfaces.Resource{ID: "orders", CatalogID: "catalog", Name: "display label", Category: interfaces.ResourceCategoryTable,
		Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 11, SourceIdentifier: "ignored.already_quoted",
		SourceMetadata: map[string]any{"database": "sales.eu", "schema": "sales.eu", "name": "order.items"},
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer, OriginalName: "order.id", OriginalType: "BIGINT"},
			{Name: "created", Type: interfaces.DataType_Datetime, OriginalName: "created at", OriginalType: "TIMESTAMP(6)"},
		}}
}
func compileTestContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "caller", Type: "user"})
}
func expectCompileMetadata(t *testing.T, resource *interfaces.Resource, connector string) (*mock_interfaces.MockResourceService, *mock_interfaces.MockCatalogService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	cs := mock_interfaces.NewMockCatalogService(ctrl)
	gomock.InOrder(
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil),
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", false).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: connector, Enabled: true, UpdateTime: 7}, nil),
	)
	return rs, cs
}
func TestCompileSQLQuery(t *testing.T) {
	t.Setenv("TZ", "UTC")
	t.Run("self join preserves source instances and raw identifiers", func(t *testing.T) {
		rs, cs := expectCompileMetadata(t, compileTestResource(), interfaces.ConnectorTypeMySQL)
		plan, err := CompileSQLQuery(compileTestContext(), "SELECT a.*, b.id AS other_id FROM {{ orders }} a JOIN {{ orders }} b ON a.id=b.id", rs, cs)
		require.NoError(t, err)
		require.Len(t, plan.TargetBinding.Relations, 2)
		assert.NotEqual(t, plan.TargetBinding.Relations[0].Instance, plan.TargetBinding.Relations[1].Instance)
		for _, relation := range plan.TargetBinding.Relations {
			assert.Equal(t, qm.PhysicalIdentifier{"sales.eu", "order.items"}, relation.Identifier)
		}
		require.Len(t, plan.TargetBinding.Fields, 4)
		assert.Equal(t, qm.PhysicalIdentifier{"order.id"}, plan.TargetBinding.Fields[0].Identifier)
		assert.Equal(t, interfaces.ConnectorTypeMySQL, plan.TargetBinding.Fields[1].NativeType.ConnectorType)
		assert.Equal(t, qm.TimeZoneInstant, plan.TargetBinding.Fields[1].NativeType.TimeZone)
		assert.Equal(t, qm.QuerySourceSource, plan.TargetBinding.Source)
		assert.NotEmpty(t, plan.TargetBinding.Fingerprint)
		var catalogs int
		for _, dep := range plan.Query.Dependencies {
			if dep.Kind == qm.DependencyCatalog {
				catalogs++
				assert.Equal(t, "7", dep.ConfigRevision)
			}
		}
		assert.Equal(t, 1, catalogs)
	})
	t.Run("denial prevents metadata reads", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		denial := errors.New("permission denied")
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		plan, err := CompileSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", rs, cs)
		assert.Nil(t, plan)
		assert.ErrorIs(t, err, denial)
	})
	t.Run("invalid SQL reads no metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		plan, err := CompileSQLQuery(compileTestContext(), "DELETE FROM {{ orders }}", mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl))
		assert.Nil(t, plan)
		assert.Error(t, err)
	})
	t.Run("invalid TZ reads no metadata", func(t *testing.T) {
		t.Setenv("TZ", "invalid/zone")
		ctrl := gomock.NewController(t)
		plan, err := CompileSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl))
		assert.Nil(t, plan)
		assert.Error(t, err)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(compileTestContext())
		cancel()
		plan, err := CompileSQLQuery(ctx, "SELECT * FROM {{ orders }}", nil, nil)
		assert.Nil(t, plan)
		assert.ErrorIs(t, err, context.Canceled)
	})
	for _, tc := range []struct {
		name      string
		connector string
		missing   bool
	}{
		{name: "no fallback to quoted name", connector: interfaces.ConnectorTypeMySQL, missing: true},
		{name: "native connector excluded", connector: interfaces.ConnectorTypeOpenSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := compileTestResource()
			if tc.missing {
				resource.SourceMetadata = nil
			}
			rs, cs := expectCompileMetadata(t, resource, tc.connector)
			plan, err := CompileSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", rs, cs)
			assert.Nil(t, plan)
			var ce *qm.CompileError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, qm.ErrorPlanningUnsupported, ce.Kind)
		})
	}
}
func TestCompileResourceDataQuery(t *testing.T) {
	t.Setenv("TZ", "UTC")
	for _, tc := range []struct {
		name      string
		index     bool
		ignore    bool
		wantError bool
	}{
		{name: "source"}, {name: "auto index unsupported", index: true, wantError: true}, {name: "ignore index", index: true, ignore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := compileTestResource()
			if tc.index {
				resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
				resource.LocalIndexName = "local-index"
			}
			rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
			params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id"}, IgnoreLocalIndex: &tc.ignore}
			plan, _, err := CompileResourceDataQuery(compileTestContext(), "orders", params, rs, cs)
			if tc.wantError {
				assert.Nil(t, plan)
				var ce *qm.CompileError
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, qm.ErrorPlanningUnsupported, ce.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, qm.QuerySourceSource, plan.TargetBinding.Source)
		})
	}
}
