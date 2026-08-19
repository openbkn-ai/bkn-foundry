package common

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// requireOperatorTypePermission verifies that the caller holds the specified operation permission on the operator type.
//
// Used for /function/execute, /ai_generate/* endpoints that are not affiliated with any existing resources: they operate on.
// Function codes that have not yet been released into the library do not have resource IDs to judge, so they are judged based on the type level (ResourceIDAll), and the semantics is the same as.
// CheckCreatePermission is consistent in logics/auth/decision.go.
//
// Valid only on the public side. The internal side (internal-v1) is called between services, and the identity comes from the X-Account-ID header rather than the verified.
// token, following the existing idiom within the service (see logics/operator/query.go:31) to skip the determination and avoid interrupting existing callers.
func requireOperatorTypePermission(
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
	authorized, err := authService.OperationCheckAll(ctx, accessor,
		interfaces.ResourceIDAll, interfaces.AuthResourceTypeOperator, operation)
	if err != nil {
		return err
	}
	if !authorized {
		return errors.NewHTTPError(ctx, http.StatusForbidden, forbiddenCodeFor(operation), nil)
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
