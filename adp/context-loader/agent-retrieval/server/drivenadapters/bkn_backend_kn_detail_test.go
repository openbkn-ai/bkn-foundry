// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

const knDetailTestBase = "http://localhost:8080/api/bkn-backend/in/v1/knowledge-networks/kn1"

func newKNDetailTestClient(t *testing.T) (*bknBackendAccess, *mocks.MockHTTPClient, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)
	return &bknBackendAccess{
		logger:     mockLogger,
		baseURL:    "http://localhost:8080/api/bkn-backend",
		httpClient: mockHTTP,
	}, mockHTTP, ctrl
}

type knDetailResponse struct {
	code int
	body string
	err  error
}

// knDetailServer answers the shell and the four child lists by URL, and records every query it
// was asked, so a test can pin which endpoints were read and how.
type knDetailServer struct {
	mu        sync.Mutex
	responses map[string]knDetailResponse
	queries   map[string]url.Values
}

func newKNDetailServer() *knDetailServer {
	return &knDetailServer{
		responses: map[string]knDetailResponse{
			"": {code: http.StatusOK, body: `{"id":"kn1","name":"Network","comment":"About",
				"object_types":[{"id":"not-from-the-shell"}]}`},
			"/concept-groups": {code: http.StatusOK, body: `{"entries":[
				{"id":"cg1","name":"Group","object_type_ids":["ot1"]}],"total_count":1}`},
			"/object-types": {code: http.StatusOK, body: `{"entries":[
				{"id":"ot1","name":"Material"}],"total_count":1}`},
			"/relation-types": {code: http.StatusOK, body: `{"entries":[
				{"id":"rt1","name":"Uses","source_object_type_id":"ot1","target_object_type_id":"ot2"}],"total_count":1}`},
			"/action-types": {code: http.StatusOK, body: `{"entries":[
				{"id":"at1","name":"Restock","object_type_id":"ot1"}],"total_count":1}`},
		},
		queries: map[string]url.Values{},
	}
}

func (s *knDetailServer) serve(_ context.Context, src string, query url.Values, _ map[string]string) (int, []byte, error) {
	suffix := strings.TrimPrefix(src, knDetailTestBase)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries[suffix] = query
	resp, ok := s.responses[suffix]
	if !ok {
		return http.StatusInternalServerError, nil, errors.New("unexpected request " + src)
	}
	return resp.code, []byte(resp.body), resp.err
}

// TestGetKnowledgeNetworkDetail_ComposesChildListsWithoutExport pins the read path of #1532.
//
// mode=export requires view_detail on the network itself, so a caller authorized only on some
// child resources was refused outright even though every child list would have answered with
// exactly what that caller may see. The detail is the default network read plus the four child
// lists, and export is never asked for.
func TestGetKnowledgeNetworkDetail_ComposesChildListsWithoutExport(t *testing.T) {
	convey.Convey("网络详情由默认读取与四个子资源列表组装，不走导出模式", t, func() {
		client, mockHTTP, ctrl := newKNDetailTestClient(t)
		defer ctrl.Finish()
		server := newKNDetailServer()
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(server.serve).Times(5)

		detail, err := client.GetKnowledgeNetworkDetail(context.Background(), "kn1")

		convey.So(err, convey.ShouldBeNil)
		convey.So(detail.ID, convey.ShouldEqual, "kn1")
		convey.So(detail.Name, convey.ShouldEqual, "Network")
		convey.So(detail.Comment, convey.ShouldEqual, "About")
		convey.So(len(detail.ConceptGroups), convey.ShouldEqual, 1)
		convey.So(detail.ConceptGroups[0].ObjectTypeIDs, convey.ShouldResemble, []string{"ot1"})
		convey.So(len(detail.ObjectTypes), convey.ShouldEqual, 1)
		convey.So(detail.ObjectTypes[0].ID, convey.ShouldEqual, "ot1")
		convey.So(len(detail.RelationTypes), convey.ShouldEqual, 1)
		convey.So(detail.RelationTypes[0].TargetObjectTypeID, convey.ShouldEqual, "ot2")
		convey.So(len(detail.ActionTypes), convey.ShouldEqual, 1)
		convey.So(detail.ActionTypes[0].ObjectTypeID, convey.ShouldEqual, "ot1")

		for suffix, query := range server.queries {
			convey.So(query.Get("mode"), convey.ShouldBeEmpty)
			if suffix == "" {
				continue
			}
			convey.So(query.Get("limit"), convey.ShouldEqual, "-1")
			convey.So(query.Get("sort"), convey.ShouldEqual, "name")
			convey.So(query.Get("direction"), convey.ShouldEqual, "asc")
		}
	})
}

// TestGetKnowledgeNetworkDetail_ShellRefusalStopsBeforeLists keeps the network read as the gate.
//
// The child lists only check that the network exists, so they answer a caller who can see
// nothing with empty lists. Reading them first would turn a refusal into an empty network.
func TestGetKnowledgeNetworkDetail_ShellRefusalStopsBeforeLists(t *testing.T) {
	convey.Convey("网络读取被拒时原样返回错误，且不再读取子资源列表", t, func() {
		client, mockHTTP, ctrl := newKNDetailTestClient(t)
		defer ctrl.Finish()
		server := newKNDetailServer()
		server.responses[""] = knDetailResponse{code: http.StatusForbidden,
			body: `{"error_code":"Public.Forbidden","description":"forbidden"}`}
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(server.serve).Times(1)

		_, err := client.GetKnowledgeNetworkDetail(context.Background(), "kn1")

		var httpErr *infraErr.HTTPError
		convey.So(errors.As(err, &httpErr), convey.ShouldBeTrue)
		convey.So(httpErr.HTTPCode, convey.ShouldEqual, http.StatusForbidden)
		convey.So(httpErr.Code, convey.ShouldEqual, "Public.Forbidden")
	})
}

// TestGetKnowledgeNetworkDetail_AnyListFailureFailsTheWhole refuses to answer with part of a model.
//
// A network missing its relation types reads as a network that has none; an authorization
// service outage would then look like a successful, smaller answer.
func TestGetKnowledgeNetworkDetail_AnyListFailureFailsTheWhole(t *testing.T) {
	for _, suffix := range []string{"/concept-groups", "/object-types", "/relation-types", "/action-types"} {
		suffix := suffix
		convey.Convey("子资源列表失败时整体失败: "+suffix, t, func() {
			client, mockHTTP, ctrl := newKNDetailTestClient(t)
			defer ctrl.Finish()
			server := newKNDetailServer()
			server.responses[suffix] = knDetailResponse{code: http.StatusServiceUnavailable,
				body: `{"error_code":"BknBackend.InternalError","description":"authorization unavailable"}`}
			mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(server.serve).Times(5)

			detail, err := client.GetKnowledgeNetworkDetail(context.Background(), "kn1")

			var httpErr *infraErr.HTTPError
			convey.So(errors.As(err, &httpErr), convey.ShouldBeTrue)
			convey.So(httpErr.HTTPCode, convey.ShouldEqual, http.StatusServiceUnavailable)
			convey.So(detail, convey.ShouldResemble, &interfaces.KnowledgeNetworkDetail{ID: "kn1"})
		})
	}

	// bkn-backend always wraps a list as {"entries": ...}. An empty 200 body is not "this
	// network has no object types"; accepting it would present a broken read as a small network.
	for _, suffix := range []string{"", "/concept-groups", "/object-types", "/relation-types", "/action-types"} {
		suffix := suffix
		convey.Convey("响应体为空时整体失败而非当作空网络: "+suffix, t, func() {
			client, mockHTTP, ctrl := newKNDetailTestClient(t)
			defer ctrl.Finish()
			server := newKNDetailServer()
			server.responses[suffix] = knDetailResponse{code: http.StatusOK}
			calls := 5
			if suffix == "" {
				calls = 1
			}
			mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(server.serve).Times(calls)

			_, err := client.GetKnowledgeNetworkDetail(context.Background(), "kn1")

			var httpErr *infraErr.HTTPError
			convey.So(errors.As(err, &httpErr), convey.ShouldBeTrue)
			convey.So(httpErr.HTTPCode, convey.ShouldEqual, http.StatusServiceUnavailable)
		})
	}

	convey.Convey("子资源列表请求本身失败时整体失败", t, func() {
		client, mockHTTP, ctrl := newKNDetailTestClient(t)
		defer ctrl.Finish()
		server := newKNDetailServer()
		server.responses["/object-types"] = knDetailResponse{code: 0, err: errors.New("connection reset")}
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(server.serve).Times(5)

		_, err := client.GetKnowledgeNetworkDetail(context.Background(), "kn1")

		convey.So(err, convey.ShouldNotBeNil)
	})
}
