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

		Convey("kb-api 不可达时保留原行并标记不完整", func() {
			kba.EXPECT().GetKnowledgeBase(gomock.Any(), "7").Return(nil, errors.New("connection refused"))

			result := service.backfillMetadata(context.Background(), interfaces.CapabilityBindingsQueryParams{},
				[]*interfaces.CapabilityBinding{live}, false)

			So(result.available, ShouldBeFalse)
			So(live.Name, ShouldEqual, "")
			So(live.Status, ShouldEqual, "")
		})
	})
}
