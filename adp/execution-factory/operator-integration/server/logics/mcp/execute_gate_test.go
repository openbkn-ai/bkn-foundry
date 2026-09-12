// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// The execution gate for an MCP Server's publication (#1483). Nothing beyond the config table is
// wired into this service, so reaching the proxy would fail loudly: the refusal has to come first.
func TestCallMCPToolRefusesAnUnpublishedServer(t *testing.T) {
	for _, status := range []interfaces.BizStatus{interfaces.BizStatusOffline, interfaces.BizStatusUnpublish, interfaces.BizStatusEditing} {
		Convey("MCP Server 状态为 "+string(status)+":调用被拒,不建连", t, func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			configs := mocks.NewMockDBMCPServerConfig(ctrl)
			configs.EXPECT().SelectByID(gomock.Any(), gomock.Nil(), "mcp-1").
				Return(&model.MCPServerConfigDB{MCPID: "mcp-1", Status: string(status)}, nil)
			// The caller is authorized to execute: the refusal must come from lifecycle, not permission.
			auth := mocks.NewMockIAuthorizationService(ctrl)
			auth.EXPECT().GetAccessor(gomock.Any(), gomock.Any()).Return(&interfaces.AuthAccessor{ID: "u-1"}, nil)
			auth.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "mcp-1", interfaces.AuthResourceTypeMCP).Return(nil)
			svc := &mcpServiceImpl{logger: logger.DefaultLogger(), AuthService: auth, DBMCPServerConfig: configs}

			_, err := svc.CallMCPTool(context.Background(), &interfaces.MCPProxyCallToolRequest{MCPID: "mcp-1", ToolName: "t"})
			So(err, ShouldNotBeNil)
			So(strings.Contains(err.Error(), "MCPServerNotPublished"), ShouldBeTrue)
		})
	}
}
