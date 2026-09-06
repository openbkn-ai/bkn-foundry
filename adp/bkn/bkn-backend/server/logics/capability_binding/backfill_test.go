// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package capability_binding

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
)

func listQuery() interfaces.CapabilityBindingsQueryParams {
	return interfaces.CapabilityBindingsQueryParams{KNID: "kn1", Branch: "main"}
}

// TestBackfillFanOut covers the cost rule of #1263: function metadata is read per tool box, not
// per tool, so a page stays cheap as the number of bound tools grows.
func TestBackfillFanOut(t *testing.T) {
	Convey("function 回填按箱不按工具", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		bindings := []*interfaces.CapabilityBinding{}
		for _, toolID := range []string{"t1", "t2", "t3"} {
			bindings = append(bindings, &interfaces.CapabilityBinding{
				ID: "b-" + toolID, CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
				OwnerID: "box-1", CapabilityID: toolID,
			})
		}
		bindings = append(bindings, &interfaces.CapabilityBinding{
			ID: "b-t9", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			OwnerID: "box-2", CapabilityID: "t9",
		})
		cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return(bindings, nil)
		cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(4, nil)

		// Four bindings, two boxes: exactly two calls.
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
			{BoxID: "box-1", BoxName: "库存工具箱", ToolID: "t1", Name: "查库存", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			{BoxID: "box-1", BoxName: "库存工具箱", ToolID: "t2", Name: "改库存", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			{BoxID: "box-1", BoxName: "库存工具箱", ToolID: "t3", Name: "删库存", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
		}, nil).Times(1)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-2").Return([]*interfaces.ToolBrief{
			{BoxID: "box-2", BoxName: "另一个箱", ToolID: "t9", Name: "别的工具", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
		}, nil).Times(1)

		list, err := service.ListCapabilities(context.Background(), listQuery())

		So(err, ShouldBeNil)
		So(list.MetadataAvailable, ShouldBeTrue)
		So(list.Entries[0].Name, ShouldEqual, "查库存")
		So(list.Entries[0].OwnerName, ShouldEqual, "库存工具箱")
	})
}

// TestBackfillMarksDangling covers the rule that a vanished target is reported, not deleted.
// Retrieval already skips such a reference silently, so the list is the only place the gap shows.
func TestBackfillMarksDangling(t *testing.T) {
	Convey("悬空绑定被标记而不是被删除", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("工具在执行工厂侧被删除", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{
				{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "alive"},
				{ID: "b2", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "deleted"},
			}, nil)
			cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(2, nil)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
				{BoxID: "box-1", ToolID: "alive", Name: "还在", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			}, nil)
			// No delete expectation: the controller fails the test if the row is removed.

			list, err := service.ListCapabilities(context.Background(), listQuery())

			So(err, ShouldBeNil)
			So(len(list.Entries), ShouldEqual, 2)
			So(list.Entries[0].Status, ShouldEqual, interfaces.EXEC_TOOL_STATUS_ENABLED)
			So(list.Entries[1].Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
		})

		Convey("整个工具箱不存在时该箱下全部绑定判为悬空", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{
				{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "gone", CapabilityID: "t1"},
				{ID: "b2", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "gone", CapabilityID: "t2"},
			}, nil)
			cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(2, nil)
			aoa.EXPECT().ListBoxTools(gomock.Any(), "gone").Return(nil, nil)

			list, err := service.ListCapabilities(context.Background(), listQuery())

			So(err, ShouldBeNil)
			for _, entry := range list.Entries {
				So(entry.Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
			}
			So(list.Boxes[0].BoxMissing, ShouldBeTrue)
		})

		Convey("技能被删除:names 不返回该 id 即为悬空", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{
				{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "alive"},
				{ID: "b2", CapabilityType: interfaces.CAPABILITY_TYPE_SKILL, CapabilityID: "deleted"},
			}, nil)
			cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(2, nil)
			aoa.EXPECT().GetSkillNamesByIDs(gomock.Any(), gomock.Any()).
				Return(map[string]string{"alive": "还在的技能"}, nil)

			list, err := service.ListCapabilities(context.Background(), listQuery())

			So(err, ShouldBeNil)
			byID := map[string]*interfaces.CapabilityBinding{}
			for _, entry := range list.Entries {
				byID[entry.ID] = entry
			}
			So(byID["b1"].Name, ShouldEqual, "还在的技能")
			So(byID["b2"].Status, ShouldEqual, interfaces.CAPABILITY_STATUS_MISSING)
		})
	})
}

// TestBackfillBoxTopUp covers the count the list page needs to offer a one-click top-up: a
// whole-box mount is expanded at write time and does not follow the box, so a tool added later is
// invisible without this.
func TestBackfillBoxTopUp(t *testing.T) {
	Convey("箱内新增工具后能给出未挂载数量", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{
			{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t1", BoundAsBox: true},
			{ID: "b2", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t2", BoundAsBox: true},
		}, nil)
		cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(2, nil)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
			{BoxID: "box-1", ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			{BoxID: "box-1", ToolID: "t2", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			{BoxID: "box-1", ToolID: "t3-new", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			// A disabled tool is not something a top-up would add, so it is not counted.
			{BoxID: "box-1", ToolID: "t4-off", Status: "disabled"},
		}, nil)

		list, err := service.ListCapabilities(context.Background(), listQuery())

		So(err, ShouldBeNil)
		So(len(list.Boxes), ShouldEqual, 1)
		So(list.Boxes[0].TotalTools, ShouldEqual, 3)
		So(list.Boxes[0].MountedTools, ShouldEqual, 2)
		So(list.Boxes[0].UnmountedTools, ShouldEqual, 1)
	})
}

// TestBackfillDegrades covers the outage rule: listing memberships is the job, names are
// decoration, and an unreachable factory must not take the mount UI down with it.
func TestBackfillDegrades(t *testing.T) {
	Convey("执行工厂不可达时仍返回绑定并标注元数据缺失", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		cba.EXPECT().ListBindings(gomock.Any(), gomock.Any()).Return([]*interfaces.CapabilityBinding{
			{ID: "b1", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", CapabilityID: "t1"},
		}, nil)
		cba.EXPECT().GetBindingsTotal(gomock.Any(), gomock.Any()).Return(1, nil)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return(nil, errors.New("connection refused"))

		list, err := service.ListCapabilities(context.Background(), listQuery())

		So(err, ShouldBeNil)
		So(len(list.Entries), ShouldEqual, 1)
		So(list.Entries[0].Name, ShouldBeEmpty)
		// An outage must not read as a deleted capability.
		So(list.Entries[0].Status, ShouldNotEqual, interfaces.CAPABILITY_STATUS_MISSING)
		So(list.MetadataAvailable, ShouldBeFalse)
	})
}

// TestWholeBoxTopUpIsAnotherMount pins the flow behind the list page's top-up button: re-issuing
// the same whole-box mount adds only what is missing. Without this, "3 tools not mounted" would
// need an endpoint of its own, and the frontend would have to diff the box against the bindings
// itself.
func TestWholeBoxTopUpIsAnotherMount(t *testing.T) {
	Convey("重发整箱挂载即补挂,已绑定的不重复写入", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		existing := &interfaces.CapabilityBinding{
			ID: "b1", KNID: "kn1", Branch: "main", CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION,
			OwnerID: "box-1", CapabilityID: "t1", BoundAsBox: true,
		}
		// The box now holds a tool that was added after the first mount.
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-1").Return([]*interfaces.ToolBrief{
			{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t1", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
			{BoxID: "box-1", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, ToolID: "t2-new", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
		}, nil)
		cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
			interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "t1").Return(existing, nil)
		cba.EXPECT().GetBindingByCapability(gomock.Any(), "kn1", "main",
			interfaces.CAPABILITY_TYPE_FUNCTION, "box-1", "t2-new").Return(nil, nil)
		// Only the new tool is written.
		cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

		bindings, err := service.AttachCapabilities(context.Background(), nil, "kn1", "main",
			[]*interfaces.AttachCapabilityEntry{
				{CapabilityType: interfaces.CAPABILITY_TYPE_FUNCTION, OwnerID: "box-1", AllTools: true},
			})

		So(err, ShouldBeNil)
		So(len(bindings), ShouldEqual, 2)
		So(bindings[0].ID, ShouldEqual, "b1")
		So(bindings[1].CapabilityID, ShouldEqual, "t2-new")
		So(bindings[1].BoundAsBox, ShouldBeTrue)
	})
}
