// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"net/http"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_target"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func validateActionOperation(action *interfaces.ActionType) error {
	return action_target.ValidateDefinition(action)
}

func actionTargetError(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails(detail)
}

func (s *actionSchedulerService) getInstancesForAction(ctx context.Context, action *interfaces.ActionType, request *interfaces.ActionExecutionRequest) ([]interfaces.ObjectSystemInfo, []map[string]any, error) {
	resolver := action_target.Resolver{Models: s.omAccess, Objects: s.ots}
	return resolver.Resolve(ctx, action, request)
}
