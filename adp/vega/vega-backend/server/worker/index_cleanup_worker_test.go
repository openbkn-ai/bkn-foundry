// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestIndexCleanupWorkerRunOnce(t *testing.T) {
	oldIndex := &interfaces.IndexMeta{Name: "vega-build-r1-t1", CreationTime: time.Now().Add(-2 * time.Hour).UnixMilli()}

	t.Run("dry run identifies completed superseded index without deleting", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		lim.EXPECT().ListIndexes(gomock.Any()).Return([]*interfaces.IndexMeta{oldIndex}, nil)
		rs.EXPECT().InternalList(gomock.Any(), interfaces.ResourcesQueryParams{}).Return([]*interfaces.ResourceSummary{{ID: "r1", LocalIndexName: "vega-build-r1-t2"}}, nil)
		bts.EXPECT().InternalGetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)

		newIndexCleanupWorkerForTest(lim, rs, bts, true).runOnce(context.Background())
	})

	t.Run("rechecks before deleting and protects a newly referenced index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		lim.EXPECT().ListIndexes(gomock.Any()).Return([]*interfaces.IndexMeta{oldIndex}, nil)
		rs.EXPECT().InternalList(gomock.Any(), interfaces.ResourcesQueryParams{}).Return([]*interfaces.ResourceSummary{{ID: "r1"}}, nil)
		bts.EXPECT().InternalGetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
		rs.EXPECT().InternalList(gomock.Any(), interfaces.ResourcesQueryParams{}).Return([]*interfaces.ResourceSummary{{ID: "r1", LocalIndexName: oldIndex.Name}}, nil)

		newIndexCleanupWorkerForTest(lim, rs, bts, false).runOnce(context.Background())
	})

	t.Run("deletes cancelled index after successful recheck", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bts := vmock.NewMockBuildTaskService(ctrl)
		lim.EXPECT().ListIndexes(gomock.Any()).Return([]*interfaces.IndexMeta{oldIndex}, nil)
		rs.EXPECT().InternalList(gomock.Any(), interfaces.ResourcesQueryParams{}).Return([]*interfaces.ResourceSummary{{ID: "r1"}}, nil).Times(2)
		bts.EXPECT().InternalGetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCancelled}, nil).Times(2)
		lim.EXPECT().DeleteIndex(gomock.Any(), oldIndex.Name).Return(nil)

		newIndexCleanupWorkerForTest(lim, rs, bts, false).runOnce(context.Background())
	})
}

func newIndexCleanupWorkerForTest(lim interfaces.LocalIndexManager, rs interfaces.ResourceService, bts interfaces.BuildTaskService, dryRun bool) *IndexCleanupWorker {
	return &IndexCleanupWorker{
		appSetting: &common.AppSetting{IndexCleanup: common.IndexCleanupConfig{
			DryRun: dryRun, ProtectionPeriod: time.Hour, MaxDeletesPerRun: 10,
		}},
		lim: lim, rs: rs, bts: bts,
		interval:         time.Hour,
		protectionPeriod: time.Hour,
		maxDeletesPerRun: 10,
		stopCh:           make(chan struct{}),
	}
}
