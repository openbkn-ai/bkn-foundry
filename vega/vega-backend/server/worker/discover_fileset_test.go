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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
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

// fakeFilesetPages serves pages of generated filesets "/obj-<n>" and records the limits it was asked for.
type fakeFilesetPages struct {
	total      int
	maxPerPage int
	calls      int
}

func (f *fakeFilesetPages) ListFilesetsPage(_ context.Context, cursor string, limit int) (*interfaces.FilesetPage, error) {
	f.calls++
	if limit > f.maxPerPage {
		f.maxPerPage = limit
	}
	start := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &start); err != nil {
			return nil, err
		}
	}
	end := min(start+limit, f.total)
	page := &interfaces.FilesetPage{Items: make([]*interfaces.FilesetMeta, 0, end-start)}
	for i := start; i < end; i++ {
		page.Items = append(page.Items, &interfaces.FilesetMeta{ID: fmt.Sprint(i), Name: fmt.Sprint(i), DisplayPath: fmt.Sprintf("/obj-%d", i)})
	}
	if end < f.total {
		page.NextCursor = fmt.Sprint(end)
	}
	return page, nil
}

// fakeFilesetResourceService is an in-memory ResourceService for large paged discovery runs; any method
// not overridden here panics through the nil embedded interface, so the full-catalog load path cannot be used.
type fakeFilesetResourceService struct {
	interfaces.ResourceService
	resources       map[string]*interfaces.Resource
	maxIDsPerLookup int
	created         int
	staled          []string
}

func (f *fakeFilesetResourceService) InternalGetSourceRefsByCatalogID(_ context.Context, _ string, _ string) ([]interfaces.ResourceSourceRef, error) {
	refs := make([]interfaces.ResourceSourceRef, 0, len(f.resources))
	for _, r := range f.resources {
		refs = append(refs, interfaces.ResourceSourceRef{ID: r.ID, SourceIdentifier: r.SourceIdentifier, Status: r.Status})
	}
	return refs, nil
}

func (f *fakeFilesetResourceService) InternalGetByIDs(_ context.Context, ids []string) (map[string]*interfaces.Resource, error) {
	f.maxIDsPerLookup = max(f.maxIDsPerLookup, len(ids))
	out := make(map[string]*interfaces.Resource, len(ids))
	for _, id := range ids {
		copied := *f.resources[id]
		out[id] = &copied
	}
	return out, nil
}

func (f *fakeFilesetResourceService) Create(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	f.created++
	r := &interfaces.Resource{ID: "new-" + req.SourceIdentifier, SourceIdentifier: req.SourceIdentifier,
		Category: req.Category, Status: req.Status, SourceMetadata: req.SourceMetadata}
	f.resources[r.ID] = r
	return r, nil
}

func (f *fakeFilesetResourceService) UpdateDiscoverStatus(context.Context, string, string) error {
	return nil
}

func (f *fakeFilesetResourceService) UpdateStatus(_ context.Context, id string, status string, _ string) error {
	if status == interfaces.ResourceStatusStale {
		f.staled = append(f.staled, id)
	}
	f.resources[id].Status = status
	return nil
}

func (f *fakeFilesetResourceService) InternalUpdateDiscoveryMetadata(context.Context, *sql.Tx, *interfaces.Resource, int64) error {
	return nil
}

func TestDiscoverFilesetResources(t *testing.T) {
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
	task := &interfaces.DiscoverTask{ID: "task-1", DiscoverActions: &actions}
	catalog := &interfaces.Catalog{ID: "catalog-1"}

	t.Run("connector without paging keeps the single ListFilesets path", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := vmock.NewMockFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		connector.EXPECT().ListFilesets(gomock.Any()).Return(nil, nil)
		rs.EXPECT().InternalGetByCatalogID(gomock.Any(), "catalog-1").Return(nil, nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

		result, err := dtw.discoverFilesetResources(context.Background(), task, catalog, connector, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, 0, result.NewCount)
	})

	t.Run("paged connector uses lightweight refs instead of loading the catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		connector := newPagedFilesetConnector(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		connector.MockPagedFilesetLister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(&interfaces.FilesetPage{}, nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

		result, err := dtw.discoverFilesetResources(context.Background(), task, catalog, connector, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, "Discover completed: 0 new, 0 stale, 0 unchanged, 0 updated, 0 restored, 0 failed", result.Message)
	})
}

func TestDiscoverPagedFilesetResources(t *testing.T) {
	actions := interfaces.ActionsFromDiscoverStrategy(interfaces.DiscoverStrategyFullSync)
	task := &interfaces.DiscoverTask{ID: "task-1", Creator: interfaces.AccountInfo{ID: "u1"}, DiscoverActions: &actions}
	catalog := &interfaces.Catalog{ID: "catalog-1"}
	page := func(cursor string, paths ...string) *interfaces.FilesetPage {
		p := &interfaces.FilesetPage{NextCursor: cursor}
		for _, path := range paths {
			p.Items = append(p.Items, &interfaces.FilesetMeta{ID: path, Name: path, DisplayPath: path})
		}
		return p
	}

	t.Run("applies every page and marks missing resources stale after the last page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts, filesetPageSizeSetting: 1}
		kept := &interfaces.Resource{ID: "r-a", SourceIdentifier: "/a", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return([]interfaces.ResourceSourceRef{
			{ID: "r-a", SourceIdentifier: "/a", Status: interfaces.ResourceStatusActive},
			{ID: "r-gone", SourceIdentifier: "/gone", Status: interfaces.ResourceStatusActive},
			{ID: "r-old", SourceIdentifier: "/old", Status: interfaces.ResourceStatusStale},
		}, nil)
		gomock.InOrder(
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 1).Return(page("c1", "/a"), nil),
			rs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"r-a"}).Return(map[string]*interfaces.Resource{"r-a": kept}, nil),
			rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).Return(nil),
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 0, "filesets processed: page 1, 1 objects (0 new, 1 updated, 0 unchanged, 0 restored)").Return(true, nil),
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", 1).Return(page("", "/b"), nil),
			rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
				Return(&interfaces.Resource{ID: "r-b", SourceIdentifier: "/b", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}, nil),
			rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-b", interfaces.DiscoverStatusNew).Return(nil),
			rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).Return(nil),
			dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 0, "filesets processed: page 2, 2 objects (1 new, 1 updated, 0 unchanged, 0 restored)").Return(true, nil),
		)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-gone", interfaces.DiscoverStatusMissing).Return(nil)
		rs.EXPECT().UpdateStatus(gomock.Any(), "r-gone", interfaces.ResourceStatusStale, "").Return(nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-old", interfaces.DiscoverStatusMissing).Return(nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", 95, "resources reconciled and metadata enriched").Return(true, nil)

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		assert.Equal(t, 1, result.UpdatedCount)
		assert.Equal(t, 1, result.StaleCount)
	})

	t.Run("failure on a later page keeps applied pages and marks nothing stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}
		listErr := interfaces.NewSourceReadForbiddenError(errors.New("access denied"))

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).
			Return([]interfaces.ResourceSourceRef{{ID: "r-gone", SourceIdentifier: "/gone", Status: interfaces.ResourceStatusActive}}, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(page("c1", "/a"), nil)
		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			Return(&interfaces.Resource{ID: "r-a", SourceIdentifier: "/a", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}, nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-a", interfaces.DiscoverStatusNew).Return(nil)
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).Return(nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", defaultFilesetPageSize).Return(nil, listErr)
		// No UpdateStatus or missing-status expectation for r-gone: marking it stale would fail the test.

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		var forbidden *interfaces.SourceReadForbiddenError
		require.ErrorAs(t, err, &forbidden)
		assert.Contains(t, err.Error(), `page 2 (cursor "c1")`)
		assert.Nil(t, result)
	})

	t.Run("stops when the task is no longer running", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(page("c1"), nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(false, nil)
		// No second ListFilesetsPage expectation: requesting page 2 after cancellation would fail the test.

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "stop fileset discovery after page 1")
		assert.Nil(t, result)
	})

	t.Run("rejects a repeated cursor", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(page("c1"), nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", defaultFilesetPageSize).Return(page("c1"), nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).Times(2)

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), `cursor "c1" repeated`)
		assert.Nil(t, result)
	})

	t.Run("stops a connector that keeps returning empty pages with new cursors", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}
		calls := 0

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).
			Return([]interfaces.ResourceSourceRef{{ID: "r-gone", SourceIdentifier: "/gone", Status: interfaces.ResourceStatusActive}}, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), gomock.Any(), defaultFilesetPageSize).
			DoAndReturn(func(context.Context, string, int) (*interfaces.FilesetPage, error) {
				calls++
				return &interfaces.FilesetPage{NextCursor: fmt.Sprintf("c%d", calls)}, nil
			}).Times(maxConsecutiveEmptyFilesetPages + 1)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).Times(maxConsecutiveEmptyFilesetPages + 1)
		// No stale expectation for r-gone: an aborted scan must not mark anything stale.

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("more than %d consecutive empty pages", maxConsecutiveEmptyFilesetPages))
		assert.Nil(t, result)
	})

	t.Run("tolerates occasional empty pages between non-empty ones", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		gomock.InOrder(
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(page("c1"), nil),
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", defaultFilesetPageSize).Return(page("", "/a"), nil),
		)
		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
			Return(&interfaces.Resource{ID: "r-a", SourceIdentifier: "/a", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}, nil)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-a", interfaces.DiscoverStatusNew).Return(nil)
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).Return(nil)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
	})

	t.Run("stops at the object safety limit before applying the page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts, filesetPageSizeSetting: 2, filesetMaxObjectsSetting: 1}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 2).Return(page("c1", "/a", "/b"), nil)

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "safety limit of 1 objects at page 1")
		assert.Nil(t, result)
	})

	t.Run("identifier repeated on a later page is matched, not created again", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}
		created := &interfaces.Resource{ID: "r-a", SourceIdentifier: "/a", Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}

		rs.EXPECT().InternalGetSourceRefsByCatalogID(gomock.Any(), "catalog-1", interfaces.ResourceCategoryFileset).Return(nil, nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", defaultFilesetPageSize).Return(page("c1", "/a"), nil)
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", defaultFilesetPageSize).Return(page("", "/a"), nil)
		rs.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).Return(created, nil).Times(1)
		rs.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r-a", interfaces.DiscoverStatusNew).Return(nil)
		rs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"r-a"}).Return(map[string]*interfaces.Resource{"r-a": created}, nil)
		rs.EXPECT().InternalUpdateDiscoveryMetadata(gomock.Any(), nil, gomock.AssignableToTypeOf(&interfaces.Resource{}), gomock.Any()).Return(nil).Times(2)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, lister, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, 1, result.NewCount)
		assert.Equal(t, 0, result.StaleCount)
	})

	t.Run("100k objects with page size 20 stay page-bounded and mark the right resources stale", func(t *testing.T) {
		const total = 100_000
		ctrl := gomock.NewController(t)
		dts := vmock.NewMockDiscoverTaskService(ctrl)
		dts.EXPECT().InternalUpdateProgress(gomock.Any(), "task-1", gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
		rs := &fakeFilesetResourceService{resources: make(map[string]*interfaces.Resource)}
		// Every even object already exists, plus 50 resources that are gone from the source.
		for i := 0; i < total; i += 2 {
			id := fmt.Sprintf("r-%d", i)
			rs.resources[id] = &interfaces.Resource{ID: id, SourceIdentifier: fmt.Sprintf("/obj-%d", i), Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}
		}
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("gone-%d", i)
			rs.resources[id] = &interfaces.Resource{ID: id, SourceIdentifier: fmt.Sprintf("/gone-%d", i), Category: interfaces.ResourceCategoryFileset, Status: interfaces.ResourceStatusActive}
		}
		pages := &fakeFilesetPages{total: total}
		dtw := &DiscoverTaskWorker{rs: rs, dts: dts}

		result, err := dtw.discoverPagedFilesetResources(context.Background(), task, catalog, pages, &discoverTaskReconcileProgress{})

		require.NoError(t, err)
		assert.Equal(t, total/defaultFilesetPageSize, pages.calls)
		assert.Equal(t, defaultFilesetPageSize, pages.maxPerPage)
		assert.LessOrEqual(t, rs.maxIDsPerLookup, defaultFilesetPageSize, "existing resources are fetched one page at a time")
		assert.Equal(t, total/2, rs.created)
		assert.Equal(t, total/2, result.NewCount)
		assert.Equal(t, 50, result.StaleCount)
		assert.Len(t, rs.staled, 50)
		for _, id := range rs.staled {
			assert.Contains(t, id, "gone-")
		}
	})
}

func TestListFilesetPage(t *testing.T) {
	transient := errors.New("connection reset")

	t.Run("retries transient errors with exponential backoff", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		var waits []time.Duration
		dtw := &DiscoverTaskWorker{filesetRetryWait: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
		want := &interfaces.FilesetPage{Items: []*interfaces.FilesetMeta{{ID: "a"}}}
		gomock.InOrder(
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", 20).Return(nil, transient),
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", 20).Return(nil, transient),
			lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", 20).Return(want, nil),
		)

		got, err := dtw.listFilesetPage(context.Background(), lister, 3, "c1", 20)

		require.NoError(t, err)
		assert.Same(t, want, got)
		assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, waits)
	})

	t.Run("gives up after the last attempt and names the page and cursor", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{filesetRetryWait: func(context.Context, time.Duration) error { return nil }}
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "c1", 20).Return(nil, transient).Times(filesetPageMaxAttempts)

		got, err := dtw.listFilesetPage(context.Background(), lister, 3, "c1", 20)

		require.ErrorIs(t, err, transient)
		assert.Contains(t, err.Error(), fmt.Sprintf(`page 3 (cursor "c1") failed after %d attempt(s)`, filesetPageMaxAttempts))
		assert.Nil(t, got)
	})

	t.Run("does not retry a non-retryable error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{filesetRetryWait: func(context.Context, time.Duration) error {
			t.Fatal("must not wait before giving up")
			return nil
		}}
		invalid := interfaces.NewSourceQueryInvalidParameterError("bad_prefix", errors.New("bad prefix"))
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 20).Return(nil, invalid).Times(1)

		got, err := dtw.listFilesetPage(context.Background(), lister, 1, "", 20)

		var target *interfaces.SourceQueryInvalidParameterError
		require.ErrorAs(t, err, &target)
		assert.Nil(t, got)
	})

	t.Run("stops waiting when the context is cancelled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		ctx, cancel := context.WithCancel(context.Background())
		dtw := &DiscoverTaskWorker{}
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 20).DoAndReturn(func(context.Context, string, int) (*interfaces.FilesetPage, error) {
			cancel()
			return nil, transient
		}).Times(1)

		got, err := dtw.listFilesetPage(ctx, lister, 1, "", 20)

		require.ErrorIs(t, err, transient)
		assert.Nil(t, got)
	})

	t.Run("interrupted backoff returns the context error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dtw := &DiscoverTaskWorker{filesetRetryWait: func(context.Context, time.Duration) error { return context.Canceled }}
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 20).Return(nil, transient).Times(1)

		got, err := dtw.listFilesetPage(ctx, lister, 1, "", 20)

		require.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "retry interrupted")
		assert.Nil(t, got)
	})

	t.Run("rejects a nil page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lister := vmock.NewMockPagedFilesetLister(ctrl)
		dtw := &DiscoverTaskWorker{}
		lister.EXPECT().ListFilesetsPage(gomock.Any(), "", 20).Return(nil, nil)

		got, err := dtw.listFilesetPage(context.Background(), lister, 1, "", 20)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "connector returned no page")
		assert.Nil(t, got)
	})
}

func TestIsRetryableFilesetListError(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"transient error", context.Background(), errors.New("timeout"), true},
		{"request deadline while task context is alive", context.Background(), context.DeadlineExceeded, true},
		{"task context cancelled", cancelled, errors.New("timeout"), false},
		{"cancellation error", context.Background(), fmt.Errorf("list: %w", context.Canceled), false},
		{"read forbidden", context.Background(), interfaces.NewSourceReadForbiddenError(errors.New("denied")), false},
		{"invalid parameter", context.Background(), interfaces.NewSourceQueryInvalidParameterError("bad", errors.New("bad")), false},
		{"unsupported operation", context.Background(), interfaces.NewUnsupportedOperationError("list", errors.New("no")), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isRetryableFilesetListError(tc.ctx, tc.err))
		})
	}
}

func TestResolveFilesetDiscoverSettings(t *testing.T) {
	cases := []struct {
		name           string
		cfg            common.DiscoverConfig
		wantPageSize   int
		wantMaxObjects int
	}{
		{"unset uses defaults", common.DiscoverConfig{}, defaultFilesetPageSize, defaultFilesetMaxObjects},
		{"valid values are kept", common.DiscoverConfig{FilesetPageSize: 200, FilesetMaxObjects: 5000}, 200, 5000},
		{"page size bounds are inclusive", common.DiscoverConfig{FilesetPageSize: maxFilesetPageSize, FilesetMaxObjects: 1}, maxFilesetPageSize, 1},
		{"page size above the bound falls back", common.DiscoverConfig{FilesetPageSize: maxFilesetPageSize + 1}, defaultFilesetPageSize, defaultFilesetMaxObjects},
		{"negative values fall back", common.DiscoverConfig{FilesetPageSize: -1, FilesetMaxObjects: -5}, defaultFilesetPageSize, defaultFilesetMaxObjects},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pageSize, maxObjects := resolveFilesetDiscoverSettings(tc.cfg)
			assert.Equal(t, tc.wantPageSize, pageSize)
			assert.Equal(t, tc.wantMaxObjects, maxObjects)
		})
	}
}
