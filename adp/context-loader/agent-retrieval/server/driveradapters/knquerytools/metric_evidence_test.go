// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knquerytools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type metricEvidenceService struct{ knmetrics.KnMetricsService }

func (metricEvidenceService) QueryMetric(_ context.Context, req *interfaces.QueryMetricReq) (*interfaces.QueryMetricResp, error) {
	return &interfaces.QueryMetricResp{KnID: req.KnID, MetricID: req.MetricID, Version: req.Version,
		Model: map[string]any{}, Entries: []interfaces.ObjectMetricDataEntry{}, Status: "ok"}, nil
}

type metricEvidencePublisher struct{ events []evidencepublisher.Event }

func (p *metricEvidencePublisher) TryPublish(event evidencepublisher.Event) evidencepublisher.PublishResult {
	p.events = append(p.events, event)
	return evidencepublisher.PublishResult{EventID: event.EventID, Disposition: evidencepublisher.Accepted}
}

type branchListingRESTBackend struct {
	interfaces.BknBackendAccess
	interfaces.BranchBknBackendAccess
	gotBranch string
}

func (b *branchListingRESTBackend) ListObjectTypesBranch(_ context.Context, _, branch string, _, _ int) (*interfaces.ObjectTypePage, error) {
	b.gotBranch = branch
	return &interfaces.ObjectTypePage{Entries: []*interfaces.ObjectType{}}, nil
}

func TestGetObjectTypesRESTBindsRequestedBranch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := &branchListingRESTBackend{}
	handler := &knQueryToolsHandler{bknBackend: backend}
	router := gin.New()
	router.POST("/kn/get_object_types", handler.GetObjectTypes)
	request := httptest.NewRequest(http.MethodPost, "/kn/get_object_types", strings.NewReader(
		`{"kn_id":"kn-1","branch":"release"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || backend.gotBranch != "release" {
		t.Fatalf("status=%d branch=%q body=%s", response.Code, backend.gotBranch, response.Body.String())
	}
}

func TestQueryMetricRESTEmitsMetricEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publisher := &metricEvidencePublisher{}
	bkntrace.SetEvidencePublisher(publisher)
	t.Cleanup(func() { bkntrace.SetEvidencePublisher(nil) })

	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
		RequestID: "req_context_loader_phase2_0001", InteractionID: "int-metric-1",
		OperationID: "op-metric-1", ObservedAt: "2026-07-25T08:00:00Z", ObservedAtProvided: true,
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID: "acct-demo", AccountType: interfaces.AccessorTypeUser,
	})

	handler := &knQueryToolsHandler{metrics: metricEvidenceService{}}
	router := gin.New()
	router.POST("/kn/query_metric", handler.QueryMetric)
	request := httptest.NewRequest(http.MethodPost, "/kn/query_metric", strings.NewReader(
		`{"kn_id":"kn-1","branch":"release","metric_id":"m-1","version":2,"query":{}}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(publisher.events) != 1 || publisher.events[0].EventType != "metric.query.completed" {
		t.Fatalf("status=%d events=%+v body=%s", response.Code, publisher.events, response.Body.String())
	}
	var event struct {
		Payload struct {
			Branch string `json:"branch"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(publisher.events[0].Envelope, &event); err != nil || event.Payload.Branch != "release" {
		t.Fatalf("metric evidence branch=%q err=%v", event.Payload.Branch, err)
	}
}
