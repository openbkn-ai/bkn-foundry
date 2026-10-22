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
	detail       *interfaces.KnowledgeNetworkDetail
	metrics      []*interfaces.RelatedMetric
	capabilities []*interfaces.CapabilityRef
	capErr       error
}

func (s *stubMetricBknBackend) ListKNCapabilities(_ context.Context, _, _, _ string) ([]*interfaces.CapabilityRef, error) {
	return s.capabilities, s.capErr
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

func TestSchemaSnapshotDefinitionIDsUseReturnedCanonicalIDs(t *testing.T) {
	objects := []*interfaces.ObjectType{{ID: "product", Name: "产品:主数据"}, {ID: "inventory", Name: "库存"}, nil}
	if got := schemaObjectTypeIDs(objects); len(got) != 2 || got[0] != "product" || got[1] != "inventory" {
		t.Fatalf("object schema refs must use returned IDs, got %#v", got)
	}
	relations := []*interfaces.RelationType{{ID: "uses_material", Name: "使用:物料"}, nil}
	if got := schemaRelationTypeIDs(relations); len(got) != 1 || got[0] != "uses_material" {
		t.Fatalf("relation schema refs must use returned IDs, got %#v", got)
	}
}

type stubMetricOntologyQuery struct {
	interfaces.DrivenOntologyQuery
	resp           *interfaces.MetricQueryDownstreamResp
	drillResp      *interfaces.MetricDrillThroughResp
	gotMetric      string
	gotDrillMetric string
	gotDrillReq    *interfaces.MetricDrillThroughReq
}

func (s *stubMetricOntologyQuery) QueryMetricData(_ context.Context, _, metricID string, _ int,
	_ *interfaces.ObjectMetricQuery) (*interfaces.MetricQueryDownstreamResp, error) {
	s.gotMetric = metricID
	return s.resp, nil
}

func (s *stubMetricOntologyQuery) DrillThroughMetricData(_ context.Context, _, metricID string, _ int,
	req *interfaces.MetricDrillThroughReq) (*interfaces.MetricDrillThroughResp, error) {
	s.gotDrillMetric = metricID
	s.gotDrillReq = req
	return s.drillResp, nil
}

func TestHandleGetObjectTypes_AdvertisesScopedMetrics(t *testing.T) {
	convey.Convey("get_object_types 带出该对象类下的指标（含未绑逻辑属性的）", t, func() {
		bkn := &stubMetricBknBackend{
			detail: &interfaces.KnowledgeNetworkDetail{
				ID:          "kn-001",
				ObjectTypes: []*interfaces.ObjectType{{ID: "ot-001", Name: "产品"}},
			},
			metrics: []*interfaces.RelatedMetric{
				{ID: "m-001", Version: 1, Name: "产品总数", OwnerObjectTypeID: "ot-001", MetricType: "atomic"},
			},
		}
		handler := handleGetObjectTypes(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), &mcpObjectSchemaAccessStub{})

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
		convey.So(related[0].(map[string]any)["version"], convey.ShouldEqual, float64(1))
	})
}

func TestHandleQueryMetric(t *testing.T) {
	convey.Convey("query_metric 按 metric_id 取数", t, func() {
		oq := &stubMetricOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{
			Entries: []interfaces.ObjectMetricDataEntry{{Status: "ok", Value: 431}},
			Status:  "ok",
		}}
		handler := handleQueryMetric(knmetrics.NewKnMetricsServiceWith(nil, nil, oq))

		result, err := handler(context.Background(), mcpReq(map[string]any{
			"kn_id":           "kn-001",
			"metric_id":       "m-001",
			"version":         1,
			"response_format": "json",
		}))

		convey.So(err, convey.ShouldBeNil)
		convey.So(result.IsError, convey.ShouldBeFalse)
		convey.So(oq.gotMetric, convey.ShouldEqual, "m-001")

		m := resultToMap(t, result)
		convey.So(m["metric_id"], convey.ShouldEqual, "m-001")
		convey.So(len(m["entries"].([]any)), convey.ShouldEqual, 1)
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

func TestHandleDrillThroughMetric(t *testing.T) {
	oq := &stubMetricOntologyQuery{drillResp: &interfaces.MetricDrillThroughResp{
		Participants: []interfaces.ObjectMetricParticipant{{InstanceIdentity: map[string]any{"id": "order-1"}, Role: "owner"}},
		Status:       "ok", EvaluatedAt: 123,
	}}
	handler := handleDrillThroughMetric(knmetrics.NewKnMetricsServiceWith(nil, nil, oq))
	result, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "metric_id": "m-001", "version": 2,
		"query":      map[string]any{"group_by": []any{map[string]any{"object_type_id": "order", "property_name": "status"}}},
		"bucket_key": "opaque-key", "page_size": 25, "response_format": "json",
	}))
	if err != nil || result.IsError {
		t.Fatalf("drill-through failed: %v, %#v", err, result)
	}
	if oq.gotDrillMetric != "m-001" || oq.gotDrillReq.BucketKey != "opaque-key" || oq.gotDrillReq.PageSize != 25 || len(oq.gotDrillReq.Query.GroupBy) != 1 {
		t.Fatalf("drill-through request lost its selection: %#v", oq.gotDrillReq)
	}
	got := resultToMap(t, result)
	if got["metric_id"] != "m-001" || len(got["participants"].([]any)) != 1 {
		t.Fatalf("unexpected drill-through result: %#v", got)
	}

	invalid, err := handler(context.Background(), mcpReq(map[string]any{
		"kn_id": "kn-001", "metric_id": "m-001", "version": 2,
		"bucket_key": "opaque-key", "page_size": 101,
	}))
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid page size must fail: %v, %#v", err, invalid)
	}
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

		handler := handleGetObjectTypes(stub, knmetrics.NewKnMetricsServiceWith(nil, stub, nil), &mcpObjectSchemaAccessStub{
			permissions: map[string]interfaces.PropertyAccessLevel{"stadium_name": interfaces.PropertyAccessFull},
		})
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

func TestHandleGetObjectTypes_AuthorizationDoesNotFallbackOrReportDeniedIDs(t *testing.T) {
	stub := &capsBknBackend{}
	handler := handleGetObjectTypes(stub, knmetrics.NewKnMetricsServiceWith(nil, stub, nil), &mcpObjectSchemaAccessStub{
		permissions: map[string]interfaces.PropertyAccessLevel{"stadium_name": interfaces.PropertyAccessFull},
	})
	req := mcpsdk.CallToolRequest{Params: mcpsdk.CallToolParams{
		Arguments: map[string]any{
			"kn_id":           "kn1",
			"ids":             []any{"stadiums", "denied"},
			"response_format": "json",
		},
	}}

	result, err := handler(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("unexpected result: err=%v result=%v", err, result)
	}
	if stub.detailCalls != 1 || stub.exportCalls != 0 {
		t.Fatalf("authorized path must use only the typed endpoint: detail=%d export=%d", stub.detailCalls, stub.exportCalls)
	}
	response := resultToMap(t, result)
	if _, exists := response["missing"]; exists {
		t.Fatalf("filtered IDs must not appear in missing diagnostics: %#v", response["missing"])
	}
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
		ID:         "stadiums",
		Name:       "球场",
		DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "stadiums-view"},
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
