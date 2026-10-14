// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
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
		connector := vmock.NewMockTableConnector(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		created := &interfaces.Resource{ID: "r1", SourceIdentifier: "users", Status: interfaces.ResourceStatusActive}
		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				require.NotNil(t, req.Builtin)
				assert.True(t, *req.Builtin)
				assert.Equal(t, "users", req.Name)
				assert.Equal(t, "app", req.Schema)
				assert.Equal(t, "source comment", req.Description)
				assert.Empty(t, req.SourceMetadata)
				return created, nil
			})
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)
		connector.EXPECT().BuildSourceIdentifier(gomock.Any()).Return("users").AnyTimes()
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1", Builtin: true},
			[]*interfaces.TableMeta{{Name: "users", Schema: "app", Database: "app", Description: "source comment"}}, nil, connector)

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
	})

	t.Run("refreshes missing status when already stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		connector.EXPECT().BuildSourceIdentifier(gomock.Any()).Return("users").AnyTimes()
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Status: interfaces.ResourceStatusStale}}, connector)

		require.NoError(t, err)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("quoted reference matches existing resource ID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		worker := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
		reference := "`sales.eu`.`order.items`"
		connector.EXPECT().BuildSourceIdentifier(gomock.Any()).Return(reference)
		resource := &interfaces.Resource{ID: "stable-id", Category: interfaces.ResourceCategoryTable, SourceIdentifier: reference, Status: interfaces.ResourceStatusActive}
		table := &interfaces.TableMeta{Database: "sales.eu", Schema: "sales.eu", Name: "order.items"}
		result, items, err := worker.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "c1"}, []*interfaces.TableMeta{table}, []*interfaces.Resource{resource}, connector)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Same(t, resource, items[0].resource)
		assert.Equal(t, "stable-id", items[0].resource.ID)
		assert.Zero(t, result.NewCount)
		assert.Zero(t, result.StaleCount)
	})

	t.Run("does not disable user-disabled resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusStale, "").Return(nil)
		connector.EXPECT().BuildSourceIdentifier(gomock.Any()).Return("users").AnyTimes()
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, _, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"}, nil,
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Enabled: false, Status: interfaces.ResourceStatusActive}}, connector)

		require.NoError(t, err)
		assert.Equal(t, 1, result.StaleCount)
	})

	t.Run("marks restored stale table", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		connector := vmock.NewMockTableConnector(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusActive, "").Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusRestored).Return(nil)
		connector.EXPECT().BuildSourceIdentifier(gomock.Any()).Return("users").AnyTimes()
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		result, items, err := dh.reconcileTableResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "cat1"},
			[]*interfaces.TableMeta{{Name: "users"}},
			[]*interfaces.Resource{{ID: "r1", SourceIdentifier: "users", Category: interfaces.ResourceCategoryTable, Status: interfaces.ResourceStatusStale}}, connector)

		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
		assert.Equal(t, 1, result.RestoredCount)
		assert.Zero(t, result.UnchangedCount)
	})
}

func TestEnrichTableMetadataContinuesWhenOneTableFails(t *testing.T) {
	t.Run("continues when one table fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		inaccessible := &interfaces.Resource{ID: "r1", SourceIdentifier: "public.no_access", LastDiscoverStatus: interfaces.DiscoverStatusNew}
		accessible := &interfaces.Resource{
			ID:                 "r2",
			SourceIdentifier:   `"public"."erp_material"`,
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
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) error {
				assert.Equal(t, "r1", resource.ID)
				meta, err := (&postgresql.PostgresqlConnector{}).DecodeTableMeta(resource.SourceMetadata)
				require.NoError(t, err)
				assert.Equal(t, "no_access", meta.Name)
				assert.Equal(t, "public", meta.Schema)
				assert.NotContains(t, resource.SourceMetadata, "original_name")
				assert.Equal(t, interfaces.DiscoverStatusError, resource.LastDiscoverStatus)
				assert.NotEmpty(t, resource.StatusMessage)
				return nil
			})
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) error {
				assert.Equal(t, "r2", resource.ID)
				require.Len(t, resource.SchemaDefinition, 1)
				assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
				assert.NotContains(t, resource.SourceMetadata, "original_name")
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
		Description:      "人工资源说明",
		SourceIdentifier: "public.departments",
		SourceMetadata: map[string]any{
			"description":          "旧源端表注释",
			"original_description": "遗留源端表注释",
			"original_name":        "public.departments",
			"legacy_extra":         "obsolete",
			"primary_keys":         []string{"obsolete_column"},
			"indices":              []interfaces.TableIndexMeta{{Name: "obsolete_index"}},
			"foreign_keys":         []interfaces.TableForeignKeyMeta{{}},
			"properties":           map[string]any{"row_count": int64(42), "row_count_time": int64(100)},
		},
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
			table.Description = "最新源端表注释"
			table.Properties = map[string]any{"estimated_row_count": int64(99)}
			table.Columns = []interfaces.TableColumnMeta{
				{Name: "department_id", Type: "varchar", Description: "源端最新部门编号注释"},
				{Name: "department_name", Type: "varchar", Description: "部门名称"},
			}
			return nil
		})
	connector.EXPECT().MapType("varchar").Return(interfaces.DataType_String).Times(2)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, updated *interfaces.Resource, _ int64) error {
			assert.Equal(t, "人工资源说明", updated.Description)
			assert.Positive(t, updated.LastDiscoverTime)
			assert.Equal(t, map[string]any{"estimated_row_count": json.Number("99")}, updated.SourceMetadata["properties"])
			assert.Equal(t, "", updated.SourceMetadata["database"])
			assert.Equal(t, "public", updated.SourceMetadata["schema"])
			assert.Equal(t, "departments", updated.SourceMetadata["name"])
			assert.NotContains(t, updated.SourceMetadata, "original_name")
			assert.NotContains(t, updated.SourceMetadata, "table_meta")
			assert.NotContains(t, updated.SourceMetadata, "legacy_extra")
			assert.NotContains(t, updated.SourceMetadata, "original_description")
			assert.Empty(t, updated.SourceMetadata["primary_keys"])
			assert.Empty(t, updated.SourceMetadata["indices"])
			assert.Empty(t, updated.SourceMetadata["foreign_keys"])
			assert.Equal(t, "最新源端表注释", updated.SourceMetadata["description"])
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

func TestEnrichTableMetadataUsesNormalizedEnumType(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{ID: "r1", SourceIdentifier: "public.orders"}
	connector := vmock.NewMockTableConnector(ctrl)
	connector.EXPECT().
		GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "orders", Schema: "public"}).
		DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
			table.Columns = []interfaces.TableColumnMeta{{
				Name: "status", Type: "enum", AliasType: "order_status",
			}}
			return nil
		})
	connector.EXPECT().MapType("enum").Return(interfaces.DataType_String)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, updated *interfaces.Resource, _ int64) error {
			require.Len(t, updated.SchemaDefinition, 1)
			assert.Equal(t, interfaces.DataType_String, updated.SchemaDefinition[0].Type)
			assert.Equal(t, "enum", updated.SchemaDefinition[0].OriginalType)
			return nil
		})

	err := dh.enrichTableMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []tableDiscoverItem{{
		resource:  resource,
		tableMeta: &interfaces.TableMeta{Name: "orders", Schema: "public"},
	}}, &interfaces.DiscoverResult{}, &discoverTaskReconcileProgress{lastProgress: 95})

	require.NoError(t, err)
}

func TestEnrichTableMetadataSynchronizesSourceDescriptions(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{
		ID:               "r1",
		Description:      "旧源端表注释",
		SourceIdentifier: "public.departments",
		SourceMetadata: map[string]any{
			"description": "旧源端表注释",
		},
		SchemaDefinition: []*interfaces.Property{
			{Name: "empty_description", Description: "", OriginalDescription: ""},
			{Name: "source_description", Description: "旧源端字段注释", OriginalDescription: "旧源端字段注释"},
			{Name: "business_description", Description: "人工字段说明", OriginalDescription: "旧源端字段注释"},
		},
	}
	connector := vmock.NewMockTableConnector(ctrl)
	connector.EXPECT().
		GetTableMeta(gomock.Any(), &interfaces.TableMeta{Name: "departments", Schema: "public"}).
		DoAndReturn(func(_ context.Context, table *interfaces.TableMeta) error {
			table.Description = "最新源端表注释"
			table.Columns = []interfaces.TableColumnMeta{
				{Name: "empty_description", Type: "varchar", Description: "最新空字段注释"},
				{Name: "source_description", Type: "varchar", Description: "最新源端字段注释"},
				{Name: "business_description", Type: "varchar", Description: "最新源端字段注释"},
			}
			return nil
		})
	connector.EXPECT().MapType("varchar").Return(interfaces.DataType_String).Times(3)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, updated *interfaces.Resource, _ int64) error {
			assert.Equal(t, "最新源端表注释", updated.Description)
			assert.Equal(t, "最新源端表注释", updated.SourceMetadata["description"])
			require.Len(t, updated.SchemaDefinition, 3)
			assert.Equal(t, "最新空字段注释", updated.SchemaDefinition[0].Description)
			assert.Equal(t, "最新源端字段注释", updated.SchemaDefinition[1].Description)
			assert.Equal(t, "人工字段说明", updated.SchemaDefinition[2].Description)
			for _, property := range updated.SchemaDefinition {
				assert.NotEmpty(t, property.OriginalDescription)
			}
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

func TestResolveSourceDescription(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		description           string
		originalDescription   string
		discoveredDescription string
		want                  string
	}{
		{name: "empty business description", description: "", originalDescription: "旧源端注释", discoveredDescription: "新源端注释", want: "新源端注释"},
		{name: "unchanged source description", description: "旧源端注释", originalDescription: "旧源端注释", discoveredDescription: "新源端注释", want: "新源端注释"},
		{name: "manual business description", description: "人工说明", originalDescription: "旧源端注释", discoveredDescription: "新源端注释", want: "人工说明"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveSourceDescription(tc.description, tc.originalDescription, tc.discoveredDescription))
		})
	}
}

func TestEnrichTableMetadataCountFailure(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		existing, negative, saveFailure bool
	}{
		{name: "new table count timeout retains retry metadata"},
		{name: "existing table retains previous count", existing: true},
		{name: "negative count retains retry metadata", negative: true},
		{name: "metadata save failure is propagated", saveFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			connector := vmock.NewMockTableConnector(ctrl)
			resource := &interfaces.Resource{ID: "r1", Name: "Business Orders", Description: "Business description", SourceIdentifier: `"sales"."orders"`, UpdateTime: 20}
			oldCount, oldTime := int64(7), int64(10)
			if tc.existing {
				resource.RowCount, resource.RowCountTime = &oldCount, &oldTime
				resource.SourceMetadata = map[string]any{"name": "orders", "schema": "sales", "description": "old comment"}
			}
			table := &interfaces.TableMeta{Name: "orders", Schema: "sales", Database: "app"}
			connector.EXPECT().GetTableMeta(gomock.Any(), table).DoAndReturn(func(_ context.Context, meta *interfaces.TableMeta) error {
				meta.Description = "new comment"
				meta.Columns = []interfaces.TableColumnMeta{{Name: "id", Type: "int4"}}
				return nil
			})
			if tc.negative {
				connector.EXPECT().CountRows(gomock.Any(), resource.SourceIdentifier).Return(int64(-1), nil)
			} else {
				connector.EXPECT().CountRows(gomock.Any(), resource.SourceIdentifier).Return(int64(0), context.DeadlineExceeded)
			}
			connector.EXPECT().MapType("int4").Return(interfaces.DataType_Integer)
			rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, resource, int64(20)).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Resource, _ int64) error {
				assert.Equal(t, "Business Orders", saved.Name)
				assert.Equal(t, "Business description", saved.Description)
				assert.Equal(t, interfaces.DiscoverStatusError, saved.LastDiscoverStatus)
				assert.Contains(t, saved.StatusMessage, "count")
				assert.Positive(t, saved.LastDiscoverTime)
				require.Len(t, saved.SchemaDefinition, 1)
				assert.Equal(t, "id", saved.SchemaDefinition[0].Name)
				meta, err := (&postgresql.PostgresqlConnector{}).DecodeTableMeta(saved.SourceMetadata)
				require.NoError(t, err)
				assert.Equal(t, "orders", meta.Name)
				assert.Equal(t, "sales", meta.Schema)
				assert.Equal(t, "new comment", meta.Description)
				if tc.existing {
					require.NotNil(t, saved.RowCount)
					require.NotNil(t, saved.RowCountTime)
					assert.Equal(t, oldCount, *saved.RowCount)
					assert.Equal(t, oldTime, *saved.RowCountTime)
				} else {
					assert.Nil(t, saved.RowCount)
					assert.Nil(t, saved.RowCountTime)
				}
				if tc.saveFailure {
					return errors.New("save failed")
				}
				return nil
			})
			result := &interfaces.DiscoverResult{}
			err := (&DiscoverTaskWorker{rs: rs}).enrichTableMetadata(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &interfaces.DiscoverActions{Count: true}}, connector, []tableDiscoverItem{{resource: resource, tableMeta: table, markAfterEnrich: tc.existing}}, result, &discoverTaskReconcileProgress{lastProgress: 95})
			if tc.saveFailure {
				require.ErrorContains(t, err, "save failed")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, 1, result.FailedCount)
			assert.Zero(t, result.UpdatedCount)
		})
	}
}
