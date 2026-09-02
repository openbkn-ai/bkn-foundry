// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/query_authorization_service.go -destination ../interfaces/mock/mock_query_authorization_service.go
type QueryAuthorizationService interface {
	AuthorizeObjectTypeQuery(ctx context.Context, knID, branch, objectTypeID string) error
	AuthorizeActionTypeQuery(ctx context.Context, knID, branch, actionTypeID string) error
	AuthorizeMetricQuery(ctx context.Context, knID, branch, metricID string) error
	AuthorizeMetricDryRun(ctx context.Context, knID, branch string, definition *MetricDefinition) error
	AuthorizeSubgraphBySource(ctx context.Context, query *SubGraphQueryBaseOnSource) error
	AuthorizeSubgraphByTypePath(ctx context.Context, query *SubGraphQueryBaseOnTypePath) error
	AuthorizeSubgraphByObjects(ctx context.Context, query *SubGraphQueryBaseOnObjects) error
}
