package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestParseSSEAuthz covers #345: The parsing interface will drive the server to initiate an outbound request to the URL given by the caller.
// The public face must first have MCP type-level new permissions.
func TestParseSSEAuthz(t *testing.T) {
	Convey("ParseSSE 授权", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		publicCtx := common.SetPublicAPIToCtx(context.Background(), true)
		req := &interfaces.MCPParseSSERequest{
			Mode: interfaces.MCPModeSSE,
			URL:  "http://127.0.0.1:1/sse",
		}

		Convey("无新建权限时拒绝，不发起出站请求", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			svc := &mcpServiceImpl{logger: logger.DefaultLogger(), AuthService: authService}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().CheckCreatePermission(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeMCP).
				Return(errors.New("create forbidden"))

			resp, err := svc.ParseSSE(publicCtx, req)
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "create forbidden")
		})

		// The endpoint is not registered in the internal plane (internal-v1), and the parsing logic must build the MCP client and load the runtime configuration.
		// The single test environment cannot be entered, so internal surface release is only guaranteed by the IsPublicAPIFromCtx judgment itself and is not asserted here.
	})
}
