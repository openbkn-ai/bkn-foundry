// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// stubAppKeys records whether Verify ran and resolves to a fixed AppKey owner so
// the test can tell the AppKey path apart from the hydra path (which resolves to
// "user-1" via stubPublicHydra).
type stubAppKeys struct{ called *bool }

func (s stubAppKeys) Verify(_ context.Context, _ string) (*interfaces.TokenInfo, error) {
	if s.called != nil {
		*s.called = true
	}
	return &interfaces.TokenInfo{VisitorID: "appkey-owner", VisitorTyp: interfaces.RealName}, nil
}

// runIntrospect drives middlewareIntrospectVerify with the given verifier and
// bearer token, returning the resolved auth context.
func runIntrospect(appKeys interfaces.AppKeyVerifier, token string) *interfaces.AccountAuthContext {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
	if token != "" {
		c.Request.Header.Set("Authorization", "Bearer "+token)
	}
	middlewareIntrospectVerify(stubPublicHydra{}, appKeys)(c)
	authCtx, _ := common.GetAccountAuthContextFromCtx(c.Request.Context())
	return authCtx
}

func TestMiddlewareIntrospectVerify_AppKeyRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	convey.Convey("middlewareIntrospectVerify routes by credential prefix", t, func() {
		convey.Convey("bak_ token -> AppKey verifier resolves owner", func() {
			called := false
			ac := runIntrospect(stubAppKeys{called: &called}, interfaces.AppKeyPrefix+"keyid_secret")
			convey.So(called, convey.ShouldBeTrue)
			convey.So(ac, convey.ShouldNotBeNil)
			convey.So(ac.AccountID, convey.ShouldEqual, "appkey-owner")
			convey.So(ac.AccountType, convey.ShouldEqual, interfaces.AccessorTypeUser)
		})

		convey.Convey("non-bak_ token -> hydra introspection", func() {
			called := false
			ac := runIntrospect(stubAppKeys{called: &called}, "ory_at_token")
			convey.So(called, convey.ShouldBeFalse) // AppKey verifier not consulted
			convey.So(ac.AccountID, convey.ShouldEqual, "user-1")
		})

		convey.Convey("nil AppKey verifier -> bak_ token falls back to hydra", func() {
			ac := runIntrospect(nil, interfaces.AppKeyPrefix+"keyid_secret")
			convey.So(ac.AccountID, convey.ShouldEqual, "user-1")
		})
	})
}
