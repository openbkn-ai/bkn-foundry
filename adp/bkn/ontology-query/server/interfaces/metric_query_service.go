// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// ObjectMetricQueryServiceV1 is the stable runtime contract implemented by
// the paid object-metric extension. Core query services depend only on this
// contract and never import the private implementation.
//
//go:generate mockgen -source ../interfaces/metric_query_service.go -destination ../interfaces/mock/mock_object_metric_query_service_v1.go
type ObjectMetricQueryServiceV1 interface {
	QueryObjectMetricDataV1(ctx context.Context, knID string, branch string, metricID string, version int, body *ObjectMetricQueryRequestV1) (ObjectMetricDataV1, error)
	TrialObjectMetricDataV1(ctx context.Context, knID string, branch string, body *ObjectMetricTrialRequestV1) (ObjectMetricDataV1, error)
}

// Implementations may provide paged participant lookup for an authorized
// result bucket. The existing query interface remains source compatible.
type ObjectMetricDrillThroughServiceV1 interface {
	QueryObjectMetricDrillThroughV1(ctx context.Context, knID, branch, metricID string, version int, body *ObjectMetricDrillThroughRequestV1) (ObjectMetricDrillThroughV1, error)
	TrialObjectMetricDrillThroughV1(ctx context.Context, knID, branch string, body *ObjectMetricTrialDrillThroughRequestV1) (ObjectMetricDrillThroughV1, error)
}
