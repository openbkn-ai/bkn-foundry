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

// tool is an enabled tool: the ordinary case. Tests about admission set Status themselves.
func tool(boxID, toolID, name, description string) *model.ToolDB {
	return &model.ToolDB{BoxID: boxID, ToolID: toolID, Name: name, Description: description,
		Status: string(interfaces.ToolStatusTypeEnabled)}
}

// publishedBoxes answers every box as published, with no kind. It is the box repository for
// tests that are not about admission, so the rule (#1443) does not turn every existing test into
// a test of an unpublished box.
type publishedBoxes struct{ model.IToolboxDB }

func (publishedBoxes) SelectListByBoxIDs(_ context.Context, boxIDs []string, _ ...string) ([]*model.ToolboxDB, error) {
	out := make([]*model.ToolboxDB, 0, len(boxIDs))
	for _, id := range boxIDs {
		out = append(out, &model.ToolboxDB{BoxID: id, Status: string(interfaces.BizStatusPublished)})
	}
	return out, nil
}

func indexed(boxID, toolID, name, description string) interfaces.IndexedCapability {
	return interfaces.IndexedCapability{
		CapabilityRef: toolRef(boxID, toolID),
		Name:          name,
		Description:   description,
	}
}

func newReconciler(toolRepo model.IToolDB, index interfaces.CapabilityIndexSyncService) *reconciler {
	return &reconciler{logger: logger.DefaultLogger(), indexSync: index, toolRepo: toolRepo, boxRepo: publishedBoxes{}}
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

// TestToolCarriesItsBoxKind covers the field that lets retrieval express the product's four kinds.
//
// A tool row does not say what kind of box it lives in, and the split the UI shows — API tools
// versus functions — is exactly that. Without it on the document, retrieval can only offer three
// kinds where every list in the product offers four.
func TestToolCarriesItsBoxKind(t *testing.T) {
	Convey("函数工具带上所属工具箱的类型", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).
			Return([]string{"box-api", "box-fn"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-api").
			Return([]*model.ToolDB{tool("box-api", "t-api", "汇率换算", "描述")}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-fn").
			Return([]*model.ToolDB{tool("box-fn", "t-fn", "库存计算", "描述")}, nil)

		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), []string{"box-api", "box-fn"}).
			Return([]*model.ToolboxDB{
				{BoxID: "box-api", MetadataType: "openapi", Status: "published"},
				{BoxID: "box-fn", MetadataType: "function", Status: "published"},
			}, nil)

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeFunction).
			Return(nil, nil)
		written := map[string]string{}
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				written[doc.CapabilityID] = doc.MetadataType
				return nil
			}).Times(2)

		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.reconcileTools(context.Background()), ShouldBeNil)

		So(written["t-api"], ShouldEqual, "openapi")
		So(written["t-fn"], ShouldEqual, "function")
	})
}

// TestBoxKindChangeIsRewritten keeps a converted tool box from carrying its old label forever. The
// kind is not part of the embedding, so the "name and description are unchanged" shortcut would
// otherwise skip it.
func TestBoxKindChangeIsRewritten(t *testing.T) {
	Convey("工具箱类型变了要重写文档", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).Return([]string{"box-1"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-1").
			Return([]*model.ToolDB{tool("box-1", "t-1", "汇率换算", "描述")}, nil)

		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), gomock.Any()).
			Return([]*model.ToolboxDB{{BoxID: "box-1", MetadataType: "openapi", Status: "published"}}, nil)

		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		// Same name and description, older kind.
		index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeFunction).Return(
			[]interfaces.IndexedCapability{{
				CapabilityRef: toolRef("box-1", "t-1"),
				MetadataType:  "function",
				Name:          "汇率换算",
				Description:   "描述",
			}}, nil)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				So(doc.MetadataType, ShouldEqual, "openapi")
				return nil
			}).Times(1)

		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.reconcileTools(context.Background()), ShouldBeNil)
	})
}

// The admission rule (#1443): the index holds what an agent can call, which is a tool that is
// enabled inside a published box. Every writer applies it, and a tool that stops qualifying is
// removed on that event rather than left for the next full pass to re-assert.

func TestSyncBoxPurgesAnUnpublishedBox(t *testing.T) {
	Convey("工具箱未发布/下线:SyncBox 删掉它已索引的全部文档,不写任何新文档", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-1").
			Return([]*model.ToolDB{tool("box-1", "t-1", "工具一", "描述"), tool("box-1", "t-2", "工具二", "描述")}, nil)
		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), []string{"box-1"}).
			Return([]*model.ToolboxDB{{BoxID: "box-1", MetadataType: "openapi", Status: "offline"}}, nil)
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexedByOwner(gomock.Any(), interfaces.CapabilityTypeFunction, "box-1").
			Return([]interfaces.IndexedCapability{indexed("box-1", "t-1", "工具一", "描述"), indexed("box-1", "t-2", "工具二", "描述")}, nil)
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-1")).Return(nil)
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-2")).Return(nil)
		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.SyncBox(context.Background(), "box-1"), ShouldBeNil)
	})
}

func TestSyncToolsRemovesADisabledToolAndKeepsItsSibling(t *testing.T) {
	Convey("同一箱内:停用的工具被删,启用的照常写入", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		disabled := tool("box-1", "t-off", "停用了", "描述")
		disabled.Status = string(interfaces.ToolStatusTypeDisabled)
		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxByID(gomock.Any(), "box-1", gomock.Any()).
			Return([]*model.ToolDB{tool("box-1", "t-on", "启用中", "描述"), disabled}, nil)
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				So(doc.CapabilityID, ShouldEqual, "t-on")
				return nil
			})
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-1", "t-off")).Return(nil)
		So(newReconciler(toolRepo, index).SyncTools(context.Background(), "box-1", []string{"t-on", "t-off"}), ShouldBeNil)
	})
}

func TestReconcileLeavesUnpublishedBoxesOutOfTheDesiredSet(t *testing.T) {
	Convey("全量对账:未发布箱的工具不在期望集里,索引中已有的会被删", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).Return([]string{"box-live", "box-draft"}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-live").Return([]*model.ToolDB{tool("box-live", "t-live", "在线", "描述")}, nil)
		toolRepo.EXPECT().SelectToolByBoxID(gomock.Any(), "box-draft").Return([]*model.ToolDB{tool("box-draft", "t-draft", "草稿", "描述")}, nil)
		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), []string{"box-live", "box-draft"}).
			Return([]*model.ToolboxDB{
				{BoxID: "box-live", MetadataType: "openapi", Status: "published"},
				{BoxID: "box-draft", MetadataType: "openapi", Status: "unpublish"},
			}, nil)
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().ListIndexed(gomock.Any(), interfaces.CapabilityTypeFunction).Return(
			[]interfaces.IndexedCapability{indexed("box-draft", "t-draft", "草稿", "描述")}, nil)
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				So(doc.OwnerID, ShouldEqual, "box-live")
				return nil
			})
		index.EXPECT().DeleteCapability(gomock.Any(), toolRef("box-draft", "t-draft")).Return(nil)
		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.reconcileTools(context.Background()), ShouldBeNil)
	})
}

func TestAnUnreadableBoxLeavesTheIndexAlone(t *testing.T) {
	Convey("读不到工具箱状态:既不写也不删,把错误交出去(读失败不是「未发布」)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxByID(gomock.Any(), "box-1", gomock.Any()).
			Return([]*model.ToolDB{tool("box-1", "t-1", "工具", "描述")}, nil)
		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("db down"))
		// No UpsertCapability and no DeleteCapability expectation: any call fails the test.
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.SyncTools(context.Background(), "box-1", []string{"t-1"}), ShouldNotBeNil)
	})
}

// TestReconcileDoesNotPurgeWhenBoxesAreUnreadable is the full-pass version of the same rule, and
// the one with the blast radius: read as "every box is unpublished", one failed query would empty
// the whole function index.
func TestReconcileDoesNotPurgeWhenBoxesAreUnreadable(t *testing.T) {
	Convey("全量对账里工具箱读失败:中止这一轮,不 apply(否则整份索引被清空)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		toolRepo := mocks.NewMockIToolDB(ctrl)
		toolRepo.EXPECT().SelectToolBoxIDsByFilter(gomock.Any(), gomock.Any()).Return([]string{"box-1", "box-2"}, nil)
		boxRepo := mocks.NewMockIToolboxDB(ctrl)
		boxRepo.EXPECT().SelectListByBoxIDs(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("db down"))
		// Neither ListIndexed nor any write/delete may be reached.
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		r := newReconciler(toolRepo, index)
		r.boxRepo = boxRepo
		So(r.reconcileTools(context.Background()), ShouldNotBeNil)
	})
}
