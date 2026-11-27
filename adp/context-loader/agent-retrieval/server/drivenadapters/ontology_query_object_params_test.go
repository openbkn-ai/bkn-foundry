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

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

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
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)

	return &ontologyQueryClient{
		logger:     mockLogger,
		baseURL:    "http://ontology-query:13018/api/ontology-query",
		httpClient: mockHTTP,
	}, mockHTTP
}

// Context Loader requests an optional total on every object query.
func TestQueryObjectInstances_AlwaysAsksForTotal(t *testing.T) {
	convey.Convey("need_total 无条件为 true", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var body *interfaces.QueryObjectInstancesReq
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
				body = payload.(*interfaces.QueryObjectInstancesReq)
				return 200, jsonBytes(map[string]any{"datas": []any{}}), nil
			})

		req := &interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10}
		resp, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(body.NeedTotal, convey.ShouldBeTrue)
		responseJSON, marshalErr := json.Marshal(resp)
		convey.So(marshalErr, convey.ShouldBeNil)
		convey.So(string(responseJSON), convey.ShouldContainSubstring, `"is_sorted":false`)
		convey.So(string(responseJSON), convey.ShouldNotContainSubstring, `"warnings"`)
	})
}

// The optional total preserves a known zero.
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

func TestQueryObjectInstances_PreservesOptionalTotal(t *testing.T) {
	convey.Convey("普通查询保持缺失的总数", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]any{"datas": []any{map[string]any{"id": "i1"}}}), nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldBeNil)
		convey.So(resp.Data, convey.ShouldHaveLength, 1)
	})

	convey.Convey("带 cursor 时保持缺失，不伪造 0", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]any{"datas": []any{map[string]any{"id": "i1"}}}), nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{
				KnID: "kn1", OtID: "ot1", Limit: 10, PagingMode: new("cursor"), Cursor: "opaque-cursor",
			})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldBeNil)
	})

	convey.Convey("下游给了真实总数就不动它", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]any{"datas": []any{}, "total_count": 42}), nil)

		resp, err := client.QueryObjectInstances(context.Background(),
			&interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(42))
	})

	convey.Convey("下游确认的零命中返回 0", t, func() {
		client, mockHTTP := newObjectQueryClient(t, gomock.NewController(t))
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(map[string]any{"datas": []any{}, "total_count": 0}), nil)

		resp, err := client.QueryObjectInstances(t.Context(),
			&interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10})
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(0))
	})
}

func TestQueryObjectInstances_ForwardsPagingMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		mode string
	}{
		{name: "default single", body: `{"offset":3}`},
		{name: "explicit single", body: `{"paging_mode":"single","offset":3}`, mode: "single"},
		{name: "cursor first page", body: `{"paging_mode":"cursor","offset":3}`, mode: "cursor"},
		{name: "cursor continuation", body: `{"paging_mode":"cursor","offset":3,"cursor":"token"}`, mode: "cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, httpClient := newObjectQueryClient(t, gomock.NewController(t))
			httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
					wire, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					var sent map[string]any
					if err := json.Unmarshal(wire, &sent); err != nil {
						t.Fatal(err)
					}
					mode, sentMode := sent["paging_mode"]
					if sentMode != (tc.mode != "") || sentMode && mode != tc.mode ||
						sent["offset"] != float64(3) || sent["limit"] != float64(10) {
						t.Errorf("unexpected Ontology Query body %s", wire)
					}
					return 200, []byte(`{"datas":[]}`), nil
				})
			req := &interfaces.QueryObjectInstancesReq{KnID: "kn1", OtID: "ot1", Limit: 10}
			if err := json.Unmarshal([]byte(tc.body), req); err != nil {
				t.Fatal(err)
			}
			if _, err := client.QueryObjectInstances(t.Context(), req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueryObjectInstances_ForwardsOpaqueCursorWithoutRawSearchAfter(t *testing.T) {
	convey.Convey("cursor 原样透传且响应不暴露 search_after", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)
		var bodyJSON []byte
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
				bodyJSON, _ = json.Marshal(payload)
				return 200, jsonBytes(map[string]any{
					"datas":     []any{map[string]any{"id": "i1"}},
					"cursor":    "next-opaque-cursor",
					"is_sorted": true,
					"warnings":  []string{"read from the resource source"},
				}), nil
			})

		resp, err := client.QueryObjectInstances(context.Background(), &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1", Limit: 10, PagingMode: new("cursor"), Cursor: "opaque-cursor",
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(bodyJSON), convey.ShouldContainSubstring, `"cursor":"opaque-cursor"`)
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "search_after")
		convey.So(resp.Cursor, convey.ShouldEqual, "next-opaque-cursor")
		responseJSON, marshalErr := json.Marshal(resp)
		convey.So(marshalErr, convey.ShouldBeNil)
		convey.So(string(responseJSON), convey.ShouldNotContainSubstring, "search_after")
		convey.So(string(responseJSON), convey.ShouldContainSubstring, `"is_sorted":true`)
		convey.So(string(responseJSON), convey.ShouldContainSubstring, `"warnings":["read from the resource source"]`)
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

		var he *httperrors.HTTPError
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
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
				body = payload.(*interfaces.QueryObjectInstancesReq)
				return 200, jsonBytes(map[string]any{"datas": []any{}}), nil
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

// exclude_system_properties / ignore_local_index are downstream query parameters, not request body fields.
// The entire req will be directly serialized into the body, so both must be marked with json: "-" so that they will not be mixed into the body.
func TestQueryObjectInstances_InternalParamsGoToQueryStringNotBody(t *testing.T) {
	convey.Convey("内部参数进查询串且不落 body", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		var bodyJSON []byte
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, payload any) (int, []byte, error) {
				got = target
				bodyJSON, _ = json.Marshal(payload)
				return 200, jsonBytes(map[string]any{"datas": []any{}, "query_source": "source"}), nil
			})

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1", Limit: 10,
			ExcludeSystemProperties: []string{"_instance_id", "_display"},
			IgnoreLocalIndex:        true,
		}
		resp, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.QuerySource, convey.ShouldEqual, "source")

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		q := parsed.Query()
		convey.So(q["exclude_system_properties"], convey.ShouldResemble, []string{"_instance_id", "_display"})
		convey.So(q.Get("ignore_local_index"), convey.ShouldEqual, "true")
		// Existing parameters have not been changed by this reconstruction.
		convey.So(q.Get("include_type_info"), convey.ShouldEqual, "false")
		convey.So(q.Get("include_logic_params"), convey.ShouldEqual, "false")

		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignore_local_index")
	})
}

// Two parameters that are turned off by default should not appear in the query string out of thin air: ignore_local_index explicitly bypasses a table resource local index.
// Data source, one order of magnitude slower, sending it accidentally is more dangerous than not sending it.
func TestQueryObjectInstances_OmitsInternalParamsWhenUnset(t *testing.T) {
	convey.Convey("未设置时不发内部参数", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, []byte, error) {
				got = target
				return 200, jsonBytes(map[string]any{"datas": []any{}}), nil
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
		_, hasIgnoring := parsed.Query()["ignore_local_index"]
		convey.So(hasIgnoring, convey.ShouldBeFalse)
	})
}

// ot_id is freely filled in by the agent. If path is entered without escaping, a value with "?" can be inserted downstream.
// Query parameters such as ignore_local_index - are the same type of injection surface as metric_id.
func TestQueryObjectInstances_EscapesIDsIntoPath(t *testing.T) {
	convey.Convey("kn_id / ot_id 转义后才进 URL", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var got string
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, []byte, error) {
				got = target
				return 200, jsonBytes(map[string]any{"datas": []any{}}), nil
			})

		req := &interfaces.QueryObjectInstancesReq{
			KnID: "kn1", OtID: "ot1?ignore_local_index=true&x=", Limit: 10,
		}
		_, err := client.QueryObjectInstances(context.Background(), req)
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// The entire injected string remains in the path segment, and the downstream will only treat it as a non-existent ot_id.
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/object-types/ot1?ignore_local_index=true&x=")
		// The value set by this layer was not overridden.
		convey.So(parsed.Query().Get("ignore_local_index"), convey.ShouldBeEmpty)
		convey.So(parsed.Query().Get("include_type_info"), convey.ShouldEqual, "false")
	})
}
