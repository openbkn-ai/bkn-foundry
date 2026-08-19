// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knmetrics carries steps 2 and 3 of the OT-first metric path:
//
//	Recall the object type → see the available metrics under the object type → get the number according to the metric's own semantics.
//
// Step 2 is AttachRelatedMetrics: put scope_type=object_type and scope_ref=<ot_id>.
// The metric is attached to the object type. Indicators that are not bound to logical attributes are completely invisible on the object type, so the Agent can only degenerate into.
// run_sql overrides the semantics itself.
//
// The third step is QueryMetric: After selecting the metric, it is handed over to ontology-query to calculate according to MetricDefinition.
// The instance-level branch that has bound logical properties still uses get_logic_properties_values, which is not included in this package.
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

// Incorrect input parameters for metric query. They are all errors on the caller's side, and are directly returned to the Agent to change the parameters and try again.
var (
	ErrKnIDRequired     = errors.New("kn_id is required")
	ErrMetricIDRequired = errors.New("metric_id is required (from get_object_types related_metrics)")
)

// Step whitelist, consistent with the granularity supported by MetricDefinition.
var validSteps = map[string]struct{}{
	"day": {}, "week": {}, "month": {}, "quarter": {}, "year": {},
}

// KnMetricsService metric visibility and metric access.
type KnMetricsService interface {
	// AttachRelatedMetrics attaches the metrics under its scope to the object type (failed to downgrade to not attached).
	AttachRelatedMetrics(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType)
	// AttachRelatedMetricCounts Attach only counts, used for progressive drill-down of get_kn_detail.
	AttachRelatedMetricCounts(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType)
	// QueryMetric takes the number based on the metric's own semantics.
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

// NewKnMetricsService create KnMetricsService singleton.
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

// NewKnMetricsServiceWith injection dependency creation (for testing).
func NewKnMetricsServiceWith(logger interfaces.Logger, bkn interfaces.BknBackendAccess,
	oq interfaces.DrivenOntologyQuery) KnMetricsService {
	return &knMetricsService{logger: logger, bknBackend: bkn, ontologyQuery: oq}
}

// AttachRelatedMetrics distributes metrics to each object type according to scope_ref.
//
// Failure to get the index is not fatal: the object type definition itself has been obtained, and marking the entire get_object_types as failed will only.
// The Agent cannot even read the schema. Downgrade to "no related_metrics" and leave logs.
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

// AttachRelatedMetricCounts only writes counts, not details (used for get_kn_detail summary).
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

// metricsByScope retrieves the metrics under this batch of object types in batches at one time and sorts them according to scope_ref; returns nil on failure.
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

// QueryMetric forwards ontology-query's metric fetching endpoint after verifying the input parameters.
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
