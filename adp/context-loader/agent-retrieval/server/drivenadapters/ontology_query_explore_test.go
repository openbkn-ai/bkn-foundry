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
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/smartystreets/goconvey/convey"
	"github.com/toon-format/toon-go"
	"go.uber.org/mock/gomock"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
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
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, []byte, error) {
				got = target
				return 200, jsonBytes(map[string]any{"objects": map[string]any{}, "relation_paths": []any{}}), nil
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

func TestExploreSubgraph_ForwardsOffsetPage(t *testing.T) {
	client, httpClient := newObjectQueryClient(t, gomock.NewController(t))
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			if body["paging_mode"] != nil || body["cursor"] != nil || body["limit"] != float64(10) || body["offset"] != float64(3) ||
				body["search_after"] != nil {
				t.Errorf("unexpected Ontology Query body %s", data)
			}
			return 200, []byte(`{"objects":{},"relation_paths":[],"cursor":"opaque-next"}`), nil
		})
	req := &interfaces.ExploreSubgraphReq{KnID: "kn1"}
	if err := json.Unmarshal([]byte(`{"source_object_type_id":"ot1","direction":"forward","path_length":2,"limit":10,"offset":3,"paging_mode":"cursor","cursor":"opaque-token"}`), req); err != nil {
		t.Fatal(err)
	}
	resp, err := client.ExploreSubgraph(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response["cursor"] != nil || response["search_after"] != nil {
		t.Fatalf("unexpected subgraph response %s", data)
	}
}

func TestSubgraphPreservesStartingBatchSortingMetadata(t *testing.T) {
	const warning = "API resource uses its configured order"
	unsorted := `{"objects":{},"relation_paths":[],"is_sorted":false,"warnings":["` + warning + `"]}`
	sorted := `{"objects":{},"relation_paths":[],"is_sorted":true}`
	explore, err := rawBodyClient(t, unsorted).ExploreSubgraph(t.Context(), &interfaces.ExploreSubgraphReq{
		KnID: "kn", SourceObjectTypeID: "source", Direction: "forward", PathLength: 1, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := rawBodyClient(t, `{"entries":[`+unsorted+`,`+sorted+`]}`).QueryInstanceSubgraph(t.Context(), &interfaces.QueryInstanceSubgraphReq{
		KnID: "kn",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, format := range []rest.ResponseFormat{rest.FormatJSON, rest.FormatTOON} {
		t.Run(string(format), func(t *testing.T) {
			decode := func(body any) map[string]any {
				t.Helper()
				_, wire, err := rest.MarshalResponse(format, body)
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]any
				if format == rest.FormatTOON {
					err = toon.Unmarshal(wire, &decoded)
				} else {
					err = json.Unmarshal(wire, &decoded)
				}
				if err != nil {
					t.Fatal(err)
				}
				return decoded
			}
			explore := decode(explore)
			if explore["is_sorted"] != false || !reflect.DeepEqual(explore["warnings"], []any{warning}) {
				t.Errorf("explore metadata = %#v", explore)
			}

			entries, ok := decode(paths)["entries"].([]any)
			if !ok || len(entries) != 2 {
				t.Fatalf("path entries = %#v", entries)
			}
			first := entries[0].(map[string]any)
			second := entries[1].(map[string]any)
			if first["is_sorted"] != false || !reflect.DeepEqual(first["warnings"], []any{warning}) ||
				second["is_sorted"] != true {
				t.Fatalf("path metadata = %#v", entries)
			}
			if _, present := second["warnings"]; present {
				t.Fatalf("unexpected warnings on the second path: %#v", second)
			}
		})
	}
}

func TestQueryInstanceSubgraph_ForwardsIndependentPathOffsets(t *testing.T) {
	client, httpClient := newObjectQueryClient(t, gomock.NewController(t))
	const body = `{"relation_type_paths":[
		{"object_types":[{"id":"ot1","limit":10,"offset":20},{"id":"ot2"}],"relation_types":[{"relation_type_id":"rel1"}]},
		{"object_types":[{"id":"ot3","limit":5,"offset":15},{"id":"ot4"}],"relation_types":[{"relation_type_id":"rel2"}]}
	]}`
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(body), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("downstream body = %s, want %s", data, body)
			}
			return http.StatusOK, []byte(`{"entries":[{},{}]}`), nil
		})
	req := &interfaces.QueryInstanceSubgraphReq{KnID: "kn1"}
	if err := json.Unmarshal([]byte(body), req); err != nil {
		t.Fatal(err)
	}
	if _, err := client.QueryInstanceSubgraph(t.Context(), req); err != nil {
		t.Fatal(err)
	}
}

// The total number of starting object types has the same origin and semantics as the object query, and need_total is also not an option for the caller.
func TestExploreSubgraph_AlwaysAsksForTotal(t *testing.T) {
	convey.Convey("need_total 无条件为 true", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)

		var body *interfaces.ExploreSubgraphReq
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, payload any) (int, []byte, error) {
				body = payload.(*interfaces.ExploreSubgraphReq)
				return 200, jsonBytes(map[string]any{"objects": map[string]any{}}), nil
			})

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 1, Limit: 10,
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(body.NeedTotal, convey.ShouldBeTrue)
	})
}

func TestExploreSubgraph_TotalCount(t *testing.T) {
	run := func(downstream map[string]any) *interfaces.ExploreSubgraphResp {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		client, mockHTTP := newObjectQueryClient(t, ctrl)
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, jsonBytes(downstream), nil)
		resp, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 1,
			Limit: 10, Offset: 10,
		})
		convey.So(err, convey.ShouldBeNil)
		return resp
	}

	convey.Convey("下游没回总数时保持缺失", t, func() {
		resp := run(map[string]any{"objects": map[string]any{}})
		convey.So(resp.TotalCount, convey.ShouldBeNil)
	})

	convey.Convey("下游确认的零命中返回 0", t, func() {
		resp := run(map[string]any{"objects": map[string]any{}, "total_count": 0})
		convey.So(resp.TotalCount, convey.ShouldNotBeNil)
		convey.So(*resp.TotalCount, convey.ShouldEqual, int64(0))
	})

	convey.Convey("下游给了总数就不动它", t, func() {
		resp := run(map[string]any{"objects": map[string]any{}, "total_count": 7})
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
		var he *httperrors.HTTPError
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
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, payload any) (int, []byte, error) {
				got = target
				bodyJSON, _ = json.Marshal(payload)
				return 200, jsonBytes(map[string]any{"objects": map[string]any{}}), nil
			})

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1?ignore_local_index=true", SourceObjectTypeID: "ot1",
			Direction: "bidirectional", PathLength: 3, Limit: 10,
			ExcludeSystemProperties: []string{"_display"},
			IgnoreLocalIndex:        true,
		})
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// The entire injection string remains in the path segment and fails to remove the parameters given by this layer.
		convey.So(parsed.Path, convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1?ignore_local_index=true/subgraph")
		convey.So(parsed.Query()["ignore_local_index"], convey.ShouldResemble, []string{"true"})
		// exclude_system_properties is effective in the exploration branch: clipping occurs at the subgraph assembly layer.
		// (expandObjectPathsBatch), not the commented out starting point object query.
		convey.So(parsed.Query()["exclude_system_properties"], convey.ShouldResemble, []string{"_display"})

		// Both are query parameternot request-body fields.
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "exclude_system_properties")
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "ignore_local_index")
		// kn_id goes through the URL and must not leak into the body.
		convey.So(string(bodyJSON), convey.ShouldNotContainSubstring, "kn1?ignore_local_index")
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
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(400, nil, downstream)

		_, err := client.ExploreSubgraph(context.Background(), &interfaces.ExploreSubgraphReq{
			KnID: "kn1", SourceObjectTypeID: "ot1", Direction: "forward", PathLength: 5, Limit: 10,
		})
		var he *httperrors.HTTPError
		convey.So(errors.As(err, &he), convey.ShouldBeTrue)
		convey.So(he.HTTPCode, convey.ShouldEqual, http.StatusBadRequest)
	})
}
