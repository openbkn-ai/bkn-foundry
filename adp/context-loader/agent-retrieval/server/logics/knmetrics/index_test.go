// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knmetrics

import (
	"context"
	"errors"
	"net/http"
	"testing"

	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type stubBknBackend struct {
	interfaces.BknBackendAccess
	metrics []*interfaces.RelatedMetric
	err     error
	gotKnID string
	gotIDs  []string
	calls   int
}

func (s *stubBknBackend) ListMetricsByObjectTypes(_ context.Context, knID string, otIDs []string) ([]*interfaces.RelatedMetric, error) {
	s.calls++
	s.gotKnID, s.gotIDs = knID, otIDs
	return s.metrics, s.err
}
func (s *stubBknBackend) ListKNCapabilities(context.Context, string, string, string) ([]*interfaces.CapabilityRef, error) {
	return nil, nil
}

type stubOntologyQuery struct {
	interfaces.DrivenOntologyQuery
	resp       *interfaces.MetricQueryDownstreamResp
	drillResp  *interfaces.MetricDrillThroughResp
	err        error
	gotKnID    string
	gotMetric  string
	gotVersion int
	gotReq     *interfaces.ObjectMetricQuery
	calls      int
	drillCalls int
}

func (s *stubOntologyQuery) QueryMetricData(_ context.Context, knID, metricID string, version int, req *interfaces.ObjectMetricQuery) (*interfaces.MetricQueryDownstreamResp, error) {
	s.calls++
	s.gotKnID, s.gotMetric, s.gotVersion, s.gotReq = knID, metricID, version, req
	return s.resp, s.err
}

func (s *stubOntologyQuery) DrillThroughMetricData(_ context.Context, _, _ string, _ int, _ *interfaces.MetricDrillThroughReq) (*interfaces.MetricDrillThroughResp, error) {
	s.drillCalls++
	return s.drillResp, s.err
}

func TestAttachRelatedMetrics(t *testing.T) {
	bkn := &stubBknBackend{metrics: []*interfaces.RelatedMetric{
		{ID: "m1", Version: 2, OwnerObjectTypeID: "ot1"}, {ID: "m2", Version: 1, OwnerObjectTypeID: "ot2"}, {ID: "m3", Version: 1, OwnerObjectTypeID: "ot1"},
	}}
	svc := NewKnMetricsServiceWith(nil, bkn, nil)
	ots := []*interfaces.ObjectType{{ID: "ot1"}, {ID: "ot2"}, {ID: "ot3"}, nil}
	if err := svc.AttachRelatedMetrics(context.Background(), "kn1", ots); err != nil {
		t.Fatal(err)
	}
	if bkn.calls != 1 || len(ots[0].RelatedMetrics) != 2 || ots[0].RelatedMetricCount != 2 || ots[1].RelatedMetrics[0].Version != 1 || ots[2].RelatedMetricCount != 0 {
		t.Fatalf("unexpected metrics: %+v", ots)
	}
	if err := svc.AttachRelatedMetricCounts(context.Background(), "kn1", ots); err != nil {
		t.Fatal(err)
	}
	if ots[0].RelatedMetricCount != 2 {
		t.Fatalf("count=%d", ots[0].RelatedMetricCount)
	}
}

func TestAttachRelatedMetrics_NoObjectTypes(t *testing.T) {
	bkn := &stubBknBackend{}
	if err := NewKnMetricsServiceWith(nil, bkn, nil).AttachRelatedMetrics(context.Background(), "kn1", nil); err != nil || bkn.calls != 0 {
		t.Fatalf("calls=%d err=%v", bkn.calls, err)
	}
}

func TestAttachRelatedMetrics_PropagatesDependencyErrors(t *testing.T) {
	for _, want := range []error{errors.New("connection reset"), infraerrors.DefaultHTTPError(context.Background(), http.StatusServiceUnavailable, "safe unavailable")} {
		bkn := &stubBknBackend{err: want}
		err := NewKnMetricsServiceWith(nil, bkn, nil).AttachRelatedMetrics(context.Background(), "kn1", []*interfaces.ObjectType{{ID: "ot1"}})
		if !errors.Is(err, want) {
			t.Fatalf("error=%v want=%v", err, want)
		}
	}
}

func TestQueryMetric_PinsVersionAndPreservesV1Status(t *testing.T) {
	oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{
		Model:   map[string]any{"metric_id": "m1", "version": 2},
		Entries: []interfaces.ObjectMetricDataEntry{{Status: "ok", Value: 431}},
		Status:  "partial", Warnings: []string{"missing source"}, OverallMs: 12,
		TotalCount: 7, NextCursor: "next-page",
	}}
	query := interfaces.ObjectMetricQuery{
		InstanceIdentities: []map[string]any{{"id": "i1"}},
		GroupBy:            []interfaces.ObjectMetricPropertyRef{{ObjectTypeID: "ot1", PropertyName: "region"}},
		Having:             &interfaces.ObjectMetricHaving{Operator: "gte", Value: 100},
		OrderBy:            []interfaces.ObjectMetricOrderBy{{Field: "value", Direction: "desc"}},
		PageSize:           2,
		Time:               &interfaces.ObjectMetricTimeRange{Start: 1000, End: 2000},
	}
	resp, err := NewKnMetricsServiceWith(nil, nil, oq).QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: " kn1 ", MetricID: " m1 ", Version: 2, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	if oq.gotKnID != "kn1" || oq.gotMetric != "m1" || oq.gotVersion != 2 || oq.gotReq.Time.Start != 1000 ||
		len(oq.gotReq.GroupBy) != 1 || oq.gotReq.Having == nil || len(oq.gotReq.OrderBy) != 1 || oq.gotReq.PageSize != 2 {
		t.Fatalf("downstream: %+v", oq)
	}
	if resp.Version != 2 || resp.Status != "partial" || len(resp.Entries) != 1 || len(resp.Warnings) != 1 || resp.OverallMs != 12 || resp.TotalCount != 7 || resp.NextCursor != "next-page" {
		t.Fatalf("response: %+v", resp)
	}
}

func TestQueryMetric_RejectsInvalidInputsBeforeDownstream(t *testing.T) {
	oq := &stubOntologyQuery{}
	svc := NewKnMetricsServiceWith(nil, nil, oq)
	cases := []struct {
		req  *interfaces.QueryMetricReq
		want error
	}{
		{nil, ErrKnIDRequired},
		{&interfaces.QueryMetricReq{MetricID: "m1", Version: 1}, ErrKnIDRequired},
		{&interfaces.QueryMetricReq{KnID: "kn1", Version: 1}, ErrMetricIDRequired},
		{&interfaces.QueryMetricReq{KnID: "kn1", MetricID: "m1"}, ErrVersionRequired},
	}
	for _, tc := range cases {
		_, err := svc.QueryMetric(context.Background(), tc.req)
		if !errors.Is(err, tc.want) {
			t.Fatalf("err=%v want=%v", err, tc.want)
		}
	}
	_, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
		KnID: "kn1", MetricID: "m1", Version: 1, Query: interfaces.ObjectMetricQuery{Time: &interfaces.ObjectMetricTimeRange{Start: 2, End: 1}},
	})
	if err == nil || oq.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, oq.calls)
	}
}

func TestMetricRuntimeQueryRejectsInvalidPagingAndDrillDepthBeforeDownstream(t *testing.T) {
	tooDeep := interfaces.ObjectMetricQuery{}
	for range 9 {
		tooDeep = interfaces.ObjectMetricQuery{BucketSelection: &interfaces.ObjectMetricBucketSelection{ParentQuery: tooDeep}}
	}
	for _, tc := range []struct {
		name  string
		query interfaces.ObjectMetricQuery
	}{
		{name: "negative page size", query: interfaces.ObjectMetricQuery{PageSize: -1}},
		{name: "page size above limit", query: interfaces.ObjectMetricQuery{PageSize: 1001}},
		{name: "cursor without page size", query: interfaces.ObjectMetricQuery{Cursor: "next"}},
		{name: "empty filter", query: interfaces.ObjectMetricQuery{Filter: &interfaces.ObjectMetricCondition{}}},
		{name: "parent cursor without page size", query: interfaces.ObjectMetricQuery{BucketSelection: &interfaces.ObjectMetricBucketSelection{
			ParentQuery: interfaces.ObjectMetricQuery{Cursor: "parent-next"},
		}}},
		{name: "empty parent filter", query: interfaces.ObjectMetricQuery{BucketSelection: &interfaces.ObjectMetricBucketSelection{
			ParentQuery: interfaces.ObjectMetricQuery{Filter: &interfaces.ObjectMetricCondition{}},
		}}},
		{name: "drill depth above limit", query: tooDeep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downstream := &stubOntologyQuery{}
			service := NewKnMetricsServiceWith(nil, nil, downstream)
			_, err := service.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
				KnID: "kn1", MetricID: "m1", Version: 1, Query: tc.query,
			})
			if err == nil || downstream.calls != 0 {
				t.Fatalf("query should be rejected before downstream: err=%v calls=%d", err, downstream.calls)
			}
			_, err = service.DrillThroughMetric(context.Background(), &interfaces.MetricDrillThroughReq{
				KnID: "kn1", MetricID: "m1", Version: 1, BucketKey: "selected", Query: tc.query,
			})
			if err == nil || downstream.drillCalls != 0 {
				t.Fatalf("drill-through should be rejected before downstream: err=%v calls=%d", err, downstream.drillCalls)
			}
		})
	}
}

func TestQueryMetric_EmptyEntriesAreArray(t *testing.T) {
	oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{Status: "no_data"}}
	resp, err := NewKnMetricsServiceWith(nil, nil, oq).QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: "kn1", MetricID: "m1", Version: 1})
	if err != nil || resp.Status != "no_data" || resp.Entries == nil || len(resp.Entries) != 0 {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
}

func TestDrillThroughMetric_EmptyParticipantsAreArray(t *testing.T) {
	query := &stubOntologyQuery{drillResp: &interfaces.MetricDrillThroughResp{Status: "ok"}}
	result, err := NewKnMetricsServiceWith(nil, nil, query).DrillThroughMetric(context.Background(),
		&interfaces.MetricDrillThroughReq{KnID: "kn1", MetricID: "m1", Version: 1, BucketKey: "selected"})
	if err != nil || result.Participants == nil || len(result.Participants) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
