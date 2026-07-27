package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestParseSSEAuthz 覆盖 #345：解析接口会驱动服务端向调用方给定的 URL 发起出站请求，
// 公开面必须先过 MCP 类型级新建权限。
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

		// 内部面（internal-v1）未注册该端点，且解析逻辑必须建 MCP 客户端、加载运行时配置，
		// 单测环境无法进入，因此内部面放行只由 IsPublicAPIFromCtx 判定本身保证，不在此断言。
	})
}
