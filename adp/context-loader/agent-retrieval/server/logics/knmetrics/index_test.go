// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knmetrics

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// stubBknBackend only implements what this package calls; the rest of
// BknBackendAccess is embedded so the stub keeps compiling as that interface grows.
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

type stubOntologyQuery struct {
	interfaces.DrivenOntologyQuery
	resp       *interfaces.MetricQueryDownstreamResp
	err        error
	gotKnID    string
	gotMetric  string
	gotFillNil bool
	gotReq     *interfaces.MetricQueryDownstreamReq
	calls      int
}

func (s *stubOntologyQuery) QueryMetricData(_ context.Context, knID, metricID string, fillNull bool,
	req *interfaces.MetricQueryDownstreamReq) (*interfaces.MetricQueryDownstreamResp, error) {
	s.calls++
	s.gotKnID, s.gotMetric, s.gotFillNil, s.gotReq = knID, metricID, fillNull, req
	return s.resp, s.err
}

func ptr[T any](v T) *T { return &v }

func TestAttachRelatedMetrics(t *testing.T) {
	convey.Convey("指标按 scope_ref 分发到各对象类", t, func() {
		bkn := &stubBknBackend{metrics: []*interfaces.RelatedMetric{
			{ID: "m1", Name: "产品总数", ScopeRef: "ot1"},
			{ID: "m2", Name: "物料总数", ScopeRef: "ot2"},
			{ID: "m3", Name: "在途产品数", ScopeRef: "ot1"},
			{ID: "m4", Name: "无归属", ScopeRef: ""},
		}}
		svc := NewKnMetricsServiceWith(nil, bkn, nil)
		ots := []*interfaces.ObjectType{{ID: "ot1"}, {ID: "ot2"}, {ID: "ot3"}, nil}

		svc.AttachRelatedMetrics(context.Background(), "kn1", ots)

		convey.So(bkn.calls, convey.ShouldEqual, 1)
		convey.So(bkn.gotKnID, convey.ShouldEqual, "kn1")
		convey.So(bkn.gotIDs, convey.ShouldResemble, []string{"ot1", "ot2", "ot3"})
		convey.So(len(ots[0].RelatedMetrics), convey.ShouldEqual, 2)
		convey.So(ots[0].RelatedMetricCount, convey.ShouldEqual, 2)
		convey.So(ots[1].RelatedMetrics[0].ID, convey.ShouldEqual, "m2")
		convey.So(ots[2].RelatedMetrics, convey.ShouldBeEmpty)
		convey.So(ots[2].RelatedMetricCount, convey.ShouldEqual, 0)
	})

	convey.Convey("取指标失败时对象类照常返回，只是没有指标", t, func() {
		bkn := &stubBknBackend{err: errors.New("backend down")}
		svc := NewKnMetricsServiceWith(nil, bkn, nil)
		ots := []*interfaces.ObjectType{{ID: "ot1"}}

		svc.AttachRelatedMetrics(context.Background(), "kn1", ots)

		convey.So(ots[0].RelatedMetrics, convey.ShouldBeNil)
		convey.So(ots[0].RelatedMetricCount, convey.ShouldEqual, 0)
	})

	convey.Convey("没有对象类时不打后端", t, func() {
		bkn := &stubBknBackend{}
		svc := NewKnMetricsServiceWith(nil, bkn, nil)

		svc.AttachRelatedMetrics(context.Background(), "kn1", nil)

		convey.So(bkn.calls, convey.ShouldEqual, 0)
	})
}

func TestAttachRelatedMetricCounts(t *testing.T) {
	convey.Convey("只写计数不写明细", t, func() {
		bkn := &stubBknBackend{metrics: []*interfaces.RelatedMetric{
			{ID: "m1", ScopeRef: "ot1"},
			{ID: "m2", ScopeRef: "ot1"},
		}}
		svc := NewKnMetricsServiceWith(nil, bkn, nil)
		ots := []*interfaces.ObjectType{{ID: "ot1"}, {ID: "ot2"}}

		svc.AttachRelatedMetricCounts(context.Background(), "kn1", ots)

		convey.So(ots[0].RelatedMetricCount, convey.ShouldEqual, 2)
		convey.So(ots[0].RelatedMetrics, convey.ShouldBeNil)
		convey.So(ots[1].RelatedMetricCount, convey.ShouldEqual, 0)
	})
}

func TestAttachRelatedMetrics_PEPFailureIsNotDowngraded(t *testing.T) {
	ctx := context.Background()
	authErr := infraerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "safe unavailable")
	bkn := &stubBknBackend{err: authErr}
	svc := NewKnMetricsServiceWithPEP(nil, bkn, nil, true)

	err := svc.AttachRelatedMetrics(ctx, "kn1", []*interfaces.ObjectType{{ID: "ot1"}})
	if !errors.Is(err, authErr) {
		t.Fatalf("expected authorization dependency error, got %v", err)
	}
}

func TestAttachRelatedMetrics_PEPUnknownFailureIsNotDowngraded(t *testing.T) {
	dependencyErr := errors.New("connection reset")
	bkn := &stubBknBackend{err: dependencyErr}
	svc := NewKnMetricsServiceWithPEP(nil, bkn, nil, true)

	err := svc.AttachRelatedMetrics(context.Background(), "kn1", []*interfaces.ObjectType{{ID: "ot1"}})
	if !errors.Is(err, dependencyErr) {
		t.Fatalf("expected protected dependency error, got %v", err)
	}
}

func TestAttachRelatedMetricCounts_LegacyFailureStillDegrades(t *testing.T) {
	bkn := &stubBknBackend{err: errors.New("backend down")}
	svc := NewKnMetricsServiceWithPEP(nil, bkn, nil, false)

	if err := svc.AttachRelatedMetricCounts(context.Background(), "kn1", []*interfaces.ObjectType{{ID: "ot1"}}); err != nil {
		t.Fatalf("legacy rollout state should keep best-effort behavior, got %v", err)
	}
}

func TestQueryMetric(t *testing.T) {
	convey.Convey("入参透传给 ontology-query，出参只留序列", t, func() {
		oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{
			Datas:     []*interfaces.MetricDataSeries{{Values: []any{431}}},
			OverallMs: 12,
		}}
		svc := NewKnMetricsServiceWith(nil, nil, oq)

		resp, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
			KnID:               " kn1 ",
			MetricID:           " m1 ",
			AnalysisDimensions: []string{"region"},
			Limit:              ptr(10),
			FillNull:           true,
			// fill_null is only valid for sequence queries with start/end, and has the same rules as downstream.
			Time: &interfaces.MetricTimeWindow{
				Instant: ptr(false), Start: ptr(int64(1)), End: ptr(int64(2)), Step: ptr("day")},
		})

		convey.So(err, convey.ShouldBeNil)
		convey.So(oq.gotKnID, convey.ShouldEqual, "kn1")
		convey.So(oq.gotMetric, convey.ShouldEqual, "m1")
		convey.So(oq.gotFillNil, convey.ShouldBeTrue)
		convey.So(oq.gotReq.AnalysisDimensions, convey.ShouldResemble, []string{"region"})
		convey.So(*oq.gotReq.Limit, convey.ShouldEqual, 10)
		convey.So(resp.KnID, convey.ShouldEqual, "kn1")
		convey.So(resp.MetricID, convey.ShouldEqual, "m1")
		convey.So(len(resp.Datas), convey.ShouldEqual, 1)
		convey.So(resp.OverallMs, convey.ShouldEqual, 12)
	})

	convey.Convey("空结果返回空数组而不是 null", t, func() {
		oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{}}
		svc := NewKnMetricsServiceWith(nil, nil, oq)

		resp, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: "kn1", MetricID: "m1"})

		convey.So(err, convey.ShouldBeNil)
		convey.So(resp.Datas, convey.ShouldNotBeNil)
		convey.So(len(resp.Datas), convey.ShouldEqual, 0)
	})

	convey.Convey("缺 kn_id / metric_id 直接拒，不打下游", t, func() {
		oq := &stubOntologyQuery{}
		svc := NewKnMetricsServiceWith(nil, nil, oq)

		_, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{MetricID: "m1"})
		convey.So(err, convey.ShouldEqual, ErrKnIDRequired)

		_, err = svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: "kn1"})
		convey.So(err, convey.ShouldEqual, ErrMetricIDRequired)

		convey.So(oq.calls, convey.ShouldEqual, 0)
	})

	// Time window rules are aligned one by one in ontology-query's validateMetricQueryRequest: local is stricter than downstream.
	// It will falsely reject legitimate calls, and it will only delay the same error, both of which are unacceptable.
	convey.Convey("时间窗校验与下游同规则", t, func() {
		oq := &stubOntologyQuery{}
		svc := NewKnMetricsServiceWith(nil, nil, oq)
		base := func(tw *interfaces.MetricTimeWindow) *interfaces.QueryMetricReq {
			return &interfaces.QueryMetricReq{KnID: "kn1", MetricID: "m1", Time: tw}
		}

		convey.Convey("省略 instant 等同于序列查询，缺 step 就地拒", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Start: ptr(int64(1)), End: ptr(int64(2))}))
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "trend query requires time.step")
			convey.So(oq.calls, convey.ShouldEqual, 0)
		})

		convey.Convey("instant=false 缺 step 同样拒", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{Instant: ptr(false)}))
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "trend query requires time.step")
		})

		convey.Convey("step 不在日历粒度里就拒", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Instant: ptr(false), Step: ptr("fortnight")}))
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "step must be a calendar interval")
		})

		convey.Convey("step 大小写不敏感（下游 ToLower，本地不能更严）", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Instant: ptr(false), Step: ptr("Day")}))
			convey.So(err, convey.ShouldBeNil)
			convey.So(oq.calls, convey.ShouldEqual, 1)
		})

		convey.Convey("instant=true 带 step 不拒（下游只是忽略它）", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Instant: ptr(true), Step: ptr("day")}))
			convey.So(err, convey.ShouldBeNil)
			convey.So(oq.calls, convey.ShouldEqual, 1)
		})

		convey.Convey("start/end 必须成对", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Instant: ptr(true), Start: ptr(int64(1))}))
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "must both be set")
			convey.So(oq.calls, convey.ShouldEqual, 0)
		})

		convey.Convey("start 晚于 end 就拒", func() {
			_, err := svc.QueryMetric(context.Background(), base(&interfaces.MetricTimeWindow{
				Instant: ptr(true), Start: ptr(int64(20)), End: ptr(int64(10))}))
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(err.Error(), convey.ShouldContainSubstring, "time.start must be <= time.end")
		})
	})

	convey.Convey("fill_null 只对区间查询有效", t, func() {
		oq := &stubOntologyQuery{}
		svc := NewKnMetricsServiceWith(nil, nil, oq)

		_, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
			KnID: "kn1", MetricID: "m1", FillNull: true})
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "fill_null requires a time range")

		_, err = svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
			KnID: "kn1", MetricID: "m1", FillNull: true,
			Time: &interfaces.MetricTimeWindow{Instant: ptr(true), Start: ptr(int64(1)), End: ptr(int64(2))}})
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "only valid for trend")

		_, err = svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{
			KnID: "kn1", MetricID: "m1", FillNull: true,
			Time: &interfaces.MetricTimeWindow{Instant: ptr(false), Step: ptr("day")}})
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "fill_null requires time.start and time.end")

		convey.So(oq.calls, convey.ShouldEqual, 0)
	})

	convey.Convey("无时间窗（无时间维度的指标）直接放行", t, func() {
		oq := &stubOntologyQuery{resp: &interfaces.MetricQueryDownstreamResp{}}
		svc := NewKnMetricsServiceWith(nil, nil, oq)

		_, err := svc.QueryMetric(context.Background(), &interfaces.QueryMetricReq{KnID: "kn1", MetricID: "m1"})

		convey.So(err, convey.ShouldBeNil)
		convey.So(oq.calls, convey.ShouldEqual, 1)
	})
}
