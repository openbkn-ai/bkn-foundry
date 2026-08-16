// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

// MetricService exposes metric CRUD and concept search (Task 3, IMPLEMENTATION_PLAN).
//
//go:generate mockgen -source metric_service.go -destination mock/mock_metric_service.go
type MetricService interface {
	// CheckMetricExistByID checks whether a metric exists by KN, branch, and ID. When present, it returns the stored
	// name. It is aligned with ObjectTypeService.CheckObjectTypeExistByID for early handler validation.
	CheckMetricExistByID(ctx context.Context, knID, branch, metricID string) (name string, exist bool, err error)
	// CheckMetricExistByName checks whether a metric exists by name. When present, it returns the metric ID and is
	// aligned with CheckObjectTypeExistByName.
	CheckMetricExistByName(ctx context.Context, knID, branch, name string) (metricID string, exist bool, err error)

	CreateMetrics(ctx context.Context, tx *sql.Tx, entries []*MetricDefinition, strictMode bool, importMode string) ([]string, error)
	ListMetrics(ctx context.Context, query MetricsListQueryParams) (*MetricsList, error)
	GetMetricByID(ctx context.Context, knID string, branch string, metricID string) (*MetricDefinition, error)
	GetMetricsByIDs(ctx context.Context, knID string, branch string, metricIDs []string) ([]*MetricDefinition, error)
	UpdateMetric(ctx context.Context, tx *sql.Tx, req *MetricDefinition, strictMode bool) error
	DeleteMetricsByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, metricIDs []string) error
	// DeleteMetricsByKnID is an internal API that deletes all metrics by knowledge network without checking
	// permissions. tx must be non-nil to match DeleteActionTypesByKnID.
	DeleteMetricsByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error

	SearchMetrics(ctx context.Context, query *ConceptsQuery) (MetricSearchResult, error)

	// InsertDatasetData writes metrics to the BKN concept dataset. It matches ObjectTypeService.InsertDatasetData and
	// is used by scenarios without a user context, such as concept synchronization.
	InsertDatasetData(ctx context.Context, metrics []*MetricDefinition) error

	// ValidateMetrics validates scope and object-type fields in strictMode, matching CreateMetrics strict validation,
	// without persisting data. The handler validates request-body syntax with ValidateMetricRequests first.
	// When batch is non-nil and strictMode=true, resolve scope_ref from BatchIDIndex.ObjectTypes first for same-KN and
	// pre-persistence Upload tar scenarios. When batch is nil, use storage for the REST batch-validation path.
	ValidateMetrics(ctx context.Context, entries []*MetricDefinition, strictMode bool, importMode string, batch *BatchIDIndex) error
}
