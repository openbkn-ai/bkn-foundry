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

// The MCP side explicitly blocks path_length <= 0, and the REST side cannot only rely on the downstream side - the downstream side only blocks > 3, for 0.
// No error is reported and an empty subgraph is returned. The two entrances have different behaviors for the same parameter, that is, "the same request comes in through another door.".
// The results are different", and the failure pattern is the worst: the missing parameter is read as "nothing is connected".
// The rules are nailed to the validate tag of the structure, and the two entries share the same one.
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
