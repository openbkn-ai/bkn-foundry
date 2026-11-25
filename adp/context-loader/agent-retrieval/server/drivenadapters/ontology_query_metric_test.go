// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

// The metric ID is caller input. Escaping prevents it from changing the pinned version.
func TestQueryMetricData_EscapesIDsIntoPath(t *testing.T) {
	convey.Convey("kn_id / metric_id 转义后才进 URL", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		mockHTTP := mocks.NewMockHTTPClient(ctrl)

		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://ontology-query:13018/api/ontology-query",
			httpClient: mockHTTP,
		}

		var got string
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, []byte, error) {
				got = target
				return 200, jsonBytes(map[string]any{"entries": []any{}, "status": "no_data"}), nil
			})

		_, err := client.QueryMetricData(context.Background(), "kn1", "release",
			"m1/data?branch=dev&x=", 3, &interfaces.ObjectMetricQuery{})
		convey.So(err, convey.ShouldBeNil)

		parsed, perr := url.Parse(got)
		convey.So(perr, convey.ShouldBeNil)
		// The caller's branch is explicit; the injected branch failed to replace it.
		convey.So(parsed.Query().Get("branch"), convey.ShouldEqual, "release")
		convey.So(parsed.Query().Get("version"), convey.ShouldEqual, "3")
		// The entire injection string remains in the path segment, and the downstream will only treat it as a non-existent metric_id.
		convey.So(parsed.EscapedPath(), convey.ShouldEqual,
			"/api/ontology-query/in/v1/knowledge-networks/kn1/object-metrics/m1%2Fdata%3Fbranch=dev&x=/data")
		convey.So(strings.Count(got, "?"), convey.ShouldEqual, 1)
	})
}

func TestDrillThroughMetricData_ForwardsPinnedBucketAndCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	httpClient := mocks.NewMockHTTPClient(ctrl)
	client := &ontologyQueryClient{
		logger: logger, baseURL: "http://ontology-query:13018/api/ontology-query", httpClient: httpClient,
	}
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, _ map[string]string, body any) (int, []byte, error) {
			parsed, err := url.Parse(target)
			if err != nil || parsed.Query().Get("version") != "2" || parsed.Query().Get("branch") != "release" ||
				!strings.HasSuffix(parsed.EscapedPath(), "/object-metrics/metric-1/data/drill-through") {
				t.Fatalf("unexpected drill-through URL: %s (%v)", target, err)
			}
			encoded, err := json.Marshal(body)
			if err != nil || !strings.Contains(string(encoded), `"bucket_key":"opaque"`) ||
				!strings.Contains(string(encoded), `"cursor":"page-2"`) ||
				!strings.Contains(string(encoded), `"page_size":25`) {
				t.Fatalf("unexpected drill-through body: %s (%v)", encoded, err)
			}
			return 200, []byte(`{"participants":[{"instance_identity":{"id":"row-1"},"role":"owner"}],"status":"ok","evaluated_at":123}`), nil
		})
	result, err := client.DrillThroughMetricData(context.Background(), "kn-1", "release", "metric-1", 2,
		&interfaces.MetricDrillThroughReq{BucketKey: "opaque", PageSize: 25, Cursor: "page-2"})
	if err != nil || len(result.Participants) != 1 || result.Participants[0].InstanceIdentity["id"] != "row-1" {
		t.Fatalf("drill-through response = %#v, %v", result, err)
	}
}

func TestDrillThroughMetricData_PreservesUnavailableParticipantsAsUnprocessable(t *testing.T) {
	client, _ := ontologyQueryServer(t, http.StatusUnprocessableEntity,
		`{"error_code":"OntologyQuery.Metric.InvalidParameter",`+
			`"description":"请求参数不合法",`+
			`"error_details":"participant lookup is unavailable for this result"}`)

	_, err := client.DrillThroughMetricData(context.Background(), "kn-1", "main", "metric-1", 1,
		&interfaces.MetricDrillThroughReq{BucketKey: "no-data-bucket"})

	he := asHTTPError(t, err)
	if he.HTTPCode != http.StatusUnprocessableEntity || he.ErrorCode != "Public.BadRequest" {
		t.Fatalf("error = %d %s, want 422 Public.BadRequest", he.HTTPCode, he.ErrorCode)
	}
	want := "OntologyQuery.Metric.InvalidParameter: participant lookup is unavailable for this result"
	if he.ErrorDetails != want {
		t.Fatalf("details = %#v, want %q", he.ErrorDetails, want)
	}
}

func TestQueryMetricData_ForwardsVersion(t *testing.T) {
	convey.Convey("version 走查询串透传", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockLogger := mocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
		mockHTTP := mocks.NewMockHTTPClient(ctrl)
		client := &ontologyQueryClient{
			logger:     mockLogger,
			baseURL:    "http://ontology-query:13018/api/ontology-query",
			httpClient: mockHTTP,
		}

		var got string
		mockHTTP.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, target string, _ map[string]string, _ any) (int, []byte, error) {
				got = target
				return 200, jsonBytes(map[string]any{"entries": []any{}, "status": "no_data"}), nil
			})

		_, err := client.QueryMetricData(context.Background(), "kn1", "", "m1", 2, nil)
		convey.So(err, convey.ShouldBeNil)
		convey.So(got, convey.ShouldEndWith,
			"/in/v1/knowledge-networks/kn1/object-metrics/m1/data?branch=main&version=2")
	})
}

func TestQueryMetricData_ForwardsFinalResultOperationsAndPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	httpClient := mocks.NewMockHTTPClient(ctrl)
	client := &ontologyQueryClient{logger: logger, baseURL: "http://ontology-query:13018/api/ontology-query", httpClient: httpClient}
	httpClient.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body any) (int, []byte, error) {
			encoded, err := json.Marshal(body)
			if err != nil || !strings.Contains(string(encoded), `"group_by"`) ||
				!strings.Contains(string(encoded), `"having":{"operator":"gte","value":10}`) ||
				!strings.Contains(string(encoded), `"order_by":[{"field":"value","direction":"desc"}]`) ||
				!strings.Contains(string(encoded), `"page_size":2`) {
				t.Fatalf("metric query lost final-result operations: %s (%v)", encoded, err)
			}
			return 200, []byte(`{"entries":[],"status":"no_data","empty_reason":"filtered_out","total_count":7,"next_cursor":"next"}`), nil
		})
	result, err := client.QueryMetricData(context.Background(), "kn-1", "release", "metric-1", 2, &interfaces.ObjectMetricQuery{
		GroupBy:  []interfaces.ObjectMetricPropertyRef{{ObjectTypeID: "order", PropertyName: "region"}},
		Having:   &interfaces.ObjectMetricHaving{Operator: "gte", Value: 10},
		OrderBy:  []interfaces.ObjectMetricOrderBy{{Field: "value", Direction: "desc"}},
		PageSize: 2,
	})
	if err != nil || result.EmptyReason != "filtered_out" || result.TotalCount != 7 || result.NextCursor != "next" {
		t.Fatalf("metric result metadata was lost: %#v, %v", result, err)
	}
}

func TestQueryMetricData_PreservesExplicitEmptyStrings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Filter struct {
				Left  map[string]json.RawMessage   `json:"left"`
				Right []map[string]json.RawMessage `json:"right"`
			} `json:"filter"`
			Having map[string]json.RawMessage `json:"having"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode forwarded metric request: %v", err)
		} else {
			if len(request.Filter.Right) != 1 || string(request.Filter.Right[0]["value"]) != `""` {
				t.Errorf("forwarded right operands = %s, want an explicit empty string literal", request.Filter.Right)
			}
			if _, present := request.Filter.Left["value"]; present {
				t.Errorf("property operand unexpectedly carries a literal: %s", request.Filter.Left["value"])
			}
			if string(request.Having["value"]) != `""` {
				t.Errorf("forwarded having = %s, want an explicit empty string", request.Having)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"value":"","status":"ok"}],"status":"ok"}`))
	}))
	defer server.Close()

	client := &ontologyQueryClient{
		baseURL:    server.URL,
		httpClient: rest.NewHTTPClientWithRawClient(server.Client()),
	}
	query := &interfaces.ObjectMetricQuery{
		Filter: &interfaces.ObjectMetricCondition{
			NodeType:           "predicate",
			ComparisonOperator: "eq",
			Left: &interfaces.ObjectMetricOperand{
				Source:      "property",
				PropertyRef: &interfaces.ObjectMetricPropertyRef{ObjectTypeID: "order", PropertyName: "region"},
			},
			Right: []interfaces.ObjectMetricOperand{{Source: "literal", ValueType: "string", Value: ""}},
		},
		Having: &interfaces.ObjectMetricHaving{Operator: "eq", Value: ""},
	}
	response, err := client.QueryMetricData(t.Context(), "kn-1", "main", "metric-1", 1, query)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := rest.MarshalResponse(rest.FormatJSON, response)
	if err != nil || !strings.Contains(string(body), `"value":""`) {
		t.Fatalf("metric result = %s, %v, want an explicit empty string value", body, err)
	}
}
