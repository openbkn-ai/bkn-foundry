package operationaudit

import "context"

type managementAuditOwnerKey struct{}

// ToolIDContextKey carries the Tool ID returned by a completed conversion.
// The source Operator ID in the request is not the created Tool's identity.
const ToolIDContextKey = "bkn.operation_audit.tool_id"

// PartialBundleContextKey is set only when a successful OpenAPI bundle reply
// reports failed child operations, so the request attempt is not called fully successful.
const PartialBundleContextKey = "bkn.operation_audit.partial_bundle"

// TargetNameContextKey carries a producer-resolved display name for routes
// whose request only contains an object ID (for example toolbox deletion).
// The producer sets it before the handler returns; the audit middleware only
// records the supplied snapshot and never performs a consumer-side lookup.
const TargetNameContextKey = "bkn.operation_audit.target_name"

// WithManagementAuditOwner gives the completed HTTP request sole ownership of
// a registered management attempt. Business-level producers still own all
// other operations, including runtime executions.
func WithManagementAuditOwner(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementAuditOwnerKey{}, true)
}

func ManagementAuditOwned(ctx context.Context) bool {
	return ctx != nil && ctx.Value(managementAuditOwnerKey{}) == true
}
