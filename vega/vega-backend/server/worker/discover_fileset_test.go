// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
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
				require.NotNil(t, req.Builtin)
				assert.True(t, *req.Builtin)
				assert.Equal(t, "Docs", req.Name)
				assert.Equal(t, interfaces.ResourceCategoryFileset, req.Category)
				assert.Equal(t, "/team/docs", req.SourceIdentifier)
				assert.Equal(t, "Docs", req.SourceMetadata["original_name"])
				return created, nil
			})
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusNew).Return(nil)

		result, items, err := dh.reconcileFilesetResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "catalog-1", Builtin: true},
			[]*interfaces.FilesetMeta{{ID: "fs-1", Name: "Docs", DisplayPath: "/team/docs"}}, nil)

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

		result, items, err := dh.reconcileFilesetResources(context.Background(), &interfaces.DiscoverTask{DiscoverActions: &actions}, &interfaces.Catalog{ID: "catalog-1"},
			nil,
			[]*interfaces.Resource{{
				ID:               "r1",
				SourceIdentifier: "/team/docs",
				Category:         interfaces.ResourceCategoryFileset,
				Enabled:          true,
				Status:           interfaces.ResourceStatusActive,
			}})

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

		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, expected int64) error {
				assert.Equal(t, int64(0), expected)
				assert.Greater(t, got.UpdateTime, expected)
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
		err := dh.enrichFilesetMetadata(context.Background(), &interfaces.DiscoverTask{}, []filesetDiscoverItem{{
			resource: resource,
			meta: &interfaces.FilesetMeta{
				ID:             "fs-1",
				Name:           "Docs",
				SourceMetadata: map[string]any{"owner": "team-a"},
				Columns:        []interfaces.FilesetColumnMeta{{Name: "title", Type: "string"}},
			},
		}}, result, &discoverTaskReconcileProgress{lastProgress: 95})

		require.NoError(t, err)
	})
}

// pagedFilesetConnector is a fileset connector that also implements PagedFilesetLister.
type pagedFilesetConnector struct {
	*vmock.MockFilesetConnector
	*vmock.MockPagedFilesetLister
}

func newPagedFilesetConnector(ctrl *gomock.Controller) pagedFilesetConnector {
	return pagedFilesetConnector{
		MockFilesetConnector:   vmock.NewMockFilesetConnector(ctrl),
		MockPagedFilesetLister: vmock.NewMockPagedFilesetLister(ctrl),
	}
}

func TestDiscoverFilesetResources(t *testing.T) {
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
	task := &interfaces.DiscoverTask{ID: "task-1", Creator: interfaces.AccountInfo{ID: "u1"}, DiscoverActions: &actions}
	catalog := &interfaces.Catalog{ID: "catalog-1"}

	t.Run("marks missing resources stale only after every page is listed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}
		kept := &interfaces.Resource{ID: "r-kept", SourceIdentifier: "/a", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}
		gone := &interfaces.Resource{ID: "r-gone", SourceIdentifier: "/gone", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}

		gomock.InOrder(
			connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", filesetListPageSize).
				Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a", Name: "A", DisplayPath: "/a"}}, NextCursor: "c1"}, nil),
			connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", filesetListPageSize).
				Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "b", Name: "B", DisplayPath: "/b"}}}, nil),
			rs.EXPECT().InternalGetByCatalogID(gomock.Any(), "catalog-1").Return([]*interfaces.Resource{kept, gone}, nil),
		)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
				assert.Equal(t, "/b", req.SourceIdentifier)
				return &interfaces.Resource{ID: "r-b", SourceIdentifier: "/b", Category: interfaces.ResourceCategoryFileset}, nil
			})
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-b", interfaces.DiscoverStatusNew).Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-gone", interfaces.DiscoverStatusMissing).Return(nil)
		rs.EXPECT().UpdateStatus(gomock.Any(), "r-gone", interfaces.ResourceStatusStale, "").Return(nil)
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).
			Return(nil).Times(2)

		result, err := dtw.discoverFilesetResources(context.Background(), task, catalog, connector, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		assert.Equal(t, 1, result.StaleCount)
	})

	t.Run("failure on a later page marks nothing stale and writes no resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}
		listErr := errors.New("list objects timeout")

		connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", filesetListPageSize).
			Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a", DisplayPath: "/a"}}, NextCursor: "c1"}, nil)
		connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", filesetListPageSize).Return(nil, listErr)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 0, "source filesets listed: page 1, 1 objects").Return(true, nil)
		// rs has no expectations: any InternalGetByCatalogID, Create or UpdateStatus call fails the test.

		result, err := dtw.discoverFilesetResources(context.Background(), task, catalog, connector, &discoverTaskReconcileProgress{})

		require.ErrorIs(t, err, listErr)
		assert.Nil(t, result)
	})
}

func TestListSourceFilesets(t *testing.T) {
	task := &interfaces.DiscoverTask{ID: "task-1"}

	t.Run("falls back to ListFilesets without progress updates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{dts: vmock.NewMockDiscoverTaskService(ctrl)}
		want := []*interfaces.FilesetMeta{{ID: "lib-1"}}
		connector.EXPECT().ListFilesets(gomock.Any()).Return(want, nil)

		got, err := dtw.listSourceFilesets(context.Background(), task, connector, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("concatenates pages and reports progress per page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{dts: dts}

		gomock.InOrder(
			connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", filesetListPageSize).
				Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a"}, {ID: "b"}}, NextCursor: "c1"}, nil),
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 3, "source filesets listed: page 1, 2 objects").Return(true, nil),
			connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", filesetListPageSize).
				Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "c"}}}, nil),
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 3, "source filesets listed: page 2, 3 objects").Return(true, nil),
		)

		got, err := dtw.listSourceFilesets(context.Background(), task, connector, &discoverTaskReconcileProgress{lastProgress: 3})

		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []string{"a", "b", "c"}, []string{got[0].ID, got[1].ID, got[2].ID})
	})

	t.Run("rejects invalid pages", func(t *testing.T) {
		cases := []struct {
			name    string
			second  *interfaces.FilesetPage
			wantErr string
		}{
			{"repeated cursor", &interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "b"}}, NextCursor: "c1"}, `cursor "c1" repeated`},
			{"nil page", nil, "connector returned no page"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				dts := vmock.NewMockDiscoverTaskService(ctrl)
				connector := newPagedFilesetConnector(ctrl)
				dtw := &DiscoverTaskWorker{dts: dts}
				connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", filesetListPageSize).
					Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a"}}, NextCursor: "c1"}, nil)
				connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", filesetListPageSize).Return(tc.second, nil)
				dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

				got, err := dtw.listSourceFilesets(context.Background(), task, connector, &discoverTaskReconcileProgress{})

				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, got)
			})
		}
	})

	t.Run("stops at the page cap", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{dts: dts}
		calls := 0
		connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), gomock.Any(), filesetListPageSize).
			DoAndReturn(func(context.Context, string, int) (*interfaces.FilesetPage, error) {
				calls++
				return &interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: fmt.Sprint(calls)}}, NextCursor: fmt.Sprintf("c%d", calls)}, nil
			}).Times(filesetListMaxPages)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).Times(filesetListMaxPages)

		got, err := dtw.listSourceFilesets(context.Background(), task, connector, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("exceeded %d pages", filesetListMaxPages))
		assert.Nil(t, got)
	})

	t.Run("returns progress update failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{dts: dts}
		connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", filesetListPageSize).
			Return(&interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a"}}, NextCursor: "c1"}, nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(false, nil)

		got, err := dtw.listSourceFilesets(context.Background(), task, connector, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "progress was not updated")
		assert.Nil(t, got)
	})
}
