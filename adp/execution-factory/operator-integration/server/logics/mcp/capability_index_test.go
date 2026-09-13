// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// The admission rule for MCP (#1443): only a server being served has its tools indexed. A draft or
// an offline server is purged, and its remote listing is not even attempted — the service has no
// proxy wired in this test, so any attempt would fail loudly. An editing server is served from its
// release and stays indexed (#1524, below).
func TestSyncMCPCapabilitiesPurgesAnUnpublishedServer(t *testing.T) {
	for _, status := range []interfaces.BizStatus{interfaces.BizStatusUnpublish, interfaces.BizStatusOffline} {
		Convey("MCP Server 状态为 "+string(status)+":删除其全部索引文档,不去拉工具列表", t, func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			configs := mocks.NewMockDBMCPServerConfig(ctrl)
			configs.EXPECT().SelectByID(gomock.Any(), gomock.Any(), "mcp-1").
				Return(&model.MCPServerConfigDB{MCPID: "mcp-1", Status: string(status)}, nil)
			index := mocks.NewMockCapabilityIndexSyncService(ctrl)
			index.EXPECT().DeleteOwner(gomock.Any(), interfaces.CapabilityTypeMCPTool, "mcp-1").Return(nil)

			svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerConfig: configs, CapabilityIndex: index}
			So(svc.syncMCPCapabilities(context.Background(), "mcp-1"), ShouldBeNil)
		})
	}
}

// releasedInstances serves one in-process MCP server per version, so a sync lists whichever
// version it resolved to for real.
type releasedInstances struct {
	interfaces.InstanceService
	servers map[int]*server.MCPServer
}

func (r *releasedInstances) GetMCPInstance(_ context.Context, _ string, version int) (*interfaces.MCPServerInstance, error) {
	return &interfaces.MCPServerInstance{MCPServer: r.servers[version]}, nil
}

func mcpServerWithTools(names ...string) *server.MCPServer {
	s := server.NewMCPServer("fixture", "1.0.0", server.WithToolCapabilities(true))
	for _, name := range names {
		s.AddTool(mcp.NewTool(name), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	}
	return s
}

// An editing server is served from its release, so its released tools stay searchable while the
// draft is edited; the draft's tools join the index only when it is published (#1524).
func TestSyncMCPCapabilitiesIndexesAnEditingServerFromItsRelease(t *testing.T) {
	Convey("editing 的 MCP Server:按已发布版本写索引,不删除,草稿里的工具不进索引", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		config := &model.MCPServerConfigDB{
			MCPID:        "mcp-1",
			Status:       string(interfaces.BizStatusEditing),
			Version:      3,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}
		configs := mocks.NewMockDBMCPServerConfig(ctrl)
		configs.EXPECT().SelectByID(gomock.Any(), gomock.Any(), "mcp-1").Return(config, nil)
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").
			Return(&model.MCPServerReleaseDB{MCPID: "mcp-1", Version: 2}, nil)
		instances := &releasedInstances{servers: map[int]*server.MCPServer{
			2: mcpServerWithTools("released_tool"),
			3: mcpServerWithTools("released_tool", "draft_only_tool"),
		}}
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		var upserted []string
		index.EXPECT().UpsertCapability(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, doc *interfaces.CapabilityDocument) error {
				upserted = append(upserted, doc.CapabilityID)
				return nil
			}).AnyTimes()
		index.EXPECT().ListIndexedByOwner(gomock.Any(), interfaces.CapabilityTypeMCPTool, "mcp-1").Return(nil, nil)

		svc := &mcpServiceImpl{
			logger:             logger.DefaultLogger(),
			DBMCPServerConfig:  configs,
			DBMCPServerRelease: releases,
			MCPInstanceService: instances,
			CapabilityIndex:    index,
		}
		So(svc.syncMCPCapabilities(context.Background(), "mcp-1"), ShouldBeNil)
		So(upserted, ShouldResemble, []string{"released_tool"})
	})
}

// An editing server with no release — imported that way — is not served, so its draft's tools must
// not reach the index either: it is purged like a draft, without listing anything (#1524).
func TestSyncMCPCapabilitiesPurgesAnEditingServerWithoutARelease(t *testing.T) {
	Convey("editing 却没有发布记录:删除其索引文档,不拉草稿的工具列表", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		configs := mocks.NewMockDBMCPServerConfig(ctrl)
		configs.EXPECT().SelectByID(gomock.Any(), gomock.Any(), "mcp-1").Return(&model.MCPServerConfigDB{
			MCPID:        "mcp-1",
			Status:       string(interfaces.BizStatusEditing),
			Version:      3,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, nil)
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").Return(nil, nil)
		index := mocks.NewMockCapabilityIndexSyncService(ctrl)
		index.EXPECT().DeleteOwner(gomock.Any(), interfaces.CapabilityTypeMCPTool, "mcp-1").Return(nil)
		instances := &releasedInstances{servers: map[int]*server.MCPServer{3: mcpServerWithTools("draft_tool")}}

		svc := &mcpServiceImpl{
			logger:             logger.DefaultLogger(),
			DBMCPServerConfig:  configs,
			DBMCPServerRelease: releases,
			MCPInstanceService: instances,
			CapabilityIndex:    index,
		}
		So(svc.syncMCPCapabilities(context.Background(), "mcp-1"), ShouldBeNil)
	})
}
