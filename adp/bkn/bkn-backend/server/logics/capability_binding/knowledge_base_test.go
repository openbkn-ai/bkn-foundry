// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability_binding

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func attachKnowledgeBase(service *capabilityBindingService, kbID, ownerID string) ([]*interfaces.CapabilityBinding, error) {
	return service.AttachCapabilities(context.Background(), nil, "kn1", "main",
		[]*interfaces.AttachCapabilityEntry{
			{CapabilityType: interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, OwnerID: ownerID, CapabilityID: kbID},
		})
}

// TestAttachKnowledgeBase covers #2576: a knowledge base is mounted like a skill, by id with no
// owner, and only once kb-api confirms it exists.
func TestAttachKnowledgeBase(t *testing.T) {
	Convey("挂载知识库", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		service, cba, _ := newTestServiceWithFactory(t, ctrl)
		kba := bmock.NewMockKnowledgeBaseAccess(ctrl)
		service.kba = kba

		Convey("kb-api 认得的知识库写一行，owner_id 被丢弃", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(&interfaces.KnowledgeBaseBrief{ID: "7", Name: "合同库"}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, "", "7").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			bindings, err := attachKnowledgeBase(service, "7", "stray-owner")

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 1)
			So(bindings[0].CapabilityType, ShouldEqual, interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE)
			So(bindings[0].OwnerID, ShouldEqual, "")
			So(bindings[0].CapabilityID, ShouldEqual, "7")
		})

		Convey("不存在的知识库返回 TargetNotFound，不写入", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "9").Return(nil, nil)

			_, err := attachKnowledgeBase(service, "9", "")

			So(httpCodeOf(t, err), ShouldEqual, http.StatusBadRequest)
			So(errorCodeOf(t, err), ShouldContainSubstring, "TargetNotFound")
		})

		Convey("没有部署 kb-api 时明确拒绝", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, interfaces.ErrKnowledgeBaseUnavailable)

			_, err := attachKnowledgeBase(service, "7", "")

			So(httpCodeOf(t, err), ShouldEqual, http.StatusBadRequest)
			So(errorCodeOf(t, err), ShouldContainSubstring, "KnowledgeBaseNotSupported")
		})

		Convey("kb-api 不可达返回 502，不写入", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, errors.New("connection refused"))

			_, err := attachKnowledgeBase(service, "7", "")

			So(httpCodeOf(t, err), ShouldEqual, http.StatusBadGateway)
			So(errorCodeOf(t, err), ShouldContainSubstring, "KnowledgeBaseServiceUnavailable")
		})
	})

	Convey("没有注入 kb-api 访问时同样拒绝", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		service, _, _ := newTestServiceWithFactory(t, ctrl)

		_, err := attachKnowledgeBase(service, "7", "")

		So(errorCodeOf(t, err), ShouldContainSubstring, "KnowledgeBaseNotSupported")
	})
}

// TestBackfillKnowledgeBases names mounted knowledge bases from kb-api and marks the ones it no
// longer knows; kb-api being unreachable degrades instead of failing the list.
func TestBackfillKnowledgeBases(t *testing.T) {
	Convey("知识库绑定回填名称", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		service, _, _ := newTestServiceWithFactory(t, ctrl)
		kba := bmock.NewMockKnowledgeBaseAccess(ctrl)
		service.kba = kba
		live := &interfaces.CapabilityBinding{CapabilityType: interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, CapabilityID: "7"}
		gone := &interfaces.CapabilityBinding{CapabilityType: interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, CapabilityID: "8"}

		Convey("找得到的填名称，找不到的标 missing", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(&interfaces.KnowledgeBaseBrief{ID: "7", Name: "合同库"}, nil)
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "8").Return(nil, nil)

			result := service.backfillMetadata(context.Background(), interfaces.CapabilityBindingsQueryParams{},
				[]*interfaces.CapabilityBinding{live, gone}, false)

			So(result.available, ShouldBeTrue)
			So(live.Name, ShouldEqual, "合同库")
			So(gone.Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
		})

		Convey("没有部署 kb-api 时标 missing，不算元数据不可用", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, interfaces.ErrKnowledgeBaseUnavailable)

			result := service.backfillMetadata(context.Background(), interfaces.CapabilityBindingsQueryParams{},
				[]*interfaces.CapabilityBinding{live}, false)

			So(result.available, ShouldBeTrue)
			So(live.Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
		})

		Convey("kb-api 不可达时保留原行并标记不完整", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, errors.New("connection refused"))

			result := service.backfillMetadata(context.Background(), interfaces.CapabilityBindingsQueryParams{},
				[]*interfaces.CapabilityBinding{live}, false)

			So(result.available, ShouldBeFalse)
			So(live.Name, ShouldEqual, "")
			So(live.Status, ShouldEqual, "")
		})
	})

	Convey("没有注入 kb-api 访问时同样标 missing", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		service, _, _ := newTestServiceWithFactory(t, ctrl)
		binding := &interfaces.CapabilityBinding{CapabilityType: interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, CapabilityID: "7"}

		result := service.backfillMetadata(context.Background(), interfaces.CapabilityBindingsQueryParams{},
			[]*interfaces.CapabilityBinding{binding}, false)

		So(result.available, ShouldBeTrue)
		So(binding.Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
	})
}

func kbDecl(id, name string) *bknsdk.BknCapabilities {
	return &bknsdk.BknCapabilities{KnowledgeBases: []*bknsdk.BknCapabilityKnowledgeBase{{ID: id, Name: name}}}
}

// TestImportKnowledgeBase resolves a declared knowledge base by id, then by exact name, like a
// skill, and reports instead of failing when it cannot.
func TestImportKnowledgeBase(t *testing.T) {
	Convey("导入知识库依赖", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		service, cba, _ := newTestServiceWithFactory(t, ctrl)
		kba := bmock.NewMockKnowledgeBaseAccess(ctrl)
		service.kba = kba

		Convey("id 落空后按名字找回并挂载", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, nil)
			kba.EXPECT().FindKnowledgeBasesByName(gomock.Any(), "合同库").
				Return([]*interfaces.KnowledgeBaseBrief{{ID: "21", Name: "合同库"}}, nil)
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "21").Return(&interfaces.KnowledgeBaseBrief{ID: "21", Name: "合同库"}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, "", "21").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", "合同库"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
			So(report.Skipped, ShouldBeEmpty)
		})

		Convey("id 命中但名字不同：不采用该 id，按名字找", func() {
			// Another environment's 7 is a different knowledge base; mounting it would expose
			// the wrong data through the proxy.
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(&interfaces.KnowledgeBaseBrief{ID: "7", Name: "员工档案"}, nil)
			kba.EXPECT().FindKnowledgeBasesByName(gomock.Any(), "合同库").
				Return([]*interfaces.KnowledgeBaseBrief{{ID: "12", Name: "合同库"}}, nil)
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "12").Return(&interfaces.KnowledgeBaseBrief{ID: "12", Name: "合同库"}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, "", "12").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", "合同库"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
		})

		Convey("id 命中但名字不同且名字找不到：跳过，不挂 id 指向的库", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(&interfaces.KnowledgeBaseBrief{ID: "7", Name: "员工档案"}, nil)
			kba.EXPECT().FindKnowledgeBasesByName(gomock.Any(), "合同库").Return(nil, nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", "合同库"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 0)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipNotFound)
		})

		Convey("只写了 id 时按 id 采用", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(&interfaces.KnowledgeBaseBrief{ID: "7", Name: "员工档案"}, nil).Times(2)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE, "", "7").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", ""))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
		})

		Convey("同名多个不猜，跳过并说明", func() {
			kba.EXPECT().FindKnowledgeBasesByName(gomock.Any(), "合同库").
				Return([]*interfaces.KnowledgeBaseBrief{{ID: "21"}, {ID: "22"}}, nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("", "合同库"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 0)
			So(len(report.Skipped), ShouldEqual, 1)
			So(report.Skipped[0].CapabilityType, ShouldEqual, interfaces.CAPABILITY_TYPE_KNOWLEDGE_BASE)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipAmbiguous)
		})

		Convey("没有部署 kb-api 时跳过为 not_supported，导入照常", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, interfaces.ErrKnowledgeBaseUnavailable)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", "合同库"))

			So(err, ShouldBeNil)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipNotSupported)
		})

		Convey("kb-api 不可达时跳过并说明", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, errors.New("connection refused"))

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main", kbDecl("7", "合同库"))

			So(err, ShouldBeNil)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipKnowledgeBaseUnreachable)
		})
	})
}
