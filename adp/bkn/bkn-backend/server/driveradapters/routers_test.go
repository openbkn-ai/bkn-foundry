// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	bmock "bkn-backend/interfaces/mock"
)

// setGinMode sets Gin to test mode and returns a restore function.
func setGinMode() func() {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	return func() {
		gin.SetMode(oldMode)
	}
}

func Test_RestHandler_HealthCheck(t *testing.T) {
	Convey("Test HealthCheck\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		handler := &restHandler{appSetting: &common.AppSetting{}}
		handler.RegisterPublic(engine)

		for _, path := range []string{"/api/bkn-backend/v1/health"} {
			Convey("It serves health information without authentication at "+path, func() {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, req)

				So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
				So(w.Body.String(), ShouldContainSubstring, "ServerVersion")
			})
		}

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		So(w.Result().StatusCode, ShouldEqual, http.StatusNotFound)
	})
}

func Test_RestHandler_VerifyOAuth_Failure(t *testing.T) {
	Convey("Test verifyOAuth returns 401 when token verification fails\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		engine := gin.New()
		engine.Use(gin.Recovery())

		as := bmock.NewMockAuthService(mockCtrl)
		handler := &restHandler{
			appSetting: &common.AppSetting{},
			as:         as,
		}
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(hydra.Visitor{}, errors.New("invalid token"))

		req := httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/knowledge-networks", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Result().StatusCode, ShouldEqual, http.StatusUnauthorized)
	})
}

func TestRegisterPublicIncludesOAuthProxyGovernanceRoutes(t *testing.T) {
	restoreGin := setGinMode()
	defer restoreGin()
	engine := gin.New()
	handler := &restHandler{appSetting: &common.AppSetting{}}
	handler.RegisterPublic(engine)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"GET /api/bkn-backend/v1/proxy-accounts",
		"GET /api/bkn-backend/v1/knowledge-networks/:kn_id/proxy-account",
		"GET /api/bkn-backend/v1/knowledge-networks/:kn_id/proxy-account/plan",
		"POST /api/bkn-backend/v1/knowledge-networks/:kn_id/proxy-account/sync",
		"POST /api/bkn-backend/v1/proxy-accounts/reconcile",
	} {
		if !routes[want] {
			t.Fatalf("public proxy governance route %q is not registered", want)
		}
	}
}
