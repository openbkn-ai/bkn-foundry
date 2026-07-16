// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
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

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.Catalog{ID: "catalog-1"},
			[]*interfaces.IndexMeta{{Name: "idx-a"}}, nil, &actions)

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

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.Catalog{ID: "catalog-1"},
			[]*interfaces.IndexMeta{{Name: "idx-a"}},
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "idx-a",
				Category:         interfaces.ResourceCategoryIndex,
				Status:           interfaces.ResourceStatusStale,
			}}, &actions)

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

		result, items, err := dh.reconcileIndexResources(context.Background(), &interfaces.Catalog{ID: "catalog-1"},
			nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "idx-a",
				Category:         interfaces.ResourceCategoryIndex,
				Status:           interfaces.ResourceStatusActive,
			}}, &actions)

		require.NoError(t, err)
		assert.Equal(t, 1, result.StaleCount)
		assert.Empty(t, items)
	})
}
