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

// The app endpoint's URL names only the server, so it serves what is released, like the proxy.
// The version picks the released deployment by key; without it the endpoint looked up version 0,
// which matches any deployment, the draft's included, and queries them all on every request (#1525).
func TestGetMCPInstanceConfigResolvesTheReleasedVersion(t *testing.T) {
	Convey("tool_imported 的 MCP Server:app 端点按发布版本取实例", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		releases := mocks.NewMockDBMCPServerRelease(ctrl)
		releases.EXPECT().SelectByMCPID(gomock.Any(), gomock.Nil(), "mcp-1").Return(&model.MCPServerReleaseDB{
			MCPID:        "mcp-1",
			Version:      2,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, nil)
		auth := mocks.NewMockIAuthorizationService(ctrl)
		auth.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "u-1"}, nil)
		auth.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "mcp-1", interfaces.AuthResourceTypeMCP).Return(nil)
		svc := &mcpServiceImpl{
			logger:             logger.DefaultLogger(),
			AuthService:        auth,
			DBMCPServerRelease: releases,
		}

		config, err := svc.GetMCPInstanceConfig(context.Background(), "mcp-1", interfaces.MCPModeStream)
		So(err, ShouldBeNil)
		So(config.MCPID, ShouldEqual, "mcp-1")
		So(config.Mode, ShouldEqual, interfaces.MCPModeStream)
		So(config.Version, ShouldEqual, 2)
	})
}
