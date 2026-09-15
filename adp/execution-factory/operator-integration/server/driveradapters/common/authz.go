package common

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// requireFunctionPermission verifies permissions for standalone Function endpoints.
//
// Code execution without a persisted function box uses a reserved adhoc resource.
// Generation uses the type-level create operation, like creating a function box.
//
// Valid only on the public side. The internal side (internal-v1) is called between services, and the identity comes from the X-Account-ID header rather than the verified.
// token, following the existing idiom within the service (see logics/operator/query.go:31) to skip the determination and avoid interrupting existing callers.
func requireFunctionPermission(
	ctx context.Context,
	authService interfaces.IAuthorizationService,
	operation interfaces.AuthOperationType,
) error {
	if !common.IsPublicAPIFromCtx(ctx) {
		return nil
	}
	authContext, ok := common.GetAccountAuthContextFromCtx(ctx)
	if !ok || authContext == nil {
		return errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "authentication required")
	}
	accessor := &interfaces.AuthAccessor{
		ID:   authContext.AccountID,
		Type: authContext.AccountType,
	}
	resourceID := interfaces.ResourceIDAll
	if operation == interfaces.AuthOperationTypeExecute {
		resourceID = "adhoc"
	}
	authorized, err := authService.OperationCheckAll(ctx, accessor,
		resourceID, interfaces.AuthResourceTypeFunction, operation)
	if err != nil {
		return err
	}
	if !authorized {
		// Name the missing grant so an administrator knows what to assign. It is the
		// same for every caller, so it reveals nothing about the account.
		return errors.NewHTTPError(ctx, http.StatusForbidden, forbiddenCodeFor(operation), map[string]any{
			"resource_type": string(interfaces.AuthResourceTypeFunction),
			"resource_id":   resourceID,
			"operation":     string(operation),
		})
	}
	return nil
}

// forbiddenCodeFor returns the rejection error code corresponding to the operation, so that the prompt received by the front end is consistent with the action.
func forbiddenCodeFor(operation interfaces.AuthOperationType) errors.ErrorCode {
	switch operation {
	case interfaces.AuthOperationTypeExecute:
		return errors.ErrExtCommonUseForbidden
	case interfaces.AuthOperationTypeCreate:
		return errors.ErrExtCommonAddForbidden
	default:
		return errors.ErrExtCommonViewForbidden
	}
}
