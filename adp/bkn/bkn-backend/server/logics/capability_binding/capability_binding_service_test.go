// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func newTestService(t *testing.T, ctrl *gomock.Controller) (*capabilityBindingService, *bmock.MockCapabilityBindingAccess) {
	t.Helper()
	cba := bmock.NewMockCapabilityBindingAccess(ctrl)
	ps := bmock.NewMockPermissionService(ctrl)
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return &capabilityBindingService{appSetting: &common.AppSetting{}, cba: cba, ps: ps}, cba
}

func TestAttachCapabilities(t *testing.T) {
	Convey("挂载能力", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("skill 与 function 各写一行", func() {
			service, cba := newTestService(t, ctrl)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_SKILL, "", "skill-1").Return(nil, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(2)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1"},
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "tool-1"},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 2)
			So(bindings[0].ID, ShouldNotBeEmpty)
			So(bindings[0].OwnerID, ShouldBeEmpty)
			So(bindings[1].OwnerID, ShouldEqual, "box-1")
		})

		Convey("重复挂载返回既有绑定且不再写入", func() {
			service, cba := newTestService(t, ctrl)
			existing := &interfaces.CapabilityBinding{
				ID: "bind-1", KNID: "kn1", Branch: "main",
				CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1",
			}
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_SKILL, "", "skill-1").Return(existing, nil)
			// No CreateBindings expectation: the controller fails the test if a write happens.

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1"},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 1)
			So(bindings[0].ID, ShouldEqual, "bind-1")
		})

		Convey("同一请求里重复的能力只查库一次、只写一行", func() {
			service, cba := newTestService(t, ctrl)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "tool-1").Return(nil, nil).Times(1)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "tool-1"},
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "tool-1"},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 2)
			So(bindings[0].ID, ShouldEqual, bindings[1].ID)
		})

		Convey("skill 带上 owner_id 时被归一化丢弃，避免同一技能落两行", func() {
			service, cba := newTestService(t, ctrl)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
				interfaces.CAPABILITY_TYPE_SKILL, "", "skill-1").Return(nil, nil).Times(1)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, OwnerID: "box-1", CapabilityID: "skill-1"},
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "skill-1"},
				})

			So(err, ShouldBeNil)
			So(len(bindings), ShouldEqual, 2)
			So(bindings[0].OwnerID, ShouldBeEmpty)
		})

		Convey("function 缺 owner_id 报 400", func() {
			service, _ := newTestService(t, ctrl)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, CapabilityID: "tool-1"},
				})

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("未知能力类型报 400，不落库", func() {
			service, _ := newTestService(t, ctrl)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: "mcp_server", CapabilityID: "mcp-1"},
				})

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("空能力标识报 400", func() {
			service, _ := newTestService(t, ctrl)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
				[]*interfaces.AttachCapabilityEntry{
					{CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "   "},
				})

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("空请求体报 400", func() {
			service, _ := newTestService(t, ctrl)

			_, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main", nil)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func TestListCapabilities(t *testing.T) {
	Convey("列举能力绑定", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("按类型过滤并返回总数", func() {
			service, cba := newTestService(t, ctrl)
			query := interfaces.CapabilityBindingsQueryParams{
				KNID: "kn1", Branch: "dev", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL,
			}
			cba.EXPECT().ListBindings(gomock.Any(), query).
				Return([]*interfaces.CapabilityBinding{{ID: "bind-1"}}, nil)
			cba.EXPECT().GetBindingsTotal(gomock.Any(), query).Return(1, nil)

			list, err := service.ListCapabilities(context.Background(), query)

			So(err, ShouldBeNil)
			So(list.TotalCount, ShouldEqual, 1)
			So(len(list.Entries), ShouldEqual, 1)
		})

		Convey("未知 type 过滤值报 400，不查库", func() {
			service, _ := newTestService(t, ctrl)

			_, err := service.ListCapabilities(context.Background(), interfaces.CapabilityBindingsQueryParams{
				KNID: "kn1", Branch: "main", CapabilityType: "operator",
			})

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})
	})
}

func TestDetachCapabilities(t *testing.T) {
	Convey("解除挂载按绑定 id 批量生效", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba := newTestService(t, ctrl)
		cba.EXPECT().DeleteBindingsByIDs(gomock.Any(), gomock.Nil(), "kn1", "main",
			[]string{"bind-1", "bind-2"}).Return(int64(2), nil)

		rows, err := service.DetachCapabilities(context.Background(), nil, "kn1", "main",
			[]string{"bind-1", "bind-2"})

		So(err, ShouldBeNil)
		So(rows, ShouldEqual, int64(2))
	})
}
