// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/query_authorization_service.go -destination ../interfaces/mock/mock_query_authorization_service.go
type QueryAuthorizationService interface {
	AuthorizeObjectTypeSchema(ctx context.Context, knID, branch, objectTypeID string) error
	AuthorizeObjectTypeQuery(ctx context.Context, knID, branch, objectTypeID string) error
	AuthorizeActionTypeQuery(ctx context.Context, knID, branch, actionTypeID string) error
	AuthorizeObjectMetricQuery(ctx context.Context, knID, branch, metricID string) error
	AuthorizeObjectMetricTrial(ctx context.Context, knID, branch string, definition *ObjectMetricDefinitionV1, query *ObjectMetricQueryRequestV1) error
	AuthorizeSubgraphBySource(ctx context.Context, query *SubGraphQueryBaseOnSource) error
	AuthorizeSubgraphByTypePath(ctx context.Context, query *SubGraphQueryBaseOnTypePath) error
	AuthorizeSubgraphByObjects(ctx context.Context, query *SubGraphQueryBaseOnObjects) error
}

// ObjectMetricExecutionAuthorizationService authorizes every resource and
// property that an immutable published metric graph may read. It is kept as a
// separate capability so existing query authorization consumers do not need to
// implement metric-runtime-specific behavior.
type ObjectMetricExecutionAuthorizationService interface {
	AuthorizeObjectMetricExecution(ctx context.Context, knID, branch string,
		executionContext *ObjectMetricExecutionContextV1, query *ObjectMetricQueryRequestV1) error
}

// ObjectMetricAuthorizationFingerprint binds a computed metric result to the
// effective authorization decisions that selected its input rows. Callers must
// still authorize every continuation before comparing this digest.
type ObjectMetricAuthorizationFingerprint struct {
	Digest string
}

// ObjectMetricExecutionAuthorizationFingerprintService extends metric
// authorization for stateful result pagination. It keeps the established
// authorization methods unchanged for callers that do not retain results.
type ObjectMetricExecutionAuthorizationFingerprintService interface {
	AuthorizeObjectMetricExecutionWithFingerprint(ctx context.Context, knID, branch string,
		executionContext *ObjectMetricExecutionContextV1,
		query *ObjectMetricQueryRequestV1) (ObjectMetricAuthorizationFingerprint, error)
	AuthorizeObjectMetricTrialWithFingerprint(ctx context.Context, knID, branch string,
		definition *ObjectMetricDefinitionV1,
		query *ObjectMetricQueryRequestV1, dependencies []*ObjectMetricDefinitionV1) (ObjectMetricAuthorizationFingerprint, error)
}

// MetricSnapshotProxyAuthorizationService rechecks the authoritative managed
// proxy lifecycle and effective resource grants before reusing computed data.
type MetricSnapshotProxyAuthorizationService interface {
	AuthorizeMetricSnapshotProxies(ctx context.Context, bindings []*TrustedProxyContext) error
}

type ManagedProxyAccountState struct {
	ProxyAccountID      string `json:"proxy_account_id"`
	AccountType         string `json:"account_type"`
	ManagedBy           string `json:"managed_by"`
	ManagedResourceType string `json:"managed_resource_type"`
	ManagedResourceID   string `json:"managed_resource_id"`
	LifecycleStatus     string `json:"lifecycle_status"`
	Enabled             bool   `json:"enabled"`
}

type MetricSnapshotProxyAccess interface {
	GetManagedProxy(ctx context.Context, proxyID string) (*ManagedProxyAccountState, error)
	CheckPermissions(ctx context.Context, request PermissionChecksRequest) (PermissionChecksResponse, error)
}
