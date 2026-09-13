// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

var errInstanceReached = errors.New("instance lookup reached")

// versionRecordingInstances stands in for the instance pool: it notes which version a proxy
// request resolved to and stops there, so no in-process MCP server has to be built.
type versionRecordingInstances struct {
	interfaces.InstanceService
	versions []int
}

func (r *versionRecordingInstances) GetMCPInstance(_ context.Context, _ string, version int) (*interfaces.MCPServerInstance, error) {
	r.versions = append(r.versions, version)
	return nil, errInstanceReached
}

// An editing MCP Server is a published server with a draft beside it. Its release keeps serving
// the proxy until the draft is published; the draft's version may have no instance at all (#1478).
func TestProxyServesTheReleaseWhileEditing(t *testing.T) {
	Convey("editing 的 MCP Server:代理 list 与 call 都按已发布版本取实例", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		configs := mocks.NewMockDBMCPServerConfig(ctrl)
		configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerConfigDB{
			MCPID:        "mcp-1",
			Status:       string(interfaces.BizStatusEditing),
			Version:      3,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, nil).Times(2)
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").
			Return(&model.MCPServerReleaseDB{MCPID: "mcp-1", Version: 2}, nil).Times(2)
		auth := mocks.NewMockIAuthorizationService(ctrl)
		auth.EXPECT().GetAccessor(gomock.Any(), gomock.Any()).Return(&interfaces.AuthAccessor{ID: "u-1"}, nil)
		auth.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "mcp-1", interfaces.AuthResourceTypeMCP).Return(nil)
		instances := &versionRecordingInstances{}
		svc := &mcpServiceImpl{
			logger:             logger.DefaultLogger(),
			AuthService:        auth,
			DBMCPServerConfig:  configs,
			DBMCPServerRelease: releases,
			MCPInstanceService: instances,
		}

		_, err := svc.CallMCPTool(context.Background(), &interfaces.MCPProxyCallToolRequest{MCPID: "mcp-1", ToolName: "t"})
		So(errors.Is(err, errInstanceReached), ShouldBeTrue)
		_, err = svc.GetMCPTools(context.Background(), &interfaces.MCPProxyToolListRequest{MCPID: "mcp-1"})
		So(errors.Is(err, errInstanceReached), ShouldBeTrue)
		So(instances.versions, ShouldResemble, []int{2, 2})
	})

	Convey("published 的 MCP Server:配置即发布内容,按配置版本取实例,不查发布表", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		configs := mocks.NewMockDBMCPServerConfig(ctrl)
		configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerConfigDB{
			MCPID:        "mcp-1",
			Status:       string(interfaces.BizStatusPublished),
			Version:      2,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, nil)
		instances := &versionRecordingInstances{}
		svc := &mcpServiceImpl{
			logger:             logger.DefaultLogger(),
			DBMCPServerConfig:  configs,
			DBMCPServerRelease: mocks.NewMockDBMCPServerRelease(ctrl),
			MCPInstanceService: instances,
		}

		_, err := svc.GetMCPTools(context.Background(), &interfaces.MCPProxyToolListRequest{MCPID: "mcp-1"})
		So(errors.Is(err, errInstanceReached), ShouldBeTrue)
		So(instances.versions, ShouldResemble, []int{2})
	})
}

// A custom server proxies an external endpoint, so the version alone does not pick what is served:
// while it is editing, the released URL, mode and headers must be used, not the draft's.
func TestServingListToolsRequestUsesTheReleasedEndpointWhileEditing(t *testing.T) {
	Convey("editing 的 custom MCP Server:连接信息取自发布内容", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerReleaseDB{
			MCPID:   "mcp-1",
			Version: 2,
			Mode:    string(interfaces.MCPModeSSE),
			URL:     "http://released.example/sse",
			Headers: `{"X-Env":"released"}`,
		}, nil)
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerRelease: releases}

		req, err := svc.servingListToolsRequest(context.Background(), &model.MCPServerConfigDB{
			MCPID:        "mcp-1",
			Status:       string(interfaces.BizStatusEditing),
			Version:      3,
			CreationType: interfaces.MCPCreationTypeCustom.String(),
			Mode:         string(interfaces.MCPModeStream),
			URL:          "http://draft.example/mcp",
			Headers:      `{"X-Env":"draft"}`,
		})
		So(err, ShouldBeNil)
		So(req.CreationType, ShouldEqual, interfaces.MCPCreationTypeCustom)
		So(req.Version, ShouldEqual, 2)
		So(req.MCPCoreInfo.Mode, ShouldEqual, interfaces.MCPModeSSE)
		So(req.MCPCoreInfo.URL, ShouldEqual, "http://released.example/sse")
		So(req.MCPCoreInfo.Headers, ShouldResemble, map[string]string{"X-Env": "released"})
	})

	Convey("editing 却没有发布记录:退回配置本身,不比修复前更差", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").Return(nil, nil)
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerRelease: releases}

		req, err := svc.servingListToolsRequest(context.Background(), &model.MCPServerConfigDB{
			MCPID:   "mcp-1",
			Status:  string(interfaces.BizStatusEditing),
			Version: 3,
			URL:     "http://draft.example/mcp",
		})
		So(err, ShouldBeNil)
		So(req.Version, ShouldEqual, 3)
		So(req.MCPCoreInfo.URL, ShouldEqual, "http://draft.example/mcp")
	})
}
