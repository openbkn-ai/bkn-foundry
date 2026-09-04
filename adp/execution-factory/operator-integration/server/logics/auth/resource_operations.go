package auth

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// ProjectAuthorizeOperations obtains the per-record authorize operation for a public management
// list. Internal callers keep their existing response contract because they do not render the
// Studio object-grant control.
func ProjectAuthorizeOperations(
	ctx context.Context,
	authorization interfaces.IAuthorizationService,
	userID string,
	resourceIDs []string,
	resourceType interfaces.AuthResourceType,
) (map[string][]interfaces.AuthOperationType, error) {
	if !common.IsPublicAPIFromCtx(ctx) || len(resourceIDs) == 0 {
		return map[string][]interfaces.AuthOperationType{}, nil
	}
	accessor, err := authorization.GetAccessor(ctx, userID)
	if err != nil {
		return nil, err
	}
	return authorization.ResourceFilterOperations(
		ctx,
		accessor,
		resourceIDs,
		resourceType,
		[]interfaces.AuthOperationType{interfaces.AuthOperationTypeView},
		[]interfaces.AuthOperationType{interfaces.AuthOperationTypeAuthorize},
	)
}
