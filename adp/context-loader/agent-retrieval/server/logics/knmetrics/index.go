// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knmetrics 承载 OT-first 指标路径的第 2、3 步：
//
//	召回对象类 → 在该对象类下看见可用指标 → 按指标自身口径取数
//
// 第 2 步是 AttachRelatedMetrics：把 scope_type=object_type 且 scope_ref=<ot_id> 的
// 指标挂到对象类上。未绑逻辑属性的指标在对象类上本来完全不可见，Agent 因此只能退化成
// run_sql 自己重写口径。
//
// 第 3 步是 QueryMetric：选定指标后交给 ontology-query 按 MetricDefinition 计算。
// 实例级、且已绑逻辑属性的那一支仍走 get_logic_properties_values，不在本包。
package knmetrics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// 指标查询入参错误。均为调用方错误，直接回给 Agent 让它改参数重试。
var (
	ErrKnIDRequired     = errors.New("kn_id is required")
	ErrMetricIDRequired = errors.New("metric_id is required (from get_object_types related_metrics)")
)

// 步长白名单，与 MetricDefinition 支持的粒度一致。
var validSteps = map[string]struct{}{
	"day": {}, "week": {}, "month": {}, "quarter": {}, "year": {},
}

// KnMetricsService 指标可见性与指标取数。
type KnMetricsService interface {
	// AttachRelatedMetrics 给对象类挂上其 scope 下的指标（失败降级为不挂）。
	AttachRelatedMetrics(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType)
	// AttachRelatedMetricCounts 只挂计数，用于 get_kn_detail 的渐进式下钻。
	AttachRelatedMetricCounts(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType)
	// QueryMetric 按指标自身口径取数。
	QueryMetric(ctx context.Context, req *interfaces.QueryMetricReq) (*interfaces.QueryMetricResp, error)
}

type knMetricsService struct {
	logger        interfaces.Logger
	bknBackend    interfaces.BknBackendAccess
	ontologyQuery interfaces.DrivenOntologyQuery
}

var (
	once     sync.Once
	instance KnMetricsService
)

// NewKnMetricsService 创建 KnMetricsService 单例。
func NewKnMetricsService() KnMetricsService {
	once.Do(func() {
		conf := config.NewConfigLoader()
		instance = &knMetricsService{
			logger:        conf.GetLogger(),
			bknBackend:    drivenadapters.NewBknBackendAccess(),
			ontologyQuery: drivenadapters.NewOntologyQueryAccess(),
		}
	})
	return instance
}

// NewKnMetricsServiceWith 注入依赖创建（测试用）。
func NewKnMetricsServiceWith(logger interfaces.Logger, bkn interfaces.BknBackendAccess,
	oq interfaces.DrivenOntologyQuery) KnMetricsService {
	return &knMetricsService{logger: logger, bknBackend: bkn, ontologyQuery: oq}
}

// AttachRelatedMetrics 按 scope_ref 把指标分发到各对象类。
//
// 取不到指标不算致命：对象类定义本身已经拿到了，把整个 get_object_types 打成失败只会
// 让 Agent 连 schema 都读不到。降级成「没有 related_metrics」并留日志。
func (s *knMetricsService) AttachRelatedMetrics(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) {
	byScope := s.metricsByScope(ctx, knID, objectTypes)
	if byScope == nil {
		return
	}
	for _, ot := range objectTypes {
		if ot == nil {
			continue
		}
		ot.RelatedMetrics = byScope[ot.ID]
		ot.RelatedMetricCount = len(ot.RelatedMetrics)
	}
}

// AttachRelatedMetricCounts 只写计数，不写明细（get_kn_detail summary 用）。
func (s *knMetricsService) AttachRelatedMetricCounts(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) {
	byScope := s.metricsByScope(ctx, knID, objectTypes)
	if byScope == nil {
		return
	}
	for _, ot := range objectTypes {
		if ot == nil {
			continue
		}
		ot.RelatedMetricCount = len(byScope[ot.ID])
	}
}

// metricsByScope 一次批量取回这批对象类下的指标，按 scope_ref 归类；失败返回 nil。
func (s *knMetricsService) metricsByScope(ctx context.Context, knID string,
	objectTypes []*interfaces.ObjectType) map[string][]*interfaces.RelatedMetric {
	ids := make([]string, 0, len(objectTypes))
	for _, ot := range objectTypes {
		if ot != nil && strings.TrimSpace(ot.ID) != "" {
			ids = append(ids, ot.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	metrics, err := s.bknBackend.ListMetricsByObjectTypes(ctx, knID, ids)
	if err != nil {
		s.warnf(ctx, "[KnMetrics] list metrics for kn=%s failed, object types answered without metrics: %v", knID, err)
		return nil
	}

	byScope := make(map[string][]*interfaces.RelatedMetric, len(ids))
	for _, m := range metrics {
		if m == nil || m.ScopeRef == "" {
			continue
		}
		byScope[m.ScopeRef] = append(byScope[m.ScopeRef], m)
	}
	return byScope
}

// QueryMetric 校验入参后转发 ontology-query 的指标取数端点。
func (s *knMetricsService) QueryMetric(ctx context.Context, req *interfaces.QueryMetricReq) (*interfaces.QueryMetricResp, error) {
	if req == nil {
		return nil, ErrKnIDRequired
	}
	knID := strings.TrimSpace(req.KnID)
	metricID := strings.TrimSpace(req.MetricID)
	if knID == "" {
		return nil, ErrKnIDRequired
	}
	if metricID == "" {
		return nil, ErrMetricIDRequired
	}
	if err := validateTimeWindow(req.Time, req.FillNull); err != nil {
		return nil, err
	}

	downstream := &interfaces.MetricQueryDownstreamReq{
		Time:               req.Time,
		Cond:               req.Cond,
		AnalysisDimensions: req.AnalysisDimensions,
		OrderBy:            req.OrderBy,
		Having:             req.Having,
		Limit:              req.Limit,
	}
	resp, err := s.ontologyQuery.QueryMetricData(ctx, knID, metricID, req.FillNull, downstream)
	if err != nil {
		return nil, err
	}

	out := &interfaces.QueryMetricResp{KnID: knID, MetricID: metricID, Datas: []*interfaces.MetricDataSeries{}}
	if resp != nil {
		if len(resp.Datas) > 0 {
			out.Datas = resp.Datas
		}
		out.OverallMs = resp.OverallMs
	}
	return out, nil
}

// isTrendWindow mirrors ontology-query's metricQueryIsTrendTime: a window is a
// trend (series) query unless instant is explicitly true. Omitting instant means
// trend, not instant — getting this backwards is what makes the common
// {"start":…,"end":…} shape pass here and fail downstream.
func isTrendWindow(t *interfaces.MetricTimeWindow) bool {
	return t != nil && (t.Instant == nil || !*t.Instant)
}

// validateTimeWindow rejects a malformed time window before it costs a round trip.
//
// Every rule here mirrors ontology-query's validateMetricQueryRequest exactly —
// same conditions, same wording — because a local check that is stricter than the
// downstream one turns a valid call into a false rejection, and a looser one just
// delays the same error. The point is a faster, identical verdict, not a second
// opinion.
func validateTimeWindow(t *interfaces.MetricTimeWindow, fillNull bool) error {
	if t == nil {
		if fillNull {
			return errors.New("fill_null requires a time range (time is required)")
		}
		return nil
	}
	if (t.Start != nil) != (t.End != nil) {
		return errors.New("time.start and time.end must both be set when either is set")
	}
	if t.Start != nil && t.End != nil && *t.Start > *t.End {
		return errors.New("time.start must be <= time.end")
	}
	if isTrendWindow(t) {
		if t.Step == nil || strings.TrimSpace(*t.Step) == "" {
			return errors.New("trend query requires time.step (calendar interval only). " +
				"Omitting time.instant means a trend query; set time.instant=true for a single point")
		}
		if err := validateCalendarStep(*t.Step); err != nil {
			return err
		}
	}
	if fillNull {
		if !isTrendWindow(t) {
			return errors.New("fill_null is only valid for trend (range) queries: set time.instant to false or omit it")
		}
		if t.Start == nil || t.End == nil {
			return errors.New("fill_null requires time.start and time.end")
		}
	}
	return nil
}

// validateCalendarStep accepts the same steps as ontology-query, case-insensitively.
func validateCalendarStep(raw string) error {
	if _, ok := validSteps[strings.ToLower(strings.TrimSpace(raw))]; !ok {
		return fmt.Errorf("step must be a calendar interval: day, week, month, quarter, year (got %q)", raw)
	}
	return nil
}

func (s *knMetricsService) warnf(ctx context.Context, format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.WithContext(ctx).Warnf(format, args...)
}
