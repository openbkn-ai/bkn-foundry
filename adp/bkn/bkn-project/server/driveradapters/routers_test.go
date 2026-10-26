// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func setGinMode() func() {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	return func() {
		gin.SetMode(oldMode)
	}
}

type fakePinger struct{ err error }

func (p fakePinger) PingContext(context.Context) error { return p.err }

func serve(handler *restHandler, method, path string) *httptest.ResponseRecorder {
	engine := gin.New()
	engine.Use(gin.Recovery())
	handler.RegisterPublic(engine)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func Test_RestHandler_HealthCheck(t *testing.T) {
	Convey("Test HealthCheck\n", t, func() {
		test := setGinMode()
		defer test()

		w := serve(&restHandler{appSetting: &common.AppSetting{}}, http.MethodGet, "/api/bkn-project/v1/health")

		So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, `"ServerName":"bkn-project"`)
	})
}

func Test_RestHandler_ReadyCheck(t *testing.T) {
	Convey("Test ReadyCheck\n", t, func() {
		test := setGinMode()
		defer test()

		Convey("Ready when the database answers\n", func() {
			w := serve(&restHandler{appSetting: &common.AppSetting{}, db: fakePinger{}}, http.MethodGet, "/api/bkn-project/v1/ready")

			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"status":"ready"`)
		})

		Convey("Unavailable when the database does not answer\n", func() {
			w := serve(&restHandler{appSetting: &common.AppSetting{}, db: fakePinger{err: errors.New("db down")}}, http.MethodGet, "/api/bkn-project/v1/ready")

			So(w.Result().StatusCode, ShouldEqual, http.StatusServiceUnavailable)
		})
	})
}

func Test_RestHandler_VerifyOAuth_Failure(t *testing.T) {
	Convey("Test release routes return 401 when token verification fails\n", t, func() {
		test := setGinMode()
		defer test()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := pmock.NewMockAuthService(mockCtrl)
		rs := pmock.NewMockReleaseService(mockCtrl)
		handler := &restHandler{appSetting: &common.AppSetting{}, as: as, rs: rs}

		for _, path := range []string{
			"/api/bkn-project/v1/lineages/orders/releases",
			"/api/bkn-project/v1/releases/orders_1-0-0",
		} {
			as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(hydra.Visitor{}, errors.New("invalid token"))

			w := serve(handler, http.MethodGet, path)

			So(w.Result().StatusCode, ShouldEqual, http.StatusUnauthorized)
		}
	})
}
