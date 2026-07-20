// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// runIntrospectMiddleware 用给定凭据跑一遍公开面认证中间件，
// 返回响应记录与中间件放行后下游看到的认证上下文（未放行时为 nil）。
func runIntrospectMiddleware(
	hydra interfaces.Hydra,
	appKeys interfaces.AppKeyVerifier,
	authorization string,
) (*httptest.ResponseRecorder, *interfaces.AccountAuthContext) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)

	var seen *interfaces.AccountAuthContext
	engine.Use(middlewareIntrospectVerify(hydra, appKeys))
	engine.GET("/probe", func(c *gin.Context) {
		seen, _ = common.GetAccountAuthContextFromCtx(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	c.Request = req
	engine.ServeHTTP(w, req)
	return w, seen
}

func TestMiddlewareIntrospectVerifyCredentialRouting(t *testing.T) {
	Convey("公开面认证中间件按凭据前缀分流", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		hydra := mocks.NewMockHydra(ctrl)
		appKeys := mocks.NewMockAppKeyVerifier(ctrl)

		Convey("bak_ 前缀走 bkn-safe，不碰 hydra", func() {
			appKeys.EXPECT().Verify(gomock.Any(), "bak_abc").Return(&interfaces.TokenInfo{
				Active:     true,
				VisitorID:  "owner-1",
				VisitorTyp: interfaces.RealName,
			}, nil).Times(1)
			// hydra 未设置 EXPECT，被调用即失败

			w, seen := runIntrospectMiddleware(hydra, appKeys, "Bearer bak_abc")
			So(w.Code, ShouldEqual, http.StatusOK)
			So(seen, ShouldNotBeNil)
			So(seen.AccountID, ShouldEqual, "owner-1")
			So(seen.AccountType, ShouldEqual, interfaces.AccessorTypeUser)
		})

		Convey("普通 bearer token 走 hydra，不碰 bkn-safe", func() {
			hydra.EXPECT().Introspect(gomock.Any()).Return(&interfaces.TokenInfo{
				Active:     true,
				VisitorID:  "user-1",
				VisitorTyp: interfaces.RealName,
			}, nil).Times(1)
			// appKeys 未设置 EXPECT，被调用即失败

			w, seen := runIntrospectMiddleware(hydra, appKeys, "Bearer ory_at_normal")
			So(w.Code, ShouldEqual, http.StatusOK)
			So(seen.AccountID, ShouldEqual, "user-1")
		})

		Convey("两条凭据路径产出一致的认证上下文", func() {
			appKeys.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(&interfaces.TokenInfo{
				Active: true, VisitorID: "same-1", VisitorTyp: interfaces.RealName,
			}, nil)
			hydra.EXPECT().Introspect(gomock.Any()).Return(&interfaces.TokenInfo{
				Active: true, VisitorID: "same-1", VisitorTyp: interfaces.RealName,
			}, nil)

			_, viaKey := runIntrospectMiddleware(hydra, appKeys, "Bearer bak_same")
			_, viaToken := runIntrospectMiddleware(hydra, appKeys, "Bearer ory_at_same")
			So(viaKey.AccountID, ShouldEqual, viaToken.AccountID)
			So(viaKey.AccountType, ShouldEqual, viaToken.AccountType)
		})

		Convey("AppKey 校验失败时中止请求，下游不执行", func() {
			appKeys.EXPECT().Verify(gomock.Any(), gomock.Any()).
				Return(nil, fmt.Errorf("api key is invalid"))

			w, seen := runIntrospectMiddleware(hydra, appKeys, "Bearer bak_revoked")
			So(w.Code, ShouldNotEqual, http.StatusOK)
			So(seen, ShouldBeNil)
		})

		Convey("verifier 为 nil 时 bak_ 凭据回落 hydra，不 panic", func() {
			// AUTH_ENABLED=false 或 BKN_SAFE_URL 未配置的部署形态
			hydra.EXPECT().Introspect(gomock.Any()).Return(&interfaces.TokenInfo{
				Active: true, VisitorID: "fallback-1", VisitorTyp: interfaces.RealName,
			}, nil).Times(1)

			w, seen := runIntrospectMiddleware(hydra, nil, "Bearer bak_abc")
			So(w.Code, ShouldEqual, http.StatusOK)
			So(seen.AccountID, ShouldEqual, "fallback-1")
		})

		Convey("无凭据时走 hydra，由其决定放行或拒绝", func() {
			hydra.EXPECT().Introspect(gomock.Any()).Return(&interfaces.TokenInfo{
				Active: true, VisitorID: "", VisitorTyp: interfaces.RealName,
			}, nil).Times(1)

			w, _ := runIntrospectMiddleware(hydra, appKeys, "")
			So(w.Code, ShouldEqual, http.StatusOK)
		})
	})
}
