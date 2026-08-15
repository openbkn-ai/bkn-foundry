// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestDiscoverTaskReconcileProgress(t *testing.T) {
	t.Run("advances once for each percentage change", func(t *testing.T) {
		progress := &discoverTaskReconcileProgress{}
		current, changed := progress.MarkSourceListed()
		assert.Equal(t, 5, current)
		assert.True(t, changed)
		current, changed = progress.MarkResourcesReconciled()
		assert.Equal(t, 20, current)
		assert.True(t, changed)
		progress.SetMetadataTotal(3)
		current, changed = progress.AdvanceMetadata()
		assert.Equal(t, 45, current)
		assert.True(t, changed)
		current, changed = progress.AdvanceMetadata()
		assert.Equal(t, 70, current)
		assert.True(t, changed)
		current, changed = progress.AdvanceMetadata()
		assert.Equal(t, 95, current)
		assert.True(t, changed)
	})

	t.Run("completes an empty reconciliation", func(t *testing.T) {
		progress := &discoverTaskReconcileProgress{}
		progress.MarkSourceListed()
		current, changed := progress.MarkResourcesReconciled()
		assert.Equal(t, 20, current)
		assert.True(t, changed)
	})
}

func TestReconcileTableResources(t *testing.T) {
	t.Run("marks new table resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		created := &interfaces.Resource{ID: "r1", SourceIdentifier: "users", Status: interfaces.ResourceStatusActive}
		rs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(created, nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"},
			[]*interfaces.TableMeta{{Name: "users"}}, nil)

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
	})

	t.Run("refreshes missing status when already stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Status: interfaces.ResourceStatusStale}})

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("does not disable user-disabled resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Status: interfaces.ResourceStatusDisabled}})

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("marks restored stale table", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusActive, "").Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusRestored).Return(nil)
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"},
			[]*interfaces.TableMeta{{Name: "users"}},
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Status: interfaces.ResourceStatusStale}})

		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
		assert.Equal(t, 1, result.RestoredCount)
		assert.Zero(t, result.UnchangedCount)
	})
}

func TestBuildSourceIdentifierUsesSchemaAsQueryableNamespace(t *testing.T) {
	dh := &DiscoverTaskWorker{}

	cases := []struct {
		name  string
		table *interfaces.TableMeta
		want  string
	}{
		{
			name:  "postgresql schema table",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "public", Name: "supplier_catalog"},
			want:  "public.supplier_catalog",
		},
		{
			name:  "mariadb schema equals database",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Schema: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
		{
			name:  "database fallback",
			table: &interfaces.TableMeta{Database: "ecommerce_db", Name: "supplier_catalog"},
			want:  "ecommerce_db.supplier_catalog",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := dh.buildSourceIdentifier(tt.table); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestEnrichTableMetadataContinuesWhenOneTableFails(t *testing.T) {
	t.Run("continues when one table fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		inaccessible := &interfaces.Resource{ID: "r1", SourceIdentifier: "public.no_access", LastDiscoverStatus: interfaces.DiscoverStatusNew}
		accessible := &interfaces.Resource{
			ID:                 "r2",
			SourceIdentifier:   "public.erp_material",
			LastDiscoverStatus: interfaces.DiscoverStatusNew,
			StatusMessage:      "discover metadata failed: table metadata not found or inaccessible: public.erp_material",
			SourceMetadata:     map[string]any{"original_name": "public.erp_material"},
		}
		connector := vmock.NewMockTableConnector(ctrl)
		connector.EXPECT().
			GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "no_access", Schema: "public"}).
			Return(errors.New("permission denied"))
		connector.EXPECT().
			GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "erp_material", Schema: "public"}).
			DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
				table.Columns = []interfaces.TableColumnMeta{{Name: "id", Type: "int4"}}
				return nil
			})
		connector.EXPECT().MapType("int4").Return("int4")
		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
				assert.Equal(t, "r1", resource.ID)
				assert.Equal(t, interfaces.DiscoverStatusError, resource.LastDiscoverStatus)
				assert.NotEmpty(t, resource.StatusMessage)
				return nil
			})
		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, resource *interfaces.Resource) error {
				assert.Equal(t, "r2", resource.ID)
				require.Len(t, resource.SchemaDefinition, 1)
				assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
				assert.Empty(t, resource.StatusMessage)
				return nil
			})

		result := &interfaces.DiscoverResult{}
		err := dh.enrichTableMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []tableDiscoverItem{
			{resource: inaccessible, tableMeta: &interfaces.TableMeta{Name: "no_access", Schema: "public"}},
			{resource: accessible, tableMeta: &interfaces.TableMeta{Name: "erp_material", Schema: "public"}},
		}, result, &discoverTaskReconcileProgress{lastProgress: 95})

		require.NoError(t, err)
		assert.Equal(t, 1, result.FailedCount)
	})
}

func TestEnrichTableMetadataPreservesBusinessMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{
		ID:               "r1",
		SourceIdentifier: "public.departments",
		SchemaDefinition: []*interfaces.Property{
			{
				Name:                "department_id",
				DisplayName:         "部门ID",
				Description:         "部门的唯一标识",
				Type:                "integer",
				OriginalDescription: "旧源端注释",
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			},
			{Name: "obsolete_column", DisplayName: "已删除字段", Type: interfaces.DataType_String},
		},
	}
	connector := vmock.NewMockTableConnector(ctrl)
	connector.EXPECT().
		GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "departments", Schema: "public"}).
		DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
			table.Columns = []interfaces.TableColumnMeta{
				{Name: "department_id", Type: "varchar", Description: "源端最新部门编号注释"},
				{Name: "department_name", Type: "varchar", Description: "部门名称"},
			}
			return nil
		})
	connector.EXPECT().MapType("varchar").Return(interfaces.DataType_String).Times(2)
	rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
		DoAndReturn(func(_ context.Context, updated *interfaces.Resource) error {
			require.Len(t, updated.SchemaDefinition, 2)

			existing := updated.SchemaDefinition[0]
			assert.Equal(t, "department_id", existing.Name)
			assert.Equal(t, "部门ID", existing.DisplayName)
			assert.Equal(t, "部门的唯一标识", existing.Description)
			assert.Equal(t, interfaces.DataType_String, existing.Type)
			assert.Equal(t, "varchar", existing.OriginalType)
			assert.Equal(t, "源端最新部门编号注释", existing.OriginalDescription)
			assert.Equal(t, []interfaces.PropertyFeature{{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}}, existing.Features)

			added := updated.SchemaDefinition[1]
			assert.Equal(t, "department_name", added.Name)
			assert.Equal(t, "department_name", added.DisplayName)
			assert.Equal(t, "部门名称", added.Description)
			assert.Equal(t, "部门名称", added.OriginalDescription)
			assert.Equal(t, []string{"department_id", "department_name"}, []string{existing.Name, added.Name})
			return nil
		})

	err := dh.enrichTableMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []tableDiscoverItem{{
		resource: resource,
		tableMeta: &interfaces.TableMeta{
			Name: "departments", Schema: "public",
		},
	}}, &interfaces.DiscoverResult{}, &discoverTaskReconcileProgress{lastProgress: 95})

	require.NoError(t, err)
}
