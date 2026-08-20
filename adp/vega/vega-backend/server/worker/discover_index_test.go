// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestOpenSearchSubFieldFeatures(t *testing.T) {
	t.Run("maps opensearch sub field types to features", func(t *testing.T) {
		assert.Equal(t, interfaces.PropertyFeatureType_Keyword, osSubFieldTypeToFeatureType("keyword"))
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, osSubFieldTypeToFeatureType("text"))
		assert.Equal(t, interfaces.PropertyFeatureType_Vector, osSubFieldTypeToFeatureType("knn_vector"))
		assert.Empty(t, osSubFieldTypeToFeatureType("object"))
	})

	t.Run("builds supported sub field features", func(t *testing.T) {
		features := buildSubFieldFeatures("title", []interfaces.IndexSubFieldMeta{
			{Name: "keyword", Type: "keyword", Attributes: map[string]any{"ignore_above": 256}},
			{Name: "text", Type: "text", Attributes: map[string]any{"analyzer": "ik_max_word"}},
			{Name: "raw_object", Type: "object"},
		})

		require.Len(t, features, 2)
		assert.Equal(t, "title.keyword", features[0].FeatureName)
		assert.Equal(t, interfaces.PropertyFeatureType_Keyword, features[0].FeatureType)
		assert.Equal(t, map[string]any{"ignore_above": 256}, features[0].Config)
		assert.Equal(t, "title.text", features[1].FeatureName)
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, features[1].FeatureType)
	})
}

func TestReconcileIndexResources(t *testing.T) {
	t.Run("creates new index resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
		created := &interfaces.Resource{ID: "r1", SourceIdentifier: "idx-a", Category: interfaces.ResourceCategoryIndex}

		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				assert.Equal(t, "catalog-1", req.CatalogID)
				assert.Equal(t, "idx-a", req.Name)
				assert.Equal(t, interfaces.ResourceCategoryIndex, req.Category)
				assert.Equal(t, "idx-a", req.SourceIdentifier)
				return created, nil
			})
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "catalog-1"},
			[]*interfaces.IndexMeta{{Name: "idx-a"}}, nil)

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.Equal(t, "idx-a", items[0].indexMeta.Name)
		assert.Equal(t, interfaces.DiscoverStatusNew, items[0].resource.LastDiscoverStatus)
	})

	t.Run("restores stale existing index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusActive, "").Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusRestored).Return(nil)

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "catalog-1"},
			[]*interfaces.IndexMeta{{Name: "idx-a"}},
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "idx-a",
				Category:         interfaces.ResourceCategoryIndex,
				Status:           interfaces.ResourceStatusStale,
			}})

		require.NoError(t, err)
		assert.Equal(t, 1, result.RestoredCount)
		require.Len(t, items, 1)
		assert.False(t, items[0].markAfterEnrich)
		assert.Equal(t, interfaces.ResourceStatusActive, items[0].resource.Status)
	})

	t.Run("marks missing active index as stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusStale, "").Return(nil)

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "catalog-1"},
			nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "idx-a",
				Category:         interfaces.ResourceCategoryIndex,
				Status:           interfaces.ResourceStatusActive,
			}})

		require.NoError(t, err)
		assert.Equal(t, 1, result.StaleCount)
		assert.Empty(t, items)
	})
}

func TestEnrichIndexMetadataContinuesWhenOneIndexFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	inaccessible := &interfaces.Resource{ID: "r1", SourceIdentifier: "unavailable", LastDiscoverStatus: interfaces.DiscoverStatusNew}
	accessible := &interfaces.Resource{
		ID:                 "r2",
		SourceIdentifier:   "products",
		LastDiscoverStatus: interfaces.DiscoverStatusNew,
		StatusMessage:      "discover metadata failed: previous failure",
		SourceMetadata:     map[string]any{"original_name": "products"},
	}
	connector := vmock.NewMockIndexConnector(ctrl)
	connector.EXPECT().
		GetIndexMeta(gomock.Any(), &interfaces.IndexMeta{Name: "unavailable"}).
		Return(errors.New("permission denied"))
	connector.EXPECT().
		GetIndexMeta(gomock.Any(), &interfaces.IndexMeta{Name: "products"}).
		DoAndReturn(func(_ context.Context, index *interfaces.IndexMeta) error {
			index.Mapping = map[string]interfaces.IndexFieldMeta{
				"id": {Name: "id", Type: "long"},
			}
			return nil
		})
	connector.EXPECT().MapType("long").Return(interfaces.DataType_Integer)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) error {
			assert.Equal(t, "r1", resource.ID)
			assert.Equal(t, interfaces.DiscoverStatusError, resource.LastDiscoverStatus)
			assert.NotEmpty(t, resource.StatusMessage)
			return nil
		})
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) error {
			assert.Equal(t, "r2", resource.ID)
			require.Len(t, resource.SchemaDefinition, 1)
			assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
			assert.Equal(t, interfaces.DataType_Integer, resource.SchemaDefinition[0].Type)
			assert.Empty(t, resource.StatusMessage)
			return nil
		})

	result := &interfaces.DiscoverResult{}
	err := dh.enrichIndexMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []indexDiscoverItem{
		{resource: inaccessible, indexMeta: &interfaces.IndexMeta{Name: "unavailable"}},
		{resource: accessible, indexMeta: &interfaces.IndexMeta{Name: "products"}},
	}, result, &discoverTaskReconcileProgress{lastProgress: 95})

	require.NoError(t, err)
	assert.Equal(t, 1, result.FailedCount)
}

func TestEnrichIndexMetadataPreservesBusinessMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{
		ID:               "r1",
		SourceIdentifier: "products",
		SchemaDefinition: []*interfaces.Property{{
			Name:        "title",
			DisplayName: "商品标题",
			Description: "人工字段说明",
			Features: []interfaces.PropertyFeature{{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}},
		}},
	}
	connector := vmock.NewMockIndexConnector(ctrl)
	connector.EXPECT().
		GetIndexMeta(gomock.Any(), &interfaces.IndexMeta{Name: "products"}).
		DoAndReturn(func(_ context.Context, index *interfaces.IndexMeta) error {
			index.Mapping = map[string]interfaces.IndexFieldMeta{
				"title": {
					Name:       "title",
					Type:       "text",
					Attributes: map[string]any{"type": "text", "analyzer": "standard"},
				},
			}
			return nil
		})
	connector.EXPECT().MapType("text").Return(interfaces.DataType_Text)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, updated *interfaces.Resource, _ int64) error {
			require.Len(t, updated.SchemaDefinition, 1)
			field := updated.SchemaDefinition[0]
			assert.Equal(t, "商品标题", field.DisplayName)
			assert.Equal(t, "人工字段说明", field.Description)
			assert.Equal(t, interfaces.DataType_Text, field.Type)
			assert.Equal(t, "text", field.OriginalType)
			assert.Equal(t, map[string]any{"analyzer": "standard"}, field.Attributes)
			assert.Equal(t, []interfaces.PropertyFeature{{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}}, field.Features)
			return nil
		})

	err := dh.enrichIndexMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []indexDiscoverItem{{
		resource:  resource,
		indexMeta: &interfaces.IndexMeta{Name: "products"},
	}}, &interfaces.DiscoverResult{}, &discoverTaskReconcileProgress{lastProgress: 95})

	require.NoError(t, err)
}

func TestEnrichIndexMetadataSynchronizesSourceDescriptions(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := vmock.NewMockResourceService(ctrl)
	dh := &DiscoverTaskWorker{rs: rs}
	resource := &interfaces.Resource{
		ID:          "r1",
		Description: "旧索引说明",
		SourceMetadata: map[string]any{
			"original_description": "旧索引说明",
		},
		SchemaDefinition: []*interfaces.Property{
			{Name: "empty", Description: ""},
			{Name: "source", Description: "旧字段说明", OriginalDescription: "旧字段说明"},
			{Name: "manual", Description: "人工字段说明", OriginalDescription: "旧字段说明"},
		},
	}
	connector := vmock.NewMockIndexConnector(ctrl)
	connector.EXPECT().
		GetIndexMeta(gomock.Any(), &interfaces.IndexMeta{Name: "products"}).
		DoAndReturn(func(_ context.Context, index *interfaces.IndexMeta) error {
			index.Description = "新索引说明"
			index.MappingMeta = map[string]any{"description": "新 mapping 说明", "owner": "search-team"}
			index.Mapping = map[string]interfaces.IndexFieldMeta{
				"empty":  {Name: "empty", Type: "keyword", Description: "新字段说明"},
				"source": {Name: "source", Type: "keyword", Description: "新字段说明"},
				"manual": {Name: "manual", Type: "keyword", Description: "新字段说明"},
			}
			return nil
		})
	connector.EXPECT().MapType("keyword").Return(interfaces.DataType_String).Times(3)
	rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, updated *interfaces.Resource, _ int64) error {
			assert.Equal(t, "新索引说明", updated.Description)
			assert.Equal(t, "新索引说明", updated.SourceMetadata["original_description"])
			assert.Equal(t, map[string]any{"description": "新 mapping 说明", "owner": "search-team"}, updated.SourceMetadata["mapping_meta"])
			fields := make(map[string]*interfaces.Property, len(updated.SchemaDefinition))
			for _, field := range updated.SchemaDefinition {
				fields[field.Name] = field
			}
			assert.Equal(t, "新字段说明", fields["empty"].Description)
			assert.Equal(t, "新字段说明", fields["source"].Description)
			assert.Equal(t, "新字段说明", fields["source"].OriginalDescription)
			assert.Equal(t, "人工字段说明", fields["manual"].Description)
			assert.Equal(t, "新字段说明", fields["manual"].OriginalDescription)
			return nil
		})

	err := dh.enrichIndexMetadata(context.Background(), &interfaces.DiscoverTask{}, connector, []indexDiscoverItem{{
		resource:  resource,
		indexMeta: &interfaces.IndexMeta{Name: "products"},
	}}, &interfaces.DiscoverResult{}, &discoverTaskReconcileProgress{lastProgress: 95})

	require.NoError(t, err)
}
