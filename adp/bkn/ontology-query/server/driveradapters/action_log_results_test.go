// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

func Test_RestHandler_QueryActionLogResultsByIn(t *testing.T) {
	Convey("GET .../action-logs/:log_id/results pages one execution's results (#790)", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		handler := MockNewRestHandler(&common.AppSetting{}, omock.NewMockAuthService(mockCtrl),
			omock.NewMockActionTypeService(mockCtrl), omock.NewMockKnowledgeNetworkService(mockCtrl),
			omock.NewMockObjectTypeService(mockCtrl))
		als := omock.NewMockActionLogsService(mockCtrl)
		handler.als = als
		handler.RegisterPublic(engine)

		get := func(query string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet,
				"/api/ontology-query/in/v1/knowledge-networks/kn_1/action-logs/exec_1/results"+query, nil)
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "user-1")
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}

		Convey("binds the page and filter and returns entries with the total", func() {
			als.EXPECT().QueryResults(gomock.Any(), &interfaces.ActionResultsQuery{
				KNID: "kn_1", LogID: "exec_1", Offset: 200, Limit: 50, Status: "failed",
			}).Return(&interfaces.ActionExecutionResultList{
				Entries:    []interfaces.ObjectExecutionResult{{Status: "failed", ErrorMessage: "boom"}},
				TotalCount: 251,
			}, nil)

			w := get("?offset=200&limit=50&status=failed")
			So(w.Code, ShouldEqual, http.StatusOK)
			var body interfaces.ActionExecutionResultList
			So(sonic.Unmarshal(w.Body.Bytes(), &body), ShouldBeNil)
			So(body.TotalCount, ShouldEqual, 251)
			So(body.Entries[0].ErrorMessage, ShouldEqual, "boom")
		})

		Convey("defaults limit to 100 and caps it at 1000", func() {
			als.EXPECT().QueryResults(gomock.Any(), &interfaces.ActionResultsQuery{KNID: "kn_1", LogID: "exec_1", Limit: 100}).
				Return(&interfaces.ActionExecutionResultList{}, nil)
			So(get("").Code, ShouldEqual, http.StatusOK)

			als.EXPECT().QueryResults(gomock.Any(), &interfaces.ActionResultsQuery{KNID: "kn_1", LogID: "exec_1", Limit: 1000}).
				Return(&interfaces.ActionExecutionResultList{}, nil)
			So(get("?limit=5000").Code, ShouldEqual, http.StatusOK)
		})

		Convey("rejects a page beyond the result window, a negative offset and an unknown status", func() {
			So(get("?offset=9990&limit=20").Code, ShouldEqual, http.StatusBadRequest)
			So(get("?offset=-1").Code, ShouldEqual, http.StatusBadRequest)
			So(get("?status=pending").Code, ShouldEqual, http.StatusBadRequest)
		})

		Convey("maps a missing execution to 404", func() {
			als.EXPECT().QueryResults(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("execution not found: exec_1"))
			So(get("").Code, ShouldEqual, http.StatusNotFound)
		})

		Convey("maps a storage failure to 500", func() {
			als.EXPECT().QueryResults(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("failed to query results: timeout"))
			So(get("").Code, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

func Test_RestHandler_GetActionLogRejectsPageBeyondWindow(t *testing.T) {
	Convey("the detail API rejects a results page beyond the window instead of reporting 404", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		handler := MockNewRestHandler(&common.AppSetting{}, omock.NewMockAuthService(mockCtrl),
			omock.NewMockActionTypeService(mockCtrl), omock.NewMockKnowledgeNetworkService(mockCtrl),
			omock.NewMockObjectTypeService(mockCtrl))
		handler.als = omock.NewMockActionLogsService(mockCtrl)
		handler.RegisterPublic(engine)

		req := httptest.NewRequest(http.MethodGet,
			"/api/ontology-query/in/v1/knowledge-networks/kn_1/action-logs/exec_1?results_offset=9950&results_limit=100", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		So(w.Code, ShouldEqual, http.StatusBadRequest)
	})
}
