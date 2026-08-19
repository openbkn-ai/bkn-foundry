// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// stubMetricBknBackend serves one knowledge-network detail and the metrics scoped
// to its object types.
type stubMetricBknBackend struct {
	interfaces.BknBackendAccess
	detail  *interfaces.KnowledgeNetworkDetail
	metrics []*interfaces.RelatedMetric
}

func (s *stubMetricBknBackend) GetKnowledgeNetworkDetail(_ context.Context, _ string) (*interfaces.KnowledgeNetworkDetail, error) {
	return s.detail, nil
}

func (s *stubMetricBknBackend) ListMetricsByObjectTypes(_ context.Context, _ string, _ []string) ([]*interfaces.RelatedMetric, error) {
	return s.metrics, nil
}

// GetObjectTypeDetail returns details by id, simulating the enriched endpoint of BKN: get_object_types changes it.
// After that, if the pile is not implemented, it will be a null pointer.
func (s *stubMetricBknBackend) GetObjectTypeDetail(_ context.Context, _ string, ids []string, _ bool) ([]*interfaces.ObjectType, error) {
	if s.detail == nil {
		return nil, nil
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}

	var matched []*interfaces.ObjectType
	for _, ot := range s.detail.ObjectTypes {
		if ot == nil {
			continue
		}
		if _, ok := wanted[ot.ID]; ok {
			matched = append(matched, ot)
		}
	}
	return matched, nil
}

type stubMetricOntologyQuery struct {
	interfaces.DrivenOntologyQuery
	resp      *interfaces.MetricQueryDownstreamResp
	gotMetric string
}

func (s *stubMetricOntologyQuery) QueryMetricData(_ context.Context, _, metricID string, _ bool,
	_ *interfaces.MetricQueryDownstreamReq) (*interfaces.MetricQueryDownstreamResp, error) {
	s.gotMetric = metricID
	return s.resp, nil
}

func TestHandleGetObjectTypes_AdvertisesScopedMetrics(t *testing.T) {
	convey.Convey("get_object_types 带出该对象类下的指标（含未绑逻辑属性的）", t, func() {
		bkn := &stubMetricBknBackend{
			detail: &interfaces.KnowledgeNetworkDetail{
				ID:          "kn-001",
				ObjectTypes: []*interfaces.ObjectType{{ID: "ot-001", Name: "产品"}},
			},
			metrics: []*interfaces.RelatedMetric{
				{ID: "m-001", Name: "产品总数", ScopeRef: "ot-001", MetricType: "atomic"},
			},
		}
		handler := handleGetObjectTypes(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil))

		result, err := handler(context.Background(), mcpReq(map[string]any{
			"kn_id":           "kn-001",
			"ids":             []any{"ot-001"},
			"response_format": "json",
		}))

		convey.So(err, convey.ShouldBeNil)
		convey.So(result.IsError, convey.ShouldBeFalse)

		m := resultToMap(t, result)
		objectTypes := m["object_types"].([]any)
		first := objectTypes[0].(map[string]any)
		related := first["related_metrics"].([]any)
		convey.So(len(related), convey.ShouldEqual, 1)
		convey.So(related[0].(map[string]any)["id"], convey.ShouldEqual, "m-001")
	})
}

func TestHandleQueryMetric(t *testing.T) {
	convey.Convey("query_metric 按 metric_id 取数", t, func() {
		oq := &stubMetricOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{
			Datas: []*interfaces.MetricDataSeries{{Values: []any{431}}},
		}}
		handler := handleQueryMetric(knmetrics.NewKnMetricsServiceWith(nil, nil, oq))

		result, err := handler(context.Background(), mcpReq(map[string]any{
			"kn_id":           "kn-001",
			"metric_id":       "m-001",
			"response_format": "json",
		}))

		convey.So(err, convey.ShouldBeNil)
		convey.So(result.IsError, convey.ShouldBeFalse)
		convey.So(oq.gotMetric, convey.ShouldEqual, "m-001")

		m := resultToMap(t, result)
		convey.So(m["metric_id"], convey.ShouldEqual, "m-001")
		convey.So(len(m["datas"].([]any)), convey.ShouldEqual, 1)
	})

	convey.Convey("缺 metric_id 回错误而不是打下游", t, func() {
		oq := &stubMetricOntologyQuery{}
		handler := handleQueryMetric(knmetrics.NewKnMetricsServiceWith(nil, nil, oq))

		result, err := handler(context.Background(), mcpReq(map[string]any{"kn_id": "kn-001"}))

		convey.So(err, convey.ShouldBeNil)
		convey.So(result.IsError, convey.ShouldBeTrue)
		convey.So(oq.gotMetric, convey.ShouldBeEmpty)
	})
}

// get_object_types must go to the enriched endpoint to get details by id: the exported view does not have condition_operations,
// The caller (Agent) determines whether the field can match / knn based on this. If it gets empty, it can only rely on guessing.
func TestHandleGetObjectTypes_UsesEnrichedEndpoint(t *testing.T) {
	convey.Convey("Test get_object_types reads the enriched detail endpoint", t, func() {
		stub := &capsBknBackend{
			ops: []interfaces.KnOperationType{
				interfaces.KnOperationTypeEqual,
				interfaces.KnOperationTypeMatch,
				interfaces.KnOperationTypeKnn,
			},
		}

		handler := handleGetObjectTypes(stub, knmetrics.NewKnMetricsServiceWith(nil, stub, nil))
		req := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
			Arguments: map[string]any{
				"kn_id": "kn1",
				"ids":   []any{"stadiums"},
			},
		}}

		result, err := handler(context.Background(), req)

		convey.So(err, convey.ShouldBeNil)
		convey.So(stub.detailCalls, convey.ShouldEqual, 1)
		convey.So(stub.exportCalls, convey.ShouldEqual, 0)
		convey.So(fmt.Sprintf("%v", result), convey.ShouldContainSubstring, "knn")
	})
}

// capsBknBackend only returns an object type with an operator, and records the number of times the two access paths are called.
type capsBknBackend struct {
	interfaces.BknBackendAccess
	ops         []interfaces.KnOperationType
	detailCalls int
	exportCalls int
}

func (s *capsBknBackend) GetObjectTypeDetail(_ context.Context, _ string, _ []string, _ bool) ([]*interfaces.ObjectType, error) {
	s.detailCalls++
	return []*interfaces.ObjectType{{
		ID:   "stadiums",
		Name: "球场",
		DataProperties: []*interfaces.DataProperty{
			{Name: "stadium_name", Type: "string", ConditionOperations: s.ops},
		},
	}}, nil
}

func (s *capsBknBackend) GetKnowledgeNetworkDetail(_ context.Context, _ string) (*interfaces.KnowledgeNetworkDetail, error) {
	s.exportCalls++
	return &interfaces.KnowledgeNetworkDetail{}, nil
}

func (s *capsBknBackend) ListMetricsByObjectTypes(_ context.Context, _ string, _ []string) ([]*interfaces.RelatedMetric, error) {
	return nil, nil
}
