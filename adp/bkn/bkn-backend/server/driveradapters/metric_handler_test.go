// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

func mockNewMetricRestHandler(
	appSetting *common.AppSetting,
	as interfaces.AuthService,
	ms interfaces.MetricService,
	kns interfaces.KNService,
) *restHandler {
	return &restHandler{
		appSetting: appSetting,
		as:         as,
		ms:         ms,
		kns:        kns,
	}
}

func Test_MetricRestHandler_ValidateMetricsRouteAlias(t *testing.T) {
	Convey("ValidateMetrics is reachable on /metrics/validate (SDK/CLI alias)\n", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		as := bmock.NewMockAuthService(mockCtrl)
		ms := bmock.NewMockMetricService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)

		handler := mockNewMetricRestHandler(appSetting, as, ms, kns)
		handler.RegisterPublic(engine)

		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)

		knID := "kn1"
		body, _ := sonic.Marshal(struct {
			Entries []*interfaces.MetricDefinition `json:"entries"`
		}{Entries: nil})

		for _, pathSuffix := range []string{"validation", "validate"} {
			pathSuffix := pathSuffix
			Convey("external POST .../metrics/"+pathSuffix+" is not 404\n", func() {
				kns.EXPECT().CheckKNExistByID(gomock.Any(), knID, gomock.Any()).Return(knID, true, nil)

				url := "/api/bkn-backend/v1/knowledge-networks/" + knID + "/metrics/" + pathSuffix
				req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
				req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, req)

				So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
				So(w.Body.String(), ShouldContainSubstring, "No metric was passed in")
			})
		}

		Convey("internal POST .../metrics/validate is not 404\n", func() {
			kns.EXPECT().CheckKNExistByID(gomock.Any(), knID, gomock.Any()).Return(knID, true, nil)

			url := "/api/bkn-backend/in/v1/knowledge-networks/" + knID + "/metrics/validate"
			req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			req.Header.Set(interfaces.CONTENT_TYPE_NAME, interfaces.CONTENT_TYPE_JSON)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			So(w.Result().StatusCode, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "No metric was passed in")
		})
	})
}
