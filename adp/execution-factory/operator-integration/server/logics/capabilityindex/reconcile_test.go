// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capabilityindex

import (
	"context"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

func tool(boxID, toolID, name, description string) *model.ToolDB {
	return &model.ToolDB{BoxID: boxID, ToolID: toolID, Name: name, Description: description}
}

func indexed(boxID, toolID, name, description string) interfaces.IndexedCapability {
	return interfaces.IndexedCapability{
		CapabilityRef: toolRef(boxID, toolID),
		Name:          name,
		Description:   description,
	}
}

func newReconciler(toolRepo model.IToolDB, index interfaces.CapabilityIndexSyncService) *reconciler {
	return &reconciler{logger: logger.DefaultLogger(), indexSync: index, toolRepo: toolRepo}
}

// TestReconcileSkipsUnchangedRows keeps a pass from re-embedding the whole platform. Name and
// description are the entire embedding input, so an unchanged pair is an unchanged vector.
func TestReconcileSkipsUnchangedRows(t *testing.T) {
	Convey("对账只写变化的行", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).Return([]string{"box-1"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-1").Return([]*model.ToolDB{
			tool("box-1", "t-same", "汇率换算", "把金额从一种货币换成另一种"),
			tool("box-1", "t-renamed", "新名字", "描述"),
			tool("box-1", "t-new", "全新工具", "描述"),
		}, nil)

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeFunction).Return(
			[]interfaces.IndexedCapability{
				indexed("box-1", "t-same", "汇率换算", "把金额从一种货币换成另一种"),
				indexed("box-1", "t-renamed", "旧名字", "描述"),
				indexed("box-1", "t-gone", "已删除", "描述"),
			}, nil)

		written := map[string]bool{}
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				written[doc.CapabilityID] = true
				return nil
			}).Times(2)
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-gone")).Return(nil)

		So(newReconciler(toolRepo, index).reconcileTools(context.Background()), ShouldBeNil)
		So(written["t-renamed"], ShouldBeTrue)
		So(written["t-new"], ShouldBeTrue)
		So(written["t-same"], ShouldBeFalse)
	})
}

// TestReconcileDoesNotPurgeOnAPartialRead is the failure this reconciler could most easily cause.
//
// An unreadable box makes the desired set incomplete. Applying an incomplete desired set as if it
// were complete deletes every capability that was missing from it — a read error turning into a
// platform-wide purge.
func TestReconcileDoesNotPurgeOnAPartialRead(t *testing.T) {
	Convey("读不到一个工具箱时不得把索引当空集清空", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).
			Return([]string{"box-1", "box-2"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("database unavailable")).AnyTimes()

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		// Nothing is read back and nothing is deleted: the pass gives up instead.
		index.EXPECT().ListIndexed(gomock.Any(), gomock.Any()).Times(0)
		index.EXPECT().DeleteCapability(gomock.Any(), gomock.Any()).Times(0)

		So(newReconciler(toolRepo, index).reconcileTools(context.Background()), ShouldNotBeNil)
	})
}

// TestReconcileDropsDeletedTools covers a soft-deleted row: present in the table, absent from the
// desired set, gone from the index.
func TestReconcileDropsDeletedTools(t *testing.T) {
	Convey("软删的工具从索引移除", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		deleted := tool("box-1", "t-deleted", "旧工具", "描述")
		deleted.IsDeleted = true

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).Return([]string{"box-1"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-1").Return([]*model.ToolDB{deleted}, nil)

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeFunction).Return(
			[]interfaces.IndexedCapability{indexed("box-1", "t-deleted", "旧工具", "描述")}, nil)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).Times(0)
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-deleted")).Return(nil)

		So(newReconciler(toolRepo, index).reconcileTools(context.Background()), ShouldBeNil)
	})
}

// TestSyncToolsReadsTheTableBack locks the property that makes the immediate write safe to call
// after a transaction that may not have survived: what lands in the index is what the table says,
// not what the caller was about to commit.
func TestSyncToolsReadsTheTableBack(t *testing.T) {
	Convey("即时同步以库里的现状为准", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("行还在就写入", func() {
			toolRepo := mocks.NewMockIToolDB(ctrl)
			toolRepo.EXPECT().SelectToolBoxByID(gomock.Any(), "box-1", []string{"t-1"}).
				Return([]*model.ToolDB{tool("box-1", "t-1", "汇率换算", "描述")}, nil)

			index := mocks.NewMockCapabilityIndexSyncService(ctrl)
			index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, doc *interfaces.CapabilityDocument) error {
					So(doc.Name, ShouldEqual, "汇率换算")
					return nil
				})

			So(newReconciler(toolRepo, index).SyncTools(context.Background(), "box-1", []string{"t-1"}),
				ShouldBeNil)
		})

		Convey("行不在了就删除——回滚后调用也是安全的", func() {
			toolRepo := mocks.NewMockIToolDB(ctrl)
			toolRepo.EXPECT().SelectToolBoxByID(gomock.Any(), "box-1", []string{"t-rolled-back"}).
				Return(nil, nil)

			index := mocks.NewMockCapabilityIndexSyncService(ctrl)
			index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).Times(0)
			index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-rolled-back")).Return(nil)

			So(newReconciler(toolRepo, index).SyncTools(context.Background(), "box-1", []string{"t-rolled-back"}),
				ShouldBeNil)
		})

		Convey("重复 id 只处理一次", func() {
			toolRepo := mocks.NewMockIToolDB(ctrl)
			toolRepo.EXPECT().SelectToolBoxByID(gomock.Any(), "box-1", []string{"t-1"}).
				Return([]*model.ToolDB{tool("box-1", "t-1", "工具", "描述")}, nil)

			index := mocks.NewMockCapabilityIndexSyncService(ctrl)
			index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).Return(nil).Times(1)

			So(newReconciler(toolRepo, index).SyncTools(context.Background(), "box-1",
				[]string{"t-1", " t-1 ", ""}), ShouldBeNil)
		})
	})
}

// TestSyncBoxScopesTheIndexRead keeps a per-box write from scanning every function capability on
// the platform.
func TestSyncBoxScopesTheIndexRead(t *testing.T) {
	Convey("按工具箱同步时只读该工具箱的索引行", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-1").
			Return([]*model.ToolDB{tool("box-1", "t-1", "工具", "描述")}, nil)

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexedByOwner(gomock.Any(), interfaces.CapabilityTypeFunction, "box-1").
			Return([]interfaces.IndexedCapability{indexed("box-1", "t-stale", "旧的", "描述")}, nil)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).Return(nil)
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-stale")).Return(nil)

		So(newReconciler(toolRepo, index).SyncBox(context.Background(), "box-1"), ShouldBeNil)
	})
}
