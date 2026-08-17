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

// 探索模式与路径模板模式打的是同一个端点，唯一的区别就是 query_type。写成
// relation_path 会静默变成另一种模式：下游按 SubGraphQueryBaseOnTypePath 绑请求体，
// 拿不到 relation_type_paths 就返回空子图，不报错——所以这条必须钉死。
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

// 起点对象类的总数与对象查询同源同语义，need_total 同样不是调用方的选项。
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

// 三态语义必须和 query_object_instance 一字不差，否则同一个「缺失」在两个工具里
// 含义不同，比不做还糟。
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

// sort 的 nil 元素在探索模式一样会打 panic（起点排序同样过下游 BuildDslQuery），
// 拦截逻辑与对象查询共用，这里盯的是「探索这条路真的调了它」。
func TestExploreSubgraph_RejectsNilSortEntry(t *testing.T) {
	convey.Convey("sort 里的 null 元素回 400 且不发请求", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, _ := newObjectQueryClient(t, ctrl)
		// 不 EXPECT Post：必须在本层拦下。

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

// 内部专用参数进查询串、不进请求体；kn_id 转义后才拼进 path。
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
		// 注入串整体留在 path 段，没能顶掉本层给的参数
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1?ignoring_store_cache=true/subgraph")
		convey.So(parsed.Query()["ignoring_store_cache"], convey.ShouldResemble, []string{"true"})
		// exclude_system_properties 在探索分支是生效的：裁剪发生在子图组装那层
		// （expandObjectPathsBatch），不是被注释掉的那条起点对象查询。
		convey.So(parsed.Query()["exclude_system_properties"], convey.ShouldResemble, []string{"_display"})

		// 两者是 query 参数不是请求体字段
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignoring_store_cache")
		// kn_id 走 URL，也不该混进 body
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "kn1?ignoring_store_cache")
		// 探索模式的三个必填项确实进了 body
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"source_object_type_id":"ot1"`)
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"direction":"bidirectional"`)
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"path_length":3`)
	})
}

// 下游对非法方向 / path_length 超 3 / 起点对象类不存在都回 4xx 且带可执行详情，
// 这些是调用方的错，不能塌陷成「依赖服务异常」。
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
