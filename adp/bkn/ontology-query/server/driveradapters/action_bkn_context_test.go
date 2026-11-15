// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

// An execution stores the Conversation and Interaction its caller declares, from the body's
// bkn_context or the trace headers, and never guesses (#2658).
func Test_RestHandler_ExecuteAction_BKNContext(t *testing.T) {
	Convey("POST .../action-types/:at_id/execute resolves bkn_context", t, func() {
		test := setGinMode()
		defer test()

		engine := gin.New()
		engine.Use(gin.Recovery())
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		handler := MockNewRestHandler(&common.AppSetting{}, omock.NewMockAuthService(mockCtrl),
			omock.NewMockActionTypeService(mockCtrl), omock.NewMockKnowledgeNetworkService(mockCtrl),
			omock.NewMockObjectTypeService(mockCtrl))
		ass := omock.NewMockActionSchedulerService(mockCtrl)
		handler.ass = ass
		handler.RegisterPublic(engine)

		post := func(body string, headers map[string]string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost,
				"/api/ontology-query/in/v1/knowledge-networks/kn_1/action-types/at_1/execute", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "user-1")
			req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}
		var seen *interfaces.ActionExecutionRequest
		accept := func() {
			ass.EXPECT().ExecuteAction(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionResponse, error) {
					seen = req
					return &interfaces.ActionExecutionResponse{ExecutionID: "e1", Status: "pending"}, nil
				})
		}
		seen = nil

		Convey("body bkn_context is stored", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"conv_1","interaction_id":"int_1"}}`, nil)
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldEqual, "conv_1")
			So(seen.InteractionID, ShouldEqual, "int_1")
		})

		Convey("without a body bkn_context the headers are used", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}]}`, map[string]string{
				common.HeaderBKNConversationID: "conv_h", common.HeaderBKNInteractionID: "int_h",
			})
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldEqual, "conv_h")
			So(seen.InteractionID, ShouldEqual, "int_h")
		})

		Convey("a body value equal to the header is accepted", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"conv_1","interaction_id":"int_1"}}`, map[string]string{
				common.HeaderBKNConversationID: "conv_1", common.HeaderBKNInteractionID: "int_1",
			})
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldEqual, "conv_1")
		})

		Convey("a body that differs from the header is rejected, not resolved", func() {
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"conv_1","interaction_id":"int_1"}}`, map[string]string{
				common.HeaderBKNConversationID: "conv_other", common.HeaderBKNInteractionID: "int_1",
			})
			So(w.Code, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "conflicts")
		})

		Convey("a malformed body id is rejected, not stored", func() {
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"bad id!","interaction_id":"int_1"}}`, nil)
			So(w.Code, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "conversation_id")
		})

		Convey("an interaction without its conversation is rejected", func() {
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"interaction_id":"int_1"}}`, nil)
			So(w.Code, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "interaction_id requires conversation_id")
		})

		Convey("a header-only interaction without a conversation is accepted with no ids recorded", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}]}`, map[string]string{
				common.HeaderBKNInteractionID: "int_h",
			})
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldBeEmpty)
			So(seen.InteractionID, ShouldBeEmpty)
		})

		Convey("a body interaction with only a header interaction is still rejected", func() {
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"interaction_id":"int_1"}}`, map[string]string{
				common.HeaderBKNInteractionID: "int_1",
			})
			So(w.Code, ShouldEqual, http.StatusBadRequest)
			So(w.Body.String(), ShouldContainSubstring, "interaction_id requires conversation_id")
		})

		Convey("a conversation alone is allowed", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"conv_1"}}`, nil)
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldEqual, "conv_1")
			So(seen.InteractionID, ShouldBeEmpty)
		})

		Convey("a request with neither stores neither", func() {
			accept()
			w := post(`{"_instance_identities":[{"id":"1"}]}`, nil)
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldBeEmpty)
			So(seen.InteractionID, ShouldBeEmpty)
		})

		Convey("a scheduled execution keeps both empty even when the caller sends them", func() {
			accept()
			w := post(`{"trigger_type":"scheduled","_instance_identities":[{"id":"1"}],"bkn_context":{"conversation_id":"conv_1","interaction_id":"int_1"}}`, map[string]string{
				common.HeaderBKNConversationID: "conv_1", common.HeaderBKNInteractionID: "int_1",
			})
			So(w.Code, ShouldEqual, http.StatusAccepted)
			So(seen.ConversationID, ShouldBeEmpty)
			So(seen.InteractionID, ShouldBeEmpty)
		})
	})
}

// The action-logs list accepts the two ids as exact-match filters.
func Test_RestHandler_QueryActionLogs_BKNContextFilters(t *testing.T) {
	Convey("GET .../action-logs forwards conversation_id and interaction_id", t, func() {
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

		var seen *interfaces.ActionLogQuery
		als.EXPECT().QueryExecutions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, q *interfaces.ActionLogQuery) (*interfaces.ActionExecutionList, error) {
				seen = q
				return &interfaces.ActionExecutionList{}, nil
			})
		req := httptest.NewRequest(http.MethodGet,
			"/api/ontology-query/in/v1/knowledge-networks/kn_1/action-logs?conversation_id=conv_1&interaction_id=int_1&status=completed", nil)
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, "user-1")
		req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, "user")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		So(w.Code, ShouldEqual, http.StatusOK)
		So(seen.ConversationID, ShouldEqual, "conv_1")
		So(seen.InteractionID, ShouldEqual, "int_1")
		So(seen.Status, ShouldEqual, "completed")
	})
}
