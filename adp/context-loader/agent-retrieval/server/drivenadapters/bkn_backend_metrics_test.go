// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
)

func TestSearchMetricTypes_UsesV1PublishedCatalogue(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	response := []byte(`{"entries":[
        {"definition":{"id":"m1","version":2,"name":"Order count","owner_object_type_id":"orders","lifecycle":{"status":"published"}}},
        {"definition":{"id":"m2","version":1,"name":"Customer count","owner_object_type_id":"customers","lifecycle":{"status":"published"}}},
        {"definition":{"id":"m3","version":1,"name":"Order draft","owner_object_type_id":"orders","lifecycle":{"status":"draft"}}}
    ],"total_count":3}`)
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, src string, query url.Values, _ map[string]string) (int, []byte, error) {
			if !strings.HasSuffix(src, "/kn1/object-metrics") || query.Get("status") != "published" {
				t.Fatalf("src=%s query=%v", src, query)
			}
			return http.StatusOK, response, nil
		}).Times(2)
	direct, err := client.SearchMetricTypes(context.Background(), &interfaces.QueryConceptsReq{
		KnID: "kn1", Cond: &interfaces.KnCondition{Field: "*", Operation: interfaces.KnOperationTypeMatch, Value: "order"},
	})
	if err != nil || len(direct.Entries) != 1 || direct.Entries[0].ID != "m1" || direct.Entries[0].Version != 2 {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}
	expanded, err := client.SearchMetricTypes(context.Background(), &interfaces.QueryConceptsReq{
		KnID: "kn1", Cond: &interfaces.KnCondition{Field: "scope_ref", Operation: interfaces.KnOperationTypeIn, Value: []string{"customers"}},
	})
	if err != nil || len(expanded.Entries) != 1 || expanded.Entries[0].ID != "m2" {
		t.Fatalf("expanded=%+v err=%v", expanded, err)
	}
}

func TestMetricTextMatches_NormalizesMetricNames(t *testing.T) {
	if !metricTextMatches("CPU_usage", "cpu usage") || metricTextMatches("CPU_usage", "memory usage") {
		t.Fatal("metric token matching failed")
	}
	if !metricTextMatches("订单总数", "请查询订单总数") || metricTextMatches("客户满意度", "请查询订单总数") {
		t.Fatal("metric Chinese sentence recall failed")
	}
}

func TestSearchMetricTypes_RestrictsConceptGroups(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, src string, _ url.Values, _ map[string]string) (int, []byte, error) {
			if !strings.HasSuffix(src, "/object-metrics") {
				t.Fatalf("unexpected URL %s", src)
			}
			return http.StatusOK, []byte(`{"entries":[{"definition":{"id":"m1","version":1,"name":"Count","owner_object_type_id":"orders","lifecycle":{"status":"published"}}},{"definition":{"id":"m2","version":1,"name":"Count","owner_object_type_id":"private","lifecycle":{"status":"published"}}}],"total_count":2}`), nil
		})
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, src string, query url.Values, _ map[string]string) (int, []byte, error) {
			if !strings.HasSuffix(src, "/concept-groups") || query.Get("limit") != "-1" {
				t.Fatalf("src=%s query=%v", src, query)
			}
			return http.StatusOK, []byte(`{"entries":[{"id":"sales","object_type_ids":["orders"]},{"id":"restricted","object_type_ids":["private"]}]}`), nil
		})
	metrics, err := client.SearchMetricTypes(context.Background(), &interfaces.QueryConceptsReq{
		KnID: "kn1", ConceptGroups: []string{"sales"},
	})
	if err != nil || len(metrics.Entries) != 1 || metrics.Entries[0].ID != "m1" {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
}

func newMetricsTestClient(t *testing.T) (*bknBackendAccess, *mocks.MockHTTPClient, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockLogger := mocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithContext(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockHTTP := mocks.NewMockHTTPClient(ctrl)
	return &bknBackendAccess{logger: mockLogger, baseURL: "http://localhost:8080/api/bkn-backend", httpClient: mockHTTP}, mockHTTP, ctrl
}

func TestListMetricsByObjectTypes_UsesPublishedV1Definitions(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	var gotURL string
	var gotQuery url.Values
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, src string, q url.Values, _ map[string]string) (int, []byte, error) {
			gotURL, gotQuery = src, q
			return http.StatusOK, []byte(`{"entries":[
              {"definition":{"id":"m1","version":2,"name":"Products","description":"Count","metric_type":"atomic","owner_object_type_id":"ot1","lifecycle":{"status":"published"},"result_semantics":{"value":{"unit":{"code":"pcs","quantity_kind":"count"}}},"query_capabilities":{"analysis_dimensions":[{"object_type_id":"ot1","property_name":"region"}],"time_dimensions":[{"object_type_id":"ot1","property_name":"date"}]}}},
              {"definition":{"id":"m2","version":1,"name":"Other","owner_object_type_id":"ot2","lifecycle":{"status":"published"}}},
              {"definition":{"id":"m3","version":1,"name":"Draft","owner_object_type_id":"ot1","lifecycle":{"status":"draft"}}}
            ],"total_count":3}`), nil
		})
	metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1", " ot2 ", "ot1"})
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "http://localhost:8080/api/bkn-backend/in/v1/knowledge-networks/kn1/object-metrics" {
		t.Fatalf("URL = %s", gotURL)
	}
	if gotQuery.Get("status") != "published" || gotQuery.Get("limit") != "500" {
		t.Fatalf("query = %v", gotQuery)
	}
	if len(metrics) != 2 {
		t.Fatalf("metrics = %+v", metrics)
	}
	if metrics[0].ID != "m1" || metrics[0].Version != 2 || metrics[0].OwnerObjectTypeID != "ot1" || metrics[0].Unit != "pcs" || len(metrics[0].TimeDimensions) != 1 || metrics[0].TimeDimensions[0].PropertyName != "date" {
		t.Fatalf("metric = %+v", metrics[0])
	}
	if len(metrics[0].AnalysisDimensions) != 1 || metrics[0].AnalysisDimensions[0].ObjectTypeID != "ot1" || metrics[0].AnalysisDimensions[0].PropertyName != "region" {
		t.Fatalf("dimensions = %+v", metrics[0].AnalysisDimensions)
	}
}

func TestListMetricsByObjectTypes_RejectsOutOfScopeAndUnversioned(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{"entries":[{"definition":{"id":"wanted","version":1,"owner_object_type_id":"ot1","lifecycle":{"status":"published"}}},{"definition":{"id":"other","version":1,"owner_object_type_id":"ot9","lifecycle":{"status":"published"}}},{"definition":{"id":"unversioned","owner_object_type_id":"ot1","lifecycle":{"status":"published"}}}],"total_count":3}`), nil)
	metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})
	if err != nil || len(metrics) != 1 || metrics[0].ID != "wanted" {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
}

func TestListMetricsByObjectTypes_NoRequestWithoutIDs(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	for _, tc := range []struct {
		kn  string
		ids []string
	}{{"kn1", []string{" ", ""}}, {"", []string{"ot1"}}} {
		metrics, err := client.ListMetricsByObjectTypes(context.Background(), tc.kn, tc.ids)
		if err != nil || metrics != nil {
			t.Fatalf("metrics=%+v err=%v", metrics, err)
		}
	}
}

func TestObjectMetricDiscoveryTreatsMissingOptionalCapabilityAsEmpty(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusNotFound, []byte(`{"error_code":"RESOURCE_NOT_FOUND"}`), nil).
		Times(2)

	searched, err := client.SearchMetricTypes(context.Background(), &interfaces.QueryConceptsReq{KnID: "kn1"})
	if err != nil || searched == nil || len(searched.Entries) != 0 || searched.TotalCount != 0 {
		t.Fatalf("searched=%+v err=%v", searched, err)
	}
	attached, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})
	if err != nil || len(attached) != 0 {
		t.Fatalf("attached=%+v err=%v", attached, err)
	}
}

func TestListMetricsByObjectTypes_PagesAndDoesNotTruncate(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	first := make([]string, metricsPageSize)
	for i := range first {
		first[i] = `{"definition":{"id":"m` + strconv.Itoa(i) + `","version":1,"owner_object_type_id":"ot1","lifecycle":{"status":"published"}}}`
	}
	var offsets []string
	gomock.InOrder(
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q url.Values, _ map[string]string) (int, []byte, error) {
				offsets = append(offsets, q.Get("offset"))
				return http.StatusOK, []byte(`{"entries":[` + strings.Join(first, ",") + `],"total_count":501}`), nil
			}),
		mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, q url.Values, _ map[string]string) (int, []byte, error) {
				offsets = append(offsets, q.Get("offset"))
				return http.StatusOK, []byte(`{"entries":[{"definition":{"id":"tail","version":1,"owner_object_type_id":"ot1","lifecycle":{"status":"published"}}}],"total_count":501}`), nil
			}),
	)
	metrics, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})
	if err != nil || len(metrics) != 501 {
		t.Fatalf("metrics=%d err=%v", len(metrics), err)
	}
	if strings.Join(offsets, ",") != "0,500" {
		t.Fatalf("offsets=%v", offsets)
	}
}

func TestListMetricsByObjectTypes_FailsOnIncompleteCatalogue(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{"entries":[],"total_count":1}`), nil)
	_, err := client.ListMetricsByObjectTypes(context.Background(), "kn1", []string{"ot1"})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error=%v", err)
	}
}

func TestListMetricsByObjectTypes_EscapesKnID(t *testing.T) {
	client, mockHTTP, ctrl := newMetricsTestClient(t)
	defer ctrl.Finish()
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, src string, q url.Values, _ map[string]string) (int, []byte, error) {
			if strings.Contains(src, "?") || !strings.HasSuffix(src, "/kn1%2Fmetrics%3Fstatus=draft/object-metrics") || q.Get("status") != "published" {
				t.Fatalf("src=%s query=%v", src, q)
			}
			return http.StatusOK, []byte(`{"entries":[],"total_count":0}`), nil
		})
	_, err := client.ListMetricsByObjectTypes(context.Background(), "kn1/metrics?status=draft", []string{"ot1"})
	if err != nil {
		t.Fatal(err)
	}
}
