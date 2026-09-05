// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
)

func httpCodeOf(t *testing.T, err error) int {
	t.Helper()
	httpErr, ok := err.(*rest.HTTPError)
	So(ok, ShouldBeTrue)
	return httpErr.HTTPCode
}

func errorCodeOf(t *testing.T, err error) string {
	t.Helper()
	httpErr, ok := err.(*rest.HTTPError)
	So(ok, ShouldBeTrue)
	return httpErr.BaseError.ErrorCode
}

// TestAttachValidatesTargets covers #1258: a binding names something in the execution factory, and
// a mount that cannot be called is worth refusing at write time rather than discovering at recall.
func TestAttachValidatesTargets(t *testing.T) {
	Convey("挂载前校验目标", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("技能不存在与技能未发布是两种可区分的错误", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "missing").Return(nil, nil)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "draft").
				Return(&interfaces.SkillBrief{SkillID: "draft", Status: "unpublish"}, nil)

			_, notFound := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "missing"},
				})
			_, notPublished := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "draft"},
				})

			So(httpCodeOf(t, notFound), ShouldEqual, http.StatusBadRequest)
			So(httpCodeOf(t, notPublished), ShouldEqual, http.StatusBadRequest)
			// The two answers must differ: one says check the id, the other says publish it.
			So(errorCodeOf(t, notFound), ShouldNotEqual, errorCodeOf(t, notPublished))
		})

		Convey("工具不存在、工具被禁用、工具箱未发布各自被拒", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "on", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "off", Status: "disabled"},
			}, nil).AnyTimes()
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-draft").Return([]*interfaces.ToolBrief{
				{BoxID: "box-draft", BoxStatus: "unpublish", ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			}, nil).AnyTimes()

			attach := func(boxID, toolID string) error {
				_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
					[]*interfaces.AttachCapabilityEntry{
						{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: boxID, CapabilityID: toolID},
					})
				return err
			}

			So(errorCodeOf(t, attach("box-1", "nope")), ShouldContainSubstring, "TargetNotFound")
			So(errorCodeOf(t, attach("box-1", "off")), ShouldContainSubstring, "TargetNotAvailable")
			So(errorCodeOf(t, attach("box-draft", "t1")), ShouldContainSubstring, "TargetNotAvailable")
		})

		Convey("工具箱不存在被拒", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "gone").Return(nil, nil)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "gone", CapabilityID: "t1"},
				})

			So(errorCodeOf(t, err), ShouldContainSubstring, "TargetNotFound")
		})

		Convey("执行工厂不可达返回 502 且不写入", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return(nil, errors.New("connection refused"))
			// No CreateBindings expectation: the controller fails the test if anything is written.
			_ = cba

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t1"},
				})

			So(httpCodeOf(t, err), ShouldEqual, http.StatusBadGateway)
		})

		Convey("挂载不额外要求对目标的执行权限", func() {
			// Binding is a modeling action by the knowledge network's owner. Whether the caller
			// may *run* the tool stays the execution factory's decision at call time, so the only
			// permission consulted here is modify on the knowledge network. This is locked so a
			// later reading of "the caller mounted something it cannot run" is not mistaken for
			// an authorization hole and fixed by adding a check that breaks modeling.
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "t1").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t1"},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 1)
		})
	})
}

// TestAttachExpandsWholeBox covers the write-time expansion: the stored rows are tool-level, so a
// tool added to the box afterwards is not inherited and each row can be released on its own.
func TestAttachExpandsWholeBox(t *testing.T) {
	Convey("整箱挂载在写入期展开", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("展开为逐工具绑定并置 bound_as_box", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t2", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
				// A disabled tool is skipped: naming it directly is refused, and expanding it
				// here would create through the back door a binding the front door rejects.
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t3", Status: "disabled"},
			}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(2)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", AllTools: true},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 2)
			for _, binding := range bindings {
				So(binding.BoundAsBox, ShouldBeTrue)
				So(binding.OwnerID, ShouldEqual, "box-1")
			}
		})

		Convey("箱内没有可挂载的工具时报 400,不静默成功", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "empty").Return([]*interfaces.ToolBrief{}, nil)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "empty", AllTools: true},
				})

			So(httpCodeOf(t, err), ShouldEqual, http.StatusBadRequest)
			So(errorCodeOf(t, err), ShouldContainSubstring, "EmptyToolBox")
		})

		Convey("整箱与逐个混在一起时同一工具箱只查一次", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
				{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			}, nil).Times(1)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t1"},
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", AllTools: true},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 2)
		})
	})
}
