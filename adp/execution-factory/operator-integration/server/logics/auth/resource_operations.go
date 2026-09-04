// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// ProjectAuthorizeOperations obtains the per-record authorize operation for a public management
// list. Internal callers keep their existing response contract because they do not render the
// Studio object-grant control.
func ProjectAuthorizeOperations(
	ctx context.Context,
	authorization interfaces.IAuthorizationService,
	accessor *interfaces.AuthAccessor,
	resourceIDs []string,
	resourceType interfaces.AuthResourceType,
) (map[string][]interfaces.AuthOperationType, error) {
	if !common.IsPublicAPIFromCtx(ctx) || len(resourceIDs) == 0 {
		return map[string][]interfaces.AuthOperationType{}, nil
	}
	if accessor == nil {
		return nil, fmt.Errorf("authorization accessor is required for public resource operation projection")
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
