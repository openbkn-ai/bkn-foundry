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

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/interfaces"
)

func skillDecl(id, name string) *bknsdk.BknCapabilities {
	return &bknsdk.BknCapabilities{Skills: []*bknsdk.BknCapabilitySkill{{ID: id, Name: name}}}
}

// TestImportResolvesByIDThenName covers the resolution order. Ids are exact but belong to the
// environment that produced the file; names are the only thing that survives the move, and a model
// imported elsewhere would otherwise arrive with everything unbound.
func TestImportResolvesByIDThenName(t *testing.T) {
	Convey("导入解析:先 id 后名字", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("同环境:id 精确命中,不按名解析", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "skill-1").
				Return(&interfaces.SkillBrief{SkillID: "skill-1", Status: interfaces.EXEC_SKILL_STATUS_PUBLISHED}, nil).Times(2)
			// No FindSkillsByName expectation: an id hit must not cost a name lookup.
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				skillDecl("skill-1", "交期评估"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
			So(report.Skipped, ShouldBeEmpty)
		})

		Convey("换环境:id 落空后按名解析", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "skill-from-elsewhere").Return(nil, nil)
			aoa.EXPECT().FindSkillsByName(gomock.Any(), "交期评估").
				Return([]*interfaces.SkillBrief{{SkillID: "local-7", Status: interfaces.EXEC_SKILL_STATUS_PUBLISHED}}, nil)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "local-7").
				Return(&interfaces.SkillBrief{SkillID: "local-7", Status: interfaces.EXEC_SKILL_STATUS_PUBLISHED}, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any(), "local-7").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				skillDecl("skill-from-elsewhere", "交期评估"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
		})
	})
}

// TestImportSkipsRatherThanGuesses covers the cases that must not bind anything, and must not stay
// silent either: a short import that looks complete is the worst outcome here.
func TestImportSkipsRatherThanGuesses(t *testing.T) {
	Convey("无法解析时跳过并报告", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("同名多条不猜", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), gomock.Any()).Return(nil, nil)
			aoa.EXPECT().FindSkillsByName(gomock.Any(), "交期评估").
				Return([]*interfaces.SkillBrief{{SkillID: "a"}, {SkillID: "b"}}, nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				skillDecl("gone", "交期评估"))

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 0)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipAmbiguous)
			So(report.Skipped[0].Name, ShouldEqual, "交期评估")
		})

		Convey("目标环境没有该能力", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), gomock.Any()).Return(nil, nil)
			aoa.EXPECT().FindSkillsByName(gomock.Any(), gomock.Any()).Return(nil, nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				skillDecl("gone", "本环境没有的技能"))

			So(err, ShouldBeNil)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipNotFound)
		})

		Convey("执行工厂不可达时整段跳过,导入不失败", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), gomock.Any()).Return(nil, errors.New("connection refused"))

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				skillDecl("skill-1", "交期评估"))

			So(err, ShouldBeNil)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipUnreachable)
		})

		Convey("一条跳过不影响另一条挂载", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "ok").
				Return(&interfaces.SkillBrief{SkillID: "ok", Status: interfaces.EXEC_SKILL_STATUS_PUBLISHED}, nil).Times(2)
			aoa.EXPECT().GetSkillByID(gomock.Any(), "gone").Return(nil, nil)
			aoa.EXPECT().FindSkillsByName(gomock.Any(), "缺失的").Return(nil, nil)
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), gomock.Any(), "ok").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{Skills: []*bknsdk.BknCapabilitySkill{
					{ID: "ok", Name: "在的"}, {ID: "gone", Name: "缺失的"},
				}})

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
			So(len(report.Skipped), ShouldEqual, 1)
		})
	})
}

// TestImportResolvesFunctionsByBoxAndToolName covers the two-level fallback for tools: the box is
// resolved by name first, then the tool inside it, because a tool id means nothing without its box.
func TestImportResolvesFunctionsByBoxAndToolName(t *testing.T) {
	Convey("函数按箱名 + 工具名兜底", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "box-from-elsewhere").Return(nil, nil)
		aoa.EXPECT().FindToolBoxesByName(gomock.Any(), "供应链计算").
			Return([]*interfaces.ToolBoxBrief{{BoxID: "local-box", Name: "供应链计算"}}, nil)
		aoa.EXPECT().ListBoxTools(gomock.Any(), "local-box").Return([]*interfaces.ToolBrief{
			{BoxID: "local-box", BoxStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED,
				ToolID: "local-tool", Name: "BOM 展开", Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
		}, nil).AnyTimes()
		cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
			gomock.Any(), "local-box", "local-tool").Return(nil, nil)
		cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

		report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
			&bknsdk.BknCapabilities{Functions: []*bknsdk.BknCapabilityFunction{{
				BoxID: "box-from-elsewhere", ToolID: "tool-from-elsewhere",
				BoxName: "供应链计算", ToolName: "BOM 展开",
			}}})

		So(err, ShouldBeNil)
		So(report.Bound, ShouldEqual, 1)
		So(report.Skipped, ShouldBeEmpty)
	})
}

// TestImportSkipsToolInUnpublishedBox pins resolution to the same bar the mount enforces. The
// mount validates the batch as a unit, so a tool whose box is not published would fail the whole
// AttachCapabilities call — every Skill that resolved cleanly would be lost with it, and the
// import would report an execution-factory outage that never happened.
func TestImportSkipsToolInUnpublishedBox(t *testing.T) {
	Convey("工具箱未发布时只跳过这一条，其余照常绑定", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		service, cba, aoa := newTestServiceWithFactory(t, ctrl)
		aoa.EXPECT().GetSkillByID(gomock.Any(), "skill-1").
			Return(&interfaces.SkillBrief{SkillID: "skill-1", Status: interfaces.EXEC_SKILL_STATUS_PUBLISHED}, nil).AnyTimes()
		aoa.EXPECT().ListBoxTools(gomock.Any(), "draft-box").Return([]*interfaces.ToolBrief{
			{BoxID: "draft-box", BoxStatus: "unpublish", ToolID: "tool-1", Name: "BOM 展开",
				Status: interfaces.EXEC_TOOL_STATUS_ENABLED},
		}, nil).AnyTimes()
		cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
			gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil)
		cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

		report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
			&bknsdk.BknCapabilities{
				Skills: []*bknsdk.BknCapabilitySkill{{ID: "skill-1", Name: "交期评估"}},
				Functions: []*bknsdk.BknCapabilityFunction{{
					BoxID: "draft-box", ToolID: "tool-1",
					BoxName: "草稿箱", ToolName: "BOM 展开",
				}},
			})

		So(err, ShouldBeNil)
		So(report.Bound, ShouldEqual, 1)
		So(len(report.Skipped), ShouldEqual, 1)
		So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipUnusable)
		So(report.Skipped[0].Detail, ShouldContainSubstring, "unpublish")
	})
}

// TestImportResolvesMCPTools covers the MCP arm of a cross-environment import. The server is
// resolved by id then name; the tool is matched by name either way, because that is how MCP
// addresses tools and there is no second id to fall back from.
func TestImportResolvesMCPTools(t *testing.T) {
	Convey("导入解析 MCP 工具", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		published := []*interfaces.MCPToolBrief{
			{MCPID: "local-mcp", MCPName: "供应链 MCP",
				MCPStatus: interfaces.EXEC_BOX_STATUS_PUBLISHED, Name: "expedite"},
		}

		Convey("同环境:mcp_id 命中", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "local-mcp").Return(published, nil).AnyTimes()
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), "local-mcp", "expedite").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{MCPTools: []*bknsdk.BknCapabilityMCPTool{{
					MCPID: "local-mcp", MCPName: "供应链 MCP", ToolName: "expedite",
				}}})

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
			So(report.Skipped, ShouldBeEmpty)
		})

		Convey("跨环境:mcp_id 落空后按服务名兜底", func() {
			service, cba, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "mcp-from-elsewhere").Return(nil, nil)
			aoa.EXPECT().FindMCPServersByName(gomock.Any(), "供应链 MCP").
				Return([]string{"local-mcp"}, nil)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "local-mcp").Return(published, nil).AnyTimes()
			cba.EXPECT().GetBindingByCapability(gomock.Any(), gomock.Any(), gomock.Any(),
				gomock.Any(), "local-mcp", "expedite").Return(nil, nil)
			cba.EXPECT().CreateBindings(gomock.Any(), gomock.Nil(), gomock.Len(1)).Return(nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{MCPTools: []*bknsdk.BknCapabilityMCPTool{{
					MCPID: "mcp-from-elsewhere", MCPName: "供应链 MCP", ToolName: "expedite",
				}}})

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 1)
		})

		Convey("服务重名时不猜", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "gone").Return(nil, nil)
			aoa.EXPECT().FindMCPServersByName(gomock.Any(), "重名").
				Return([]string{"mcp-a", "mcp-b"}, nil)

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{MCPTools: []*bknsdk.BknCapabilityMCPTool{{
					MCPID: "gone", MCPName: "重名", ToolName: "expedite",
				}}})

			So(err, ShouldBeNil)
			So(report.Bound, ShouldEqual, 0)
			So(len(report.Skipped), ShouldEqual, 1)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipAmbiguous)
		})

		Convey("服务在但工具名不在", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "local-mcp").Return(published, nil).AnyTimes()

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{MCPTools: []*bknsdk.BknCapabilityMCPTool{{
					MCPID: "local-mcp", ToolName: "no_such_tool",
				}}})

			So(err, ShouldBeNil)
			So(len(report.Skipped), ShouldEqual, 1)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipNotFound)
		})

		Convey("服务未发布时跳过而非让整批失败", func() {
			service, _, aoa := newTestServiceWithFactory(t, ctrl)
			aoa.EXPECT().ListMCPTools(gomock.Any(), "draft-mcp").Return([]*interfaces.MCPToolBrief{
				{MCPID: "draft-mcp", MCPStatus: "unpublish", Name: "expedite"},
			}, nil).AnyTimes()

			report, err := service.ImportCapabilities(context.Background(), "kn1", "main",
				&bknsdk.BknCapabilities{MCPTools: []*bknsdk.BknCapabilityMCPTool{{
					MCPID: "draft-mcp", ToolName: "expedite",
				}}})

			So(err, ShouldBeNil)
			So(len(report.Skipped), ShouldEqual, 1)
			So(report.Skipped[0].Reason, ShouldEqual, CapabilitySkipUnusable)
		})
	})
}
