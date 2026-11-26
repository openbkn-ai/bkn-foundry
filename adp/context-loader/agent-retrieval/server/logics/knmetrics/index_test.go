// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knmetrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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

type branchMetricBackend struct {
	*stubBknBackend
	interfaces.BranchBknBackendAccess
	branch string
}

func (s *branchMetricBackend) ListMetricsByObjectTypesBranch(_ context.Context, _ string, branch string, _ []string) ([]*interfaces.RelatedMetric, error) {
	s.branch = branch
	return s.metrics, s.err
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
	gotBranch  string
	gotMetric  string
	gotVersion int
	gotReq     *interfaces.ObjectMetricQuery
	calls      int
	drillCalls int
}

func (s *stubOntologyQuery) QueryMetricData(_ context.Context, knID, branch, metricID string, version int, req *interfaces.ObjectMetricQuery) (*interfaces.MetricQueryDownstreamResp, error) {
	s.calls++
	s.gotKnID, s.gotBranch, s.gotMetric, s.gotVersion, s.gotReq = knID, branch, metricID, version, req
	return s.resp, s.err
}

func (s *stubOntologyQuery) DrillThroughMetricData(_ context.Context, _, branch, _ string, _ int, _ *interfaces.MetricDrillThroughReq) (*interfaces.MetricDrillThroughResp, error) {
	s.drillCalls++
	s.gotBranch = branch
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

func TestAttachRelatedMetricsUsesRequestedBranchAndRejectsUnsupportedReader(t *testing.T) {
	backend := &branchMetricBackend{stubBknBackend: &stubBknBackend{metrics: []*interfaces.RelatedMetric{{ID: "release-only", OwnerObjectTypeID: "orders"}}}}
	objects := []*interfaces.ObjectType{{ID: "orders"}}
	if err := NewKnMetricsServiceWith(nil, backend, nil).AttachRelatedMetrics(context.Background(), "kn1", objects, "release"); err != nil {
		t.Fatal(err)
	}
	if backend.branch != "release" || len(objects[0].RelatedMetrics) != 1 {
		t.Fatalf("branch=%q metrics=%+v", backend.branch, objects[0].RelatedMetrics)
	}
	if err := NewKnMetricsServiceWith(nil, &stubBknBackend{}, nil).AttachRelatedMetrics(context.Background(), "kn1", objects, "release"); err == nil {
		t.Fatal("non-main discovery fell back to a main-only reader")
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
		Model:         map[string]any{"metric_id": "m1", "version": 2},
		IsApproximate: true,
		Entries:       []interfaces.ObjectMetricDataEntry{{Status: "ok", Value: 431}},
		Status:        "partial", Warnings: []string{"missing source"}, OverallMs: 12,
		TotalCount: 7, NextCursor: "next-page",
	}}
	query := interfaces.ObjectMetricQuery{
		GroupBy:  []interfaces.ObjectMetricPropertyRef{{ObjectTypeID: "ot1", PropertyName: "region"}},
		Having:   &interfaces.ObjectMetricHaving{Operator: "gte", Value: 100},
		OrderBy:  []interfaces.ObjectMetricOrderBy{{Field: "value", Direction: "desc"}},
		PageSize: 2,
		Time:     &interfaces.ObjectMetricTimeRange{Start: 1000, End: 2000},
	}
	resp, err := NewKnMetricsServiceWith(nil, nil, oq).QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: " kn1 ", Branch: " release ", MetricID: " m1 ", Version: 2, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	if oq.gotKnID != "kn1" || oq.gotBranch != "release" || oq.gotMetric != "m1" || oq.gotVersion != 2 || oq.gotReq.Time.Start != 1000 ||
		len(oq.gotReq.GroupBy) != 1 || oq.gotReq.Having == nil || len(oq.gotReq.OrderBy) != 1 || oq.gotReq.PageSize != 2 {
		t.Fatalf("downstream: %+v", oq)
	}
	if resp.Version != 2 || resp.Status != "partial" || len(resp.Entries) != 1 || len(resp.Warnings) != 1 || resp.OverallMs != 12 || resp.TotalCount != 7 || resp.NextCursor != "next-page" {
		t.Fatalf("response: %+v", resp)
	}
	if !resp.IsApproximate {
		t.Fatalf("approximation metadata was lost: %+v", resp)
	}
}

func TestQueryAndDrillThroughMetric_DefaultBranchToMain(t *testing.T) {
	oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{Status: "ok"},
		drillResp: &interfaces.MetricDrillThroughResp{Status: "ok"}}
	service := NewKnMetricsServiceWith(nil, nil, oq)
	if _, err := service.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
		KnID: "kn1", MetricID: "m1", Version: 1,
	}); err != nil || oq.gotBranch != defaultBranch {
		t.Fatalf("query branch=%q err=%v", oq.gotBranch, err)
	}
	if _, err := service.DrillThroughMetric(context.Background(), &interfaces.MetricDrillThroughReq{
		KnID: "kn1", MetricID: "m1", Version: 1, BucketKey: "bucket",
	}); err != nil || oq.gotBranch != defaultBranch {
		t.Fatalf("drill-through branch=%q err=%v", oq.gotBranch, err)
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

// definitionBackend answers by-id reads the way bkn-backend does: when any id is gone, the
// whole read is 404 and returns nothing.
type definitionBackend struct {
	interfaces.BknBackendAccess
	definitions map[string]*interfaces.ObjectType
	reads       [][]string
	err         error
	// failSingleRead fails a one-id read of this id with err instead of answering it.
	failSingleRead string
}

func (b *definitionBackend) GetObjectTypeDetail(ctx context.Context, _ string, ids []string, _ bool) ([]*interfaces.ObjectType, error) {
	b.reads = append(b.reads, append([]string(nil), ids...))
	if b.err != nil && (b.failSingleRead == "" || (len(ids) == 1 && ids[0] == b.failSingleRead)) {
		return nil, b.err
	}
	var found []*interfaces.ObjectType
	for _, id := range ids {
		definition, ok := b.definitions[id]
		if !ok {
			return nil, infraerrors.DefaultHTTPError(ctx, http.StatusNotFound, "object type not found")
		}
		found = append(found, definition)
	}
	return found, nil
}

func definitionsFor(listed []*interfaces.ObjectType, missing ...string) map[string]*interfaces.ObjectType {
	definitions := make(map[string]*interfaces.ObjectType, len(listed))
	for _, objectType := range listed {
		definitions[objectType.ID] = &interfaces.ObjectType{ID: objectType.ID, Name: objectType.Name,
			DataProperties: []*interfaces.DataProperty{{Name: "id", Type: "string"}}, PrimaryKeys: []string{"id"}}
	}
	for _, id := range missing {
		delete(definitions, id)
	}
	return definitions
}

func listedSummaries(n int) []*interfaces.ObjectType {
	listed := make([]*interfaces.ObjectType, 0, n)
	for i := range n {
		id := fmt.Sprintf("ot-%03d", i)
		listed = append(listed, &interfaces.ObjectType{ID: id, Name: id})
	}
	return listed
}

// The listing's summaries are replaced by the by-id definitions, in the listing's order and in
// batches of 50 ids (#3412).
func TestWithObjectTypeDefinitionsReadsDefinitionsInBatches(t *testing.T) {
	listed := listedSummaries(120)
	backend := &definitionBackend{definitions: definitionsFor(listed)}
	listed = append(listed, nil)

	got, err := WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main", listed)
	if err != nil {
		t.Fatal(err)
	}
	if sizes := []int{len(backend.reads[0]), len(backend.reads[1]), len(backend.reads[2])}; len(backend.reads) != 3 ||
		!reflect.DeepEqual(sizes, []int{50, 50, 20}) {
		t.Fatalf("reads = %d of sizes %v, want 50, 50, 20", len(backend.reads), sizes)
	}
	if len(got) != 120 {
		t.Fatalf("got %d object types, want the 120 listed", len(got))
	}
	for i, objectType := range got {
		if want := fmt.Sprintf("ot-%03d", i); objectType.ID != want {
			t.Fatalf("entry %d = %s, want %s in the listing's order", i, objectType.ID, want)
		}
		if len(objectType.DataProperties) != 1 || len(objectType.PrimaryKeys) != 1 {
			t.Fatalf("%s carries no definition: %+v", objectType.ID, objectType)
		}
	}
}

// An object type deleted between the listing and the by-id read makes bkn-backend answer its
// whole batch 404. get_kn_detail must still answer: that batch is read one id at a time, the
// deleted one keeps its summary, and every other object type gets its definition.
func TestWithObjectTypeDefinitionsKeepsTheSummaryOfADeletedObjectType(t *testing.T) {
	listed := listedSummaries(60)
	backend := &definitionBackend{definitions: definitionsFor(listed, "ot-007")}

	got, err := WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main", listed)
	if err != nil {
		t.Fatalf("a deleted object type failed the whole detail: %v", err)
	}
	if len(got) != 60 {
		t.Fatalf("got %d object types, want the 60 listed", len(got))
	}
	for i, objectType := range got {
		if want := fmt.Sprintf("ot-%03d", i); objectType.ID != want {
			t.Fatalf("entry %d = %s, want %s in the listing's order", i, objectType.ID, want)
		}
		if i == 7 {
			if objectType != listed[7] {
				t.Fatalf("the deleted object type must keep its summary, got %+v", objectType)
			}
			continue
		}
		if len(objectType.DataProperties) != 1 || len(objectType.PrimaryKeys) != 1 {
			t.Fatalf("%s carries no definition: %+v", objectType.ID, objectType)
		}
	}
	// The failed batch of 50, its 50 one-id reads, then the second batch of 10 in one read.
	if len(backend.reads) != 52 || len(backend.reads[0]) != 50 || len(backend.reads[51]) != 10 {
		t.Fatalf("reads = %d, want 1 batch + 50 single + 1 batch", len(backend.reads))
	}
	for _, read := range backend.reads[1:51] {
		if len(read) != 1 {
			t.Fatalf("the 404 batch must be read again one id at a time, got a read of %d", len(read))
		}
	}

	only := listedSummaries(1)
	backend = &definitionBackend{definitions: definitionsFor(only, "ot-000")}
	got, err = WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main", only)
	if err != nil || len(got) != 1 || got[0] != only[0] || len(backend.reads) != 1 {
		t.Fatalf("a lone deleted object type: %v, %v, %d reads; want its summary from one read", got, err, len(backend.reads))
	}
}

// A one-id read that fails for any other reason than 404 still fails the detail.
func TestWithObjectTypeDefinitionsFailsWhenAOneIDReadFails(t *testing.T) {
	listed := listedSummaries(3)
	backend := &definitionBackend{definitions: definitionsFor(listed, "ot-000"),
		err: errors.New("bkn-backend down"), failSingleRead: "ot-002"}
	if got, err := WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main", listed); err == nil || got != nil {
		t.Fatalf("got %v, %v; want the one-id read's error", got, err)
	}
}

func TestWithObjectTypeDefinitionsFailsWhenTheReadFails(t *testing.T) {
	backend := &definitionBackend{err: errors.New("bkn-backend down")}
	got, err := WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main",
		[]*interfaces.ObjectType{{ID: "ot-1"}})
	if err == nil || got != nil {
		t.Fatalf("got %v, %v; want the read's error", got, err)
	}

	got, err = WithObjectTypeDefinitions(context.Background(), backend, "kn-1", "main", nil)
	if err != nil || len(got) != 0 || len(backend.reads) != 1 {
		t.Fatalf("an empty listing must read nothing: %v, %v, %d reads", got, err, len(backend.reads))
	}
}
