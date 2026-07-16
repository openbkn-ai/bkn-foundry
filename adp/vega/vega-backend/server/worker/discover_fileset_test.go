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

func TestFilesetSourceIdentifier(t *testing.T) {
	t.Run("uses display path then id fallback", func(t *testing.T) {
		assert.Equal(t, "/team/docs", filesetSourceIdentifier(&interfaces.FilesetMeta{ID: "fs-1", DisplayPath: "/team/docs"}))
		assert.Equal(t, "fs-1", filesetSourceIdentifier(&interfaces.FilesetMeta{ID: "fs-1"}))
	})
}

func TestReconcileFilesetResources(t *testing.T) {
	t.Run("creates new fileset resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
		created := &interfaces.Resource{ID: "r1", SourceIdentifier: "/team/docs", Category: interfaces.ResourceCategoryFileset}

		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				assert.Equal(t, "catalog-1", req.CatalogID)
				assert.Equal(t, "Docs", req.Name)
				assert.Equal(t, interfaces.ResourceCategoryFileset, req.Category)
				assert.Equal(t, "/team/docs", req.SourceIdentifier)
				assert.Equal(t, "Docs", req.SourceMetadata["original_name"])
				return created, nil
			})
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)

		result, items, err := dh.reconcileFilesetResources(context.Background(), &interfaces.Catalog{ID: "catalog-1"},
			[]*interfaces.FilesetMeta{{ID: "fs-1", Name: "Docs", DisplayPath: "/team/docs"}}, nil, &actions)

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		require.Len(t, items, 1)
		assert.Equal(t, "Docs", items[0].meta.Name)
		assert.Equal(t, interfaces.DiscoverStatusNew, items[0].resource.LastDiscoverStatus)
	})

	t.Run("marks active missing fileset stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)

		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusMissing).Return(nil)
		rs.EXPECT().UpdateStatus(gomock.Any(), "r1", interfaces.ResourceStatusStale, "").Return(nil)

		result, items, err := dh.reconcileFilesetResources(context.Background(), &interfaces.Catalog{ID: "catalog-1"},
			nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "/team/docs",
				Category:         interfaces.ResourceCategoryFileset,
				Status:           interfaces.ResourceStatusActive,
			}}, &actions)

		require.NoError(t, err)
		assert.Equal(t, 1, result.StaleCount)
		assert.Empty(t, items)
	})
}

func TestEnrichFilesetMetadata(t *testing.T) {
	t.Run("preserves existing metadata and enriches columns", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dh := &DiscoverTaskWorker{rs: rs}
		resource := &interfaces.Resource{
			ID:                 "r1",
			SourceIdentifier:   "/team/docs",
			LastDiscoverStatus: interfaces.DiscoverStatusNew,
			SourceMetadata:     map[string]any{"keep": "value"},
		}

		rs.EXPECT().UpdateResource(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Resource{})).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				assert.Equal(t, "value", got.SourceMetadata["keep"])
				assert.Equal(t, "Docs", got.SourceMetadata["original_name"])
				assert.Equal(t, "", got.SourceMetadata["original_description"])
				assert.Equal(t, []interfaces.FilesetColumnMeta{{Name: "title", Type: "string"}}, got.SourceMetadata["columns"])
				require.Len(t, got.SchemaDefinition, 1)
				assert.Equal(t, "title", got.SchemaDefinition[0].Name)
				assert.Equal(t, "string", got.SchemaDefinition[0].Type)
				assert.Equal(t, interfaces.DiscoverStatusNew, got.LastDiscoverStatus)
				return nil
			})

		result := &interfaces.DiscoverResult{}
		err := dh.enrichFilesetMetadata(context.Background(), []filesetDiscoverItem{{
			resource: resource,
			meta: &interfaces.FilesetMeta{
				ID:             "fs-1",
				Name:           "Docs",
				SourceMetadata: map[string]any{"owner": "team-a"},
				Columns:        []interfaces.FilesetColumnMeta{{Name: "title", Type: "string"}},
			},
		}}, result)

		require.NoError(t, err)
	})
}
