// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// The exploration mode and the path template mode hit the same endpoint, the only difference is query_type. written as.
// relation_path will silently change to another mode: the downstream binds the request body according to SubGraphQueryBaseOnTypePath,
// If relation_type_paths cannot be obtained, an empty subgraph will be returned and no error will be reported - so this must be nailed down.
func TestExploreSubgraph_UsesEmptyQueryType(t *testing.T) {
	convey.Convey("query_type 为空串走探索分支", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, any, error) {
				got = target
				return 200, map[string]any{"objects": map[string]any{}, "relation_paths": []any{}}, nil
			})

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 2, Limit: 10,
		})
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		q := parsed.Query()
		_, present := q["query_type"]
		convey.So(present, convey.ShouldBeTrue)
		convey.So(q.Get("query_type"), convey.ShouldBeEmpty)
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/subgraph")
	})
}

// The total number of starting object types has the same origin and semantics as the object query, and need_total is also not an option for the caller.
func TestExploreSubgraph_AlwaysAsksForTotal(t *testing.T) {
	convey.Convey("need_total 无条件为 true", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var body *interfaces.ExploreSubgraphReq
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, any, error) {
				body = payload.(*interfaces.ExploreSubgraphReq)
				return 200, map[string]any{"objects": map[string]any{}}, nil
			})

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 1, Limit: 10,
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(body.NeedTotal, convey.ShouldBeTrue)
	})
}

// The tri-state semantics must match query_object_instance exactly; otherwise the same "missing" state
// would mean different things in the two tools, which is worse than not handling it.
func TestExploreSubgraph_TotalCountThreeState(t *testing.T) {
	run := func(cursor []any, downstream map[string]any) *interfaces.ExploreSubgraphResp {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, downstream, nil)
		resp, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 1,
			Limit: 10, SearchAfter: cursor,
		})
		convey.So(err, convey.ShouldBeNil)
		return resp
	}

	convey.Convey("无游标且下游没回总数 => 补成 0", t, func() {
		resp := run(nil, map[string]any{"objects": map[string]any{}})
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(0))
	})

	convey.Convey("有游标且下游没回总数 => 保持缺失", t, func() {
		resp := run([]any{"cursor"}, map[string]any{"objects": map[string]any{}})
		convey.So(resp.TotalCount, convey.ShouldBeNil)
	})

	convey.Convey("下游给了总数就不动它", t, func() {
		resp := run(nil, map[string]any{"objects": map[string]any{}, "total_count": 7})
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(7))
	})
}

// The nil element of sort will also panic in exploration mode (the starting point sorting also goes through the downstream BuildDslQuery).
// The interception logic is shared with object query. The focus here is "exploring this path and really invoked it".
func TestExploreSubgraph_RejectsNilSortEntry(t *testing.T) {
	convey.Convey("sort 里的 null 元素回 400 且不发请求", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, _ := newObjectQueryClient(t, ctrl)
		// Do not EXPECT Post: it must be blocked in this layer.

		resp, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 1, Limit: 10,
			Sort: []*interfaces.SortSpec{nil},
		})
		convey.So(resp, convey.ShouldBeNil)
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusBadRequest)
	})
}

// Internal special parameters are entered into the query string and not into the request body; kn_id is escaped and then spelled into path.
func TestExploreSubgraph_InternalParamsAndEscaping(t *testing.T) {
	convey.Convey("内部参数进查询串，kn_id 转义", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		var bodyJSON []byte
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, payload any) (int, any, error) {
				got = target
				bodyJSON, _ = json.Marshal(payload)
				return 200, map[string]any{"objects": map[string]any{}}, nil
			})

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1?ignoring_store_cache=true", SourceObjectTypeID: "ot1",
			Direction: "bidirectional", PathLength: 3, Limit: 10,
			ExcludeSystemProperties: []string{"_display"},
			IgnoringStoreCache:      true,
		})
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// The entire injection string remains in the path segment and fails to remove the parameters given by this layer.
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1?ignoring_store_cache=true/subgraph")
		convey.So(parsed.Query()["ignoring_store_cache"], convey.ShouldResemble, []string{"true"})
		// exclude_system_properties is effective in the exploration branch: clipping occurs at the subgraph assembly layer.
		// (expandObjectPathsBatch), not the commented out starting point object query.
		convey.So(parsed.Query()["exclude_system_properties"], convey.ShouldResemble, []string{"_display"})

		// Both are query parameternot request-body fields.
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignoring_store_cache")
		// kn_id goes through the URL and must not leak into the body.
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "kn1?ignoring_store_cache")
		// The three required fields for exploration mode are indeed in the body.
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"source_object_type_id":"ot1"`)
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"direction":"bidirectional"`)
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"path_length":3`)
	})
}

// Downstream will return 4xx with executable details for illegal directions / path_length exceeds 3 / starting point object type does not exist.
// These are the fault of the caller and cannot be collapsed into "dependency service exceptions".
func TestExploreSubgraph_PreservesDownstreamClientError(t *testing.T) {
	convey.Convey("下游 400 保留状态码与详情", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)

		downstream := infraErr.DefaultHTTPError(context.Background(), http.StatusBadRequest,
			"路径长度不能超过3, 当前路径长度为5")
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(400, nil, downstream)

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 5, Limit: 10,
		})
		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusBadRequest)
	})
}
