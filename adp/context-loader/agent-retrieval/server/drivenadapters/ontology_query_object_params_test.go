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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// newObjectQueryClient returns a client under test that only cares about "what the sent URL and request body look like".
func newObjectQueryClient(t *testing.T, ctrl *gomock.Controller) (*ontologyQueryClient, *mocks.MockHTTPClient) {
	t.Helper()

	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)

	return &ontologyQueryClient{
		logger:     mockLogger,
		baseURL:    "http://ontology-query:13018/api/ontology-query",
		httpClient: mockHTTP,
	}, mockHTTP
}

// need_total is the request body field. If it is not set downstream, total_count will not be returned. The caller only knows "Is there a next page?".
// The total number of hits is not known - this is not optional, so this layer is unconditionally set to true, and anything passed by the caller will be overwritten.
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

// total_count is three-state, and must be pinned in both directions:
// - True zero hits must be serialized to 0 (omitempty on the pointer only swallows nil, not 0), otherwise the caller sees.
// The field is missing, and the valid conclusion is regarded as service failure;
// - Must be missing when the downstream does not calculate the total (from the second page of the cursor, the downstream is forced to NeedTotal=false), otherwise.
// 0 is a false zero hit and is inconsistent with non-empty datas.
func TestQueryObjectInstancesResp_TotalCountIsThreeState(t *testing.T) {
	convey.Convey("零命中序列化成 0", t, func() {
		zero := int64(0)
		out, err := json.Marshal(&interfaces.QueryObjectInstancesResp{Data: []any{}, TotalCount: &zero})
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(out), convey.ShouldContainSubstring, `"total_count":0`)
	})

	convey.Convey("下游未返回总数时字段缺失，不伪造 0", t, func() {
		out, err := json.Marshal(&interfaces.QueryObjectInstancesResp{
			Data: []any{map[string]any{"id": "inst_1"}},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(out), convey.ShouldNotContainSubstring, "total_count")
	})
}

// The downstream Objects.TotalCount brings its own omitempty. The real 0 cannot be transmitted online at all. Just look at the response body.
// Can't tell the difference between "zero hit" and "no count". The criteria are in the request: no cursor ⇒ calculated, missing is 0; there is a cursor ⇒ downstream.
// The total calculation is forced to be turned off and must remain missing.
func TestQueryObjectInstances_ResolvesAbsentTotalFromRequest(t *testing.T) {
	convey.Convey("无 search_after 时缺失的总数补成 0", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]any{"datas": []any{}}, nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(0))
	})

	convey.Convey("带 search_after 时保持缺失，不伪造 0", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]any{"datas": []any{map[string]any{"id": "i1"}}}, nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{
				KnID: "kn1", OtID: "ot1", Limit: 10, SearchAfter: []any{"cursor"},
			})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldBeNil)
	})

	convey.Convey("下游给了真实总数就不动它", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, map[string]any{"datas": []any{}, "total_count": 42}, nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(42))
	})
}

// "sort":[null] is tied to []*SortSpec{nil}. Downstream validate.go and logics/common.go are directly fetched.
// sp.Field, forwarding will result in a null pointer panic instead of 400, so structural nil must be blocked at this layer.
func TestQueryObjectInstances_RejectsNilSortEntry(t *testing.T) {
	convey.Convey("sort 里的 null 元素回 400 且不发请求", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)
		// No EXPECT Post: The request must be blocked at this layer, and not a single byte should be sent to the downstream.
		_ = mockHTTP

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1", Limit: 10,
			Sort: []*interfaces.SortSpec{{Field: "created_at", Direction: "desc"}, nil},
		}
		resp, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(resp, convey.ShouldBeNil)
		convey.So(err, convey.ShouldNotBeNil)

		var he *infraErr.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusBadRequest)
	})
}

// Sort is transmitted transparently as it is: only the downstream knows whether the field belongs to the object type, and checking half of it at this layer will only cause the rules on both sides to drift.
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

// exclude_system_properties / ignoring_store_cache are downstream query parameters, not request body fields.
// The entire req will be directly serialized into the body, so both must be marked with json: "-" so that they will not be mixed into the body.
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
		// Existing parameters have not been changed by this reconstruction.
		convey.So(q.Get("include_type_info"), convey.ShouldEqual, "false")
		convey.So(q.Get("include_logic_params"), convey.ShouldEqual, "false")

		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignoring_store_cache")
	})
}

// Two parameters that are turned off by default should not appear in the query string out of thin air: ignoring_store_cache will push the query away from the index.
// Data source, one order of magnitude slower, sending it accidentally is more dangerous than not sending it.
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

// ot_id is freely filled in by the agent. If path is entered without escaping, a value with "?" can be inserted downstream.
// Query parameters such as ignoring_store_cache - are the same type of injection surface as metric_id.
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
		// The entire injected string remains in the path segment, and the downstream will only treat it as a non-existent ot_id.
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/object-types/ot1?ignoring_store_cache=true&x=")
		// The value set by this layer was not overridden.
		convey.So(parsed.Query().Get("ignoring_store_cache"), convey.ShouldBeEmpty)
		convey.So(parsed.Query().Get("include_type_info"), convey.ShouldEqual, "false")
	})
}
