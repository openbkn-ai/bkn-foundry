// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// newObjectQueryClient 返回一个只关心「发出去的 URL 与请求体长什么样」的被测客户端。
func newObjectQueryClient(t *testing.T, ctrl *gomock.Controller) (*ontologyQueryClient, *mocks.MockHTTPClient) {
	t.Helper()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)

	return &ontologyQueryClient{
		logger:     mockLogger,
		baseURL:    "http://ontology-query:13018/api/ontology-query",
		httpClient: mockHTTP,
	}, mockHTTP
}

// need_total 是请求体字段。不设它下游就不回 total_count，调用方只知道「还有没有下一页」，
// 不知道命中总量——这不是可选项，所以本层无条件置 true，调用方传什么都覆盖。
func TestQueryObjectInstances_AlwaysAsksForTotal(t *testing.T) {
	convey.Convey("need_total 无条件为 true", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var body *interfaces.QueryObjectInstancesReq
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, any, error) {
				body = payload.(*interfaces.QueryObjectInstancesReq)
				return 200, map[string]any{"datas": []any{}}, nil
			})

		req := &interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(body.NeedTotal, convey.ShouldBeTrue)
	})
}

// total_count 必须连 0 一起序列化。带 omitempty 的话零命中与「服务端没回」在调用方
// 看来长得一模一样，而零命中是有效结论，不该缺失。
func TestQueryObjectInstancesResp_SerializesZeroTotal(t *testing.T) {
	convey.Convey("零命中时 total_count 仍出现且为 0", t, func() {
		out, err := json.Marshal(&interfaces.QueryObjectInstancesResp{Data: []any{}})
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(out), convey.ShouldContainSubstring, `"total_count":0`)
	})
}

// sort 原样透传：field 是否属于该对象类只有下游知道，本层再校验一半只会让两侧规则漂移。
func TestQueryObjectInstances_ForwardsSort(t *testing.T) {
	convey.Convey("sort 原样进请求体", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var body *interfaces.QueryObjectInstancesReq
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, any, error) {
				body = payload.(*interfaces.QueryObjectInstancesReq)
				return 200, map[string]any{"datas": []any{}}, nil
			})

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1", Limit: 10,
			Sort: []*interfaces.SortSpec{
				{Field: "created_at", Direction: "desc"},
				{Field: "amount", Direction: "asc"},
			},
		}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(body.Sort), convey.ShouldEqual, 2)
		convey.So(body.Sort[0].Field, convey.ShouldEqual, "created_at")
		convey.So(body.Sort[0].Direction, convey.ShouldEqual, "desc")
		convey.So(body.Sort[1].Field, convey.ShouldEqual, "amount")
	})
}

// exclude_system_properties / ignoring_store_cache 是下游的 query 参数，不是请求体字段。
// 整个 req 会被直接序列化成 body，所以两者必须标 json:"-" 才不会混进 body。
func TestQueryObjectInstances_InternalParamsGoToQueryStringNotBody(t *testing.T) {
	convey.Convey("内部参数进查询串且不落 body", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		var bodyJSON []byte
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, payload any) (int, any, error) {
				got = target
				bodyJSON, _ = json.Marshal(payload)
				return 200, map[string]any{"datas": []any{}}, nil
			})

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1", Limit: 10,
			ExcludeSystemProperties: []string{"_instance_id", "_display"},
			IgnoringStoreCache:      true,
		}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		q := parsed.Query()
		convey.So(q["exclude_system_properties"], convey.ShouldResemble, []string{"_instance_id", "_display"})
		convey.So(q.Get("ignoring_store_cache"), convey.ShouldEqual, "true")
		// 既有参数没被这次重构改掉
		convey.So(q.Get("include_type_info"), convey.ShouldEqual, "false")
		convey.So(q.Get("include_logic_params"), convey.ShouldEqual, "false")

		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignoring_store_cache")
	})
}

// 两个默认关闭的参数不该凭空出现在查询串里：ignoring_store_cache 会把查询从索引赶到
// 数据源，慢一个数量级，误发比不发危险。
func TestQueryObjectInstances_OmitsInternalParamsWhenUnset(t *testing.T) {
	convey.Convey("未设置时不发内部参数", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, any, error) {
				got = target
				return 200, map[string]any{"datas": []any{}}, nil
			})

		req := &interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/object-types/ot1")
		_, hasExclude := parsed.Query()["exclude_system_properties"]
		convey.So(hasExclude, convey.ShouldBeFalse)
		_, hasIgnoring := parsed.Query()["ignoring_store_cache"]
		convey.So(hasIgnoring, convey.ShouldBeFalse)
	})
}

// ot_id 由 agent 自由填写。未转义就拼进 path 的话，一个带 "?" 的值就能给下游塞进
// ignoring_store_cache 等查询参数——与 metric_id 是同一类注入面。
func TestQueryObjectInstances_EscapesIDsIntoPath(t *testing.T) {
	convey.Convey("kn_id / ot_id 转义后才进 URL", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, any, error) {
				got = target
				return 200, map[string]any{"datas": []any{}}, nil
			})

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1?ignoring_store_cache=true&x=", Limit: 10,
		}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// 注入串整体留在 path 段，下游只会把它当成一个不存在的 ot_id
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/object-types/ot1?ignoring_store_cache=true&x=")
		// 本层给的值没被顶掉
		convey.So(parsed.Query().Get("ignoring_store_cache"), convey.ShouldBeEmpty)
		convey.So(parsed.Query().Get("include_type_info"), convey.ShouldEqual, "false")
	})
}
