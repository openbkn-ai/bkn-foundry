// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knmetrics carries steps 2 and 3 of the OT-first metric path:
//
//	Recall the object type → see the available metrics under the object type → get the number according to the metric's own semantics.
//
// Step 2 is AttachRelatedMetrics: expose published versions owned by the
// object type. Step 3 is QueryMetric: pass the selected immutable version to
// ontology-query. Object metrics are always instance-set aggregations; object
// type logical properties are a separate capability.
package knmetrics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Incorrect input parameters for metric query. They are all errors on the caller's side, and are directly returned to the Agent to change the parameters and try again.
var (
	ErrKnIDRequired     = errors.New("kn_id is required")
	ErrMetricIDRequired = errors.New("metric_id is required (from get_object_types related_metrics)")
	ErrVersionRequired  = errors.New("version must be a positive published object metric version (from get_object_types related_metrics)")
)

// KnMetricsService metric visibility and metric access.
type KnMetricsService interface {
	// AttachRelatedMetrics attaches the metrics under its scope to the object type.
	AttachRelatedMetrics(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) error
	// AttachRelatedMetricCounts Attach only counts, used for progressive drill-down of get_kn_detail.
	AttachRelatedMetricCounts(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) error
	// QueryMetric takes the number based on the metric's own semantics.
	QueryMetric(ctx context.Context, req *interfaces.QueryMetricReq) (*interfaces.QueryMetricResp, error)
	DrillThroughMetric(ctx context.Context, req *interfaces.MetricDrillThroughReq) (*interfaces.MetricDrillThroughResp, error)
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

// AttachRelatedMetrics distributes published metrics by owner object type.
func (s *knMetricsService) AttachRelatedMetrics(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) error {
	byScope, err := s.metricsByScope(ctx, knID, objectTypes)
	if err != nil {
		return err
	}
	for _, ot := range objectTypes {
		if ot == nil {
			continue
		}
		ot.RelatedMetrics = byScope[ot.ID]
		ot.RelatedMetricCount = len(ot.RelatedMetrics)
	}
	return nil
}

// AttachRelatedMetricCounts only writes counts, not details (used for get_kn_detail summary).
func (s *knMetricsService) AttachRelatedMetricCounts(ctx context.Context, knID string, objectTypes []*interfaces.ObjectType) error {
	byScope, err := s.metricsByScope(ctx, knID, objectTypes)
	if err != nil {
		return err
	}
	for _, ot := range objectTypes {
		if ot == nil {
			continue
		}
		ot.RelatedMetricCount = len(byScope[ot.ID])
	}
	return nil
}

// metricsByScope groups visible metrics by owner object type.
func (s *knMetricsService) metricsByScope(ctx context.Context, knID string,
	objectTypes []*interfaces.ObjectType) (map[string][]*interfaces.RelatedMetric, error) {
	ids := make([]string, 0, len(objectTypes))
	for _, ot := range objectTypes {
		if ot != nil && strings.TrimSpace(ot.ID) != "" {
			ids = append(ids, ot.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	metrics, err := s.bknBackend.ListMetricsByObjectTypes(ctx, knID, ids)
	if err != nil {
		return nil, err
	}

	byScope := make(map[string][]*interfaces.RelatedMetric, len(ids))
	for _, m := range metrics {
		if m == nil || m.OwnerObjectTypeID == "" {
			continue
		}
		byScope[m.OwnerObjectTypeID] = append(byScope[m.OwnerObjectTypeID], m)
	}
	return byScope, nil
}

// QueryMetric forwards only V1 runtime choices to a published, version-pinned object metric.
func (s *knMetricsService) QueryMetric(ctx context.Context, req *interfaces.QueryMetricReq) (*interfaces.QueryMetricResp, error) {
	if req == nil {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, ErrKnIDRequired
	}
	knID := strings.TrimSpace(req.KnID)
	metricID := strings.TrimSpace(req.MetricID)
	if knID == "" {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, ErrKnIDRequired
	}
	if metricID == "" {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, ErrMetricIDRequired
	}
	if req.Version < 1 {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, ErrVersionRequired
	}
	if err := validateMetricRuntimeQuery(req.Query, 0); err != nil {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, err
	}
	if req.Query.Time != nil && req.Query.Time.Start > req.Query.Time.End {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, errors.New("query.time.start must be <= query.time.end")
	}
	resp, err := s.ontologyQuery.QueryMetricData(ctx, knID, metricID, req.Version, &req.Query)
	if err != nil {
		return nil, err
	}

	out := &interfaces.QueryMetricResp{KnID: knID, MetricID: metricID, Version: req.Version, Entries: []interfaces.ObjectMetricDataEntry{}}
	if resp != nil {
		if len(resp.Entries) > 0 {
			out.Entries = resp.Entries
		}
		out.Model = resp.Model
		out.Status = resp.Status
		out.EmptyReason = resp.EmptyReason
		out.TotalCount = resp.TotalCount
		out.NextCursor = resp.NextCursor
		out.Warnings = resp.Warnings
		out.OverallMs = resp.OverallMs
		out.EvaluatedAt = resp.EvaluatedAt
		out.EffectiveWindow = resp.EffectiveWindow
	}
	return out, nil
}

// DrillThroughMetric pages authorized participants of a selected published result bucket.
func (s *knMetricsService) DrillThroughMetric(ctx context.Context, req *interfaces.MetricDrillThroughReq) (*interfaces.MetricDrillThroughResp, error) {
	if req == nil || strings.TrimSpace(req.KnID) == "" || strings.TrimSpace(req.MetricID) == "" || req.Version < 1 || strings.TrimSpace(req.BucketKey) == "" {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, errors.New("kn_id, metric_id, version, and bucket_key are required")
	}
	if req.PageSize < 0 || req.PageSize > 100 {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, errors.New("page_size must be between 0 and 100")
	}
	if err := validateMetricRuntimeQuery(req.Query, 0); err != nil {
		bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
		return nil, err
	}
	resp, err := s.ontologyQuery.DrillThroughMetricData(ctx, strings.TrimSpace(req.KnID), strings.TrimSpace(req.MetricID), req.Version, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("metric drill-through returned no result")
	}
	if resp.Participants == nil {
		resp.Participants = []interfaces.ObjectMetricParticipant{}
	}
	resp.KnID = strings.TrimSpace(req.KnID)
	resp.MetricID = strings.TrimSpace(req.MetricID)
	resp.Version = req.Version
	return resp, nil
}

func validateMetricRuntimeQuery(query interfaces.ObjectMetricQuery, depth int) error {
	if query.PageSize < 0 || query.PageSize > 1000 {
		return errors.New("query.page_size must be between 0 and 1000")
	}
	if query.Cursor != "" && query.PageSize == 0 {
		return errors.New("query.page_size is required with cursor")
	}
	if query.Filter != nil && strings.TrimSpace(query.Filter.NodeType) == "" {
		return errors.New("query.filter.node_type is required")
	}
	if query.BucketSelection != nil {
		if depth >= 8 {
			return fmt.Errorf("metric drill depth exceeds 8")
		}
		return validateMetricRuntimeQuery(query.BucketSelection.ParentQuery, depth+1)
	}
	return nil
}

//nolint:unused // Retained for metric diagnostics.
func (s *knMetricsService) warnf(ctx context.Context, format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.WithContext(ctx).Warnf(format, args...)
}
