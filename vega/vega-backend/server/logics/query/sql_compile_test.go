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
func TestPrepareSQLQuery(t *testing.T) {
	t.Setenv("TZ", "UTC")
	t.Run("self join preserves source instances and raw identifiers", func(t *testing.T) {
		rs, cs := expectCompileMetadata(t, compileTestResource(), interfaces.ConnectorTypeMySQL)
		plan, deps, err := PrepareSQLQuery(compileTestContext(), "SELECT a.*, b.id AS other_id FROM {{ orders }} a JOIN {{ orders }} b ON a.id=b.id", rs, cs)
		require.NoError(t, err)
		join := plan.FromPart.(*qm.Join)
		left, right := join.Left.(*qm.Scan), join.Right.(*qm.Scan)
		assert.NotEqual(t, left.Instance, right.Instance)
		for _, scan := range []*qm.Scan{left, right} {
			assert.Equal(t, qm.PhysicalIdentifier{"sales.eu", "order.items"}, scan.Identifier)
			require.Len(t, scan.Fields, 2)
			assert.Equal(t, qm.PhysicalIdentifier{"order.id"}, scan.Fields[0].Identifier)
		}
		require.Len(t, deps, 3)
		assert.Equal(t, qm.DependencyResource, deps[0].Kind)
		assert.Equal(t, "11", deps[0].Version)
		assert.NotEmpty(t, deps[0].SchemaFingerprint)
		assert.Equal(t, "orders", deps[0].Authorization[0].ResourceID)
		assert.Equal(t, qm.DependencyConfig, deps[2].Kind)
		assert.NotEmpty(t, deps[2].ConfigRevision)
		var catalogs int
		for _, dep := range deps {
			if dep.Kind == qm.DependencyCatalog {
				catalogs++
				assert.Equal(t, "7", dep.ConfigRevision)
			}
		}
		assert.Equal(t, 1, catalogs)
	})
	t.Run("expanded view dependencies belong to preparation result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs, cs := mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl)
		view := &interfaces.Resource{ID: "view", CatalogID: "views", Category: interfaces.ResourceCategoryLogicalView,
			Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 12, LogicType: interfaces.LogicType_Derived,
			LogicDefinition:  &interfaces.DerivedLogicDefinition{SourceResourceID: "orders", FilterCondition: map[string]any{"field": "id", "operation": ">", "value": 0}},
			SchemaDefinition: []*interfaces.Property{{Name: "public_id", OriginalName: "order.id", OriginalType: "BIGINT", Type: interfaces.DataType_Integer}}}
		gomock.InOrder(
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "view", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "view").Return(view, nil),
			cs.EXPECT().InternalGetByID(gomock.Any(), "views", false).Return(&interfaces.Catalog{ID: "views", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 8}, nil),
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(compileTestResource(), nil),
			cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", false).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 7}, nil),
		)
		query, deps, err := PrepareSQLQuery(compileTestContext(), "SELECT a.public_id AS left_id,b.public_id AS right_id FROM {{view}} a LEFT JOIN {{view}} b ON a.public_id=b.public_id", rs, cs)
		require.NoError(t, err)
		require.Len(t, deps, 5)
		byID := map[string]qm.Dependency{}
		for _, dep := range deps {
			byID[dep.ID] = dep
		}
		assert.Equal(t, qm.DependencyView, byID["view"].Kind)
		assert.Equal(t, "12", byID["view"].Version)
		assert.Equal(t, qm.DependencyResource, byID["orders"].Kind)
		assert.Equal(t, "11", byID["orders"].Version)
		for _, id := range []string{"view", "orders"} {
			assert.NotEmpty(t, byID[id].SchemaFingerprint)
			require.Len(t, byID[id].Authorization, 1)
			assert.Equal(t, id, byID[id].Authorization[0].ResourceID)
			assert.Equal(t, interfaces.OPERATION_TYPE_QUERY_DATA, byID[id].Authorization[0].Operation)
		}
		assert.Equal(t, "8", byID["views"].ConfigRevision)
		assert.Equal(t, "7", byID["catalog"].ConfigRevision)
		assert.Equal(t, qm.CurrentSemanticsVersion, byID["query_semantics"].Version)
		assert.NotEmpty(t, byID["query_semantics"].ConfigRevision)
		join := query.FromPart.(*qm.Join)
		left, right := join.Left.(*qm.Subquery), join.Right.(*qm.Subquery)
		assert.NotEqual(t, left.Instance, right.Instance)
		for _, boundary := range []*qm.Subquery{left, right} {
			assert.Equal(t, "view", boundary.Resource.ID)
			require.Len(t, boundary.Query.WherePart, 1)
			assert.Equal(t, qm.OriginSavedView, boundary.Query.WherePart[0].Origin.Kind)
			scan := boundary.Query.FromPart.(*qm.Scan)
			assert.Equal(t, qm.PhysicalIdentifier{"sales.eu", "order.items"}, scan.Identifier)
			assert.Equal(t, qm.PhysicalIdentifier{"order.id"}, scan.Fields[0].Identifier)
		}
	})

	t.Run("denial prevents metadata reads", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		denial := errors.New("permission denied")
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		plan, deps, err := PrepareSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", rs, cs)
		assert.Nil(t, plan)
		assert.Nil(t, deps)
		assert.ErrorIs(t, err, denial)
	})
	t.Run("invalid SQL reads no metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		plan, deps, err := PrepareSQLQuery(compileTestContext(), "DELETE FROM {{ orders }}", mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl))
		assert.Nil(t, plan)
		assert.Nil(t, deps)
		assert.Error(t, err)
	})
	t.Run("invalid TZ reads no metadata", func(t *testing.T) {
		t.Setenv("TZ", "invalid/zone")
		ctrl := gomock.NewController(t)
		plan, deps, err := PrepareSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl))
		assert.Nil(t, plan)
		assert.Nil(t, deps)
		assert.Error(t, err)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(compileTestContext())
		cancel()
		plan, deps, err := PrepareSQLQuery(ctx, "SELECT * FROM {{ orders }}", nil, nil)
		assert.Nil(t, plan)
		assert.Nil(t, deps)
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
			plan, deps, err := PrepareSQLQuery(compileTestContext(), "SELECT * FROM {{ orders }}", rs, cs)
			assert.Nil(t, plan)
			assert.Nil(t, deps)
			var ce *qm.CompileError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, qm.ErrorPlanningUnsupported, ce.Kind)
		})
	}
}
func TestPrepareResourceDataQuery(t *testing.T) {
	t.Setenv("TZ", "UTC")
	for _, tc := range []struct {
		name        string
		index       bool
		ignore      bool
		unspecified bool
		wantError   bool
	}{
		{name: "source"}, {name: "index unsupported", index: true, wantError: true},
		{name: "default routing keeps index", index: true, unspecified: true, wantError: true},
		{name: "ignore index", index: true, ignore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := compileTestResource()
			if tc.index {
				resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
				resource.LocalIndexName = "local-index"
			}
			rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
			params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id"}, IgnoreLocalIndex: &tc.ignore}
			if tc.unspecified {
				params.IgnoreLocalIndex = nil
			}
			plan, deps, err := PrepareResourceDataQuery(compileTestContext(), "orders", params, rs, cs)
			if tc.wantError {
				assert.Nil(t, plan)
				assert.Nil(t, deps)
				var ce *qm.CompileError
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, qm.ErrorPlanningUnsupported, ce.Kind)
				return
			}
			require.NoError(t, err)
			require.Len(t, deps, 3)
			assert.Equal(t, "orders", deps[0].ID)
		})
	}
}
