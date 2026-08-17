// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knquerysubgraph

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type recordingSubgraphService struct{ called bool }

func (s *recordingSubgraphService) QueryInstanceSubgraph(_ context.Context, _ *interfaces.QueryInstanceSubgraphReq) (*interfaces.QueryInstanceSubgraphResp, error) {
	return &interfaces.QueryInstanceSubgraphResp{}, nil
}

func (s *recordingSubgraphService) ExploreSubgraph(_ context.Context, _ *interfaces.ExploreSubgraphReq) (*interfaces.ExploreSubgraphResp, error) {
	s.called = true
	return &interfaces.ExploreSubgraphResp{Objects: map[string]any{}}, nil
}

func exploreRequest(t *testing.T, body string) (*httptest.ResponseRecorder, *recordingSubgraphService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	service := &recordingSubgraphService{}
	handler := &knQuerySubgraphHandler{Logger: logger.DefaultLogger(), KnQuerySubgraphService: service}

	engine := gin.New()
	engine.POST("/kn/explore_subgraph", handler.ExploreSubgraph)

	req := httptest.NewRequest(http.MethodPost, "/kn/explore_subgraph?kn_id=kn1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w, service
}

// MCP 那侧显式拦了 path_length <= 0，REST 这侧不能只靠下游——下游只拦 > 3，对 0
// 不报错、返回空子图。两个入口对同一份参数行为不同，就是「同一个请求换个门进来
// 结果不一样」，而且失败形态最坏：漏填参数被读成「什么都没连上」。
// 规则钉在结构体的 validate tag 上，两个入口才共用同一条。
func TestExploreSubgraph_RESTRejectsMissingPathLength(t *testing.T) {
	convey.Convey("REST 入口漏传 path_length 回 400，不放行到下游", t, func() {
		w, service := exploreRequest(t, `{"source_object_type_id":"ot1","direction":"forward"}`)
		convey.So(w.Code, convey.ShouldEqual, http.StatusBadRequest)
		convey.So(service.called, convey.ShouldBeFalse)
	})
}

func TestExploreSubgraph_RESTRejectsPathLengthAboveDownstreamLimit(t *testing.T) {
	convey.Convey("path_length 超过下游上限 3 时本层就回 400", t, func() {
		w, service := exploreRequest(t,
			`{"source_object_type_id":"ot1","direction":"forward","path_length":4}`)
		convey.So(w.Code, convey.ShouldEqual, http.StatusBadRequest)
		convey.So(service.called, convey.ShouldBeFalse)
	})
}

func TestExploreSubgraph_RESTAcceptsValidPathLength(t *testing.T) {
	convey.Convey("1-3 之间照常放行", t, func() {
		w, service := exploreRequest(t,
			`{"source_object_type_id":"ot1","direction":"forward","path_length":2}`)
		convey.So(w.Code, convey.ShouldEqual, http.StatusOK)
		convey.So(service.called, convey.ShouldBeTrue)
	})
}
