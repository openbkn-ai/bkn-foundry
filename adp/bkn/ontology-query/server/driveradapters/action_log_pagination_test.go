// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
)

// Cursor paging and offset paging are mutually exclusive: OpenSearch fails a non-zero from next to
// search_after, and that used to surface as a 500 (#1669).
func Test_RestHandler_QueryActionLogs_OffsetAndSearchAfterAreExclusive(t *testing.T) {
	Convey("GET .../action-logs rejects offset together with search_after", t, func() {
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
				"/api/ontology-query/in/v1/knowledge-networks/kn_1/action-logs"+query, nil)
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "user-1")
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}

		cursor := "?search_after=1789780257572,01a0b737-6b24-7d37-b8c1-61095804a879"

		Convey("both given: 400 without reaching the service", func() {
			w := get(cursor + "&offset=10&limit=1")
			So(w.Code, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "search_after")
		})

		Convey("cursor alone: forwarded with a zero offset", func() {
			var seen *interfaces.ActionLogQuery
			als.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, query *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
					seen = query
					return &interfaces.ActionExecutionList{}, nil
				})

			So(get(cursor+"&limit=1").Code, ShouldEqual, http.StatusOK)
			So(seen.Offset, ShouldEqual, 0)
			So(len(seen.SearchAfter), ShouldEqual, 2)
		})

		Convey("offset alone: forwarded as is", func() {
			var seen *interfaces.ActionLogQuery
			als.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, query *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
					seen = query
					return &interfaces.ActionExecutionList{}, nil
				})

			So(get("?offset=10&limit=1").Code, ShouldEqual, http.StatusOK)
			So(seen.Offset, ShouldEqual, 10)
			So(seen.SearchAfter, ShouldBeEmpty)
		})
	})
}
