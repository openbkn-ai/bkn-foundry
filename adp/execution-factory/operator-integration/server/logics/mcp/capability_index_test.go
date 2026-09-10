// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// The admission rule for MCP (#1443): only a published server's tools are indexed. A draft or an
// offline server is purged, and its remote listing is not even attempted — the service has no
// proxy wired in this test, so any attempt would fail loudly.
func TestSyncMCPCapabilitiesPurgesAnUnpublishedServer(t *testing.T) {
	for _, status := range []interfaces.BizStatus{interfaces.BizStatusUnpublish, interfaces.BizStatusOffline, interfaces.BizStatusEditing} {
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
