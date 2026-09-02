// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

const actionExecutionPEPEnabledEnv = "ACTION_EXECUTION_PEP_ENABLED"

// ActionExecutionPEPEnabled reports whether the complete action execution PEP
// is enabled. It remains off until policies and schedule subjects are migrated.
func ActionExecutionPEPEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(actionExecutionPEPEnabledEnv)))
	return value == "true" || value == "1"
}

func (s *actionSchedulerService) authorizeActionType(ctx context.Context, knID string,
	actionType *interfaces.ActionType) ([]interfaces.PermissionRequirement, error) {
	if !ActionExecutionPEPEnabled() {
		return nil, nil
	}
	if s == nil || s.permissions == nil {
		return nil, actionPermissionUnavailable(ctx, fmt.Errorf("action permission service is not configured"))
	}
	requirements, err := s.resolveActionPermissionRequirements(ctx, knID, actionType)
	if err != nil {
		return nil, err
	}
	if err := s.permissions.RequirePermissions(ctx, requirements); err != nil {
		return nil, err
	}
	logger.Infof("Action execution permission check passed: phase=submit kn_id=%s action_type_id=%s requirements=%d",
		knID, actionType.ATID, len(requirements))
	return requirements, nil
}

func (s *actionSchedulerService) authorizeExecution(ctx context.Context,
	requirements []interfaces.PermissionRequirement) error {
	if !ActionExecutionPEPEnabled() {
		return nil
	}
	if s == nil || s.permissions == nil {
		return actionPermissionUnavailable(ctx, fmt.Errorf("action permission service is not configured"))
	}
	if len(requirements) == 0 {
		return actionPermissionUnavailable(ctx, fmt.Errorf("execution permission snapshot is missing"))
	}
	if err := s.permissions.RequirePermissions(ctx, requirements); err != nil {
		return err
	}
	logger.Infof("Action execution permission check passed: phase=invoke requirements=%d", len(requirements))
	return nil
}

func (s *actionSchedulerService) resolveActionPermissionRequirements(ctx context.Context, knID string,
	actionType *interfaces.ActionType) ([]interfaces.PermissionRequirement, error) {
	if actionType == nil {
		return nil, actionPermissionInvalid(ctx, "action type is required")
	}
	if s == nil || s.omAccess == nil {
		return nil, actionPermissionUnavailable(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	if err := validateActionResourceID(knID, actionType.ATID); err != nil {
		return nil, actionPermissionInvalid(ctx, err.Error())
	}

	requirements := []interfaces.PermissionRequirement{{
		ResourceType: interfaces.PermissionResourceTypeActionType,
		ResourceID:   knID + "/" + actionType.ATID,
		Operation:    interfaces.PermissionOperationExecute,
	}}
	switch actionType.ActionSource.Type {
	case interfaces.ActionSourceTypeTool:
		if err := validateStandaloneActionResourceID(actionType.ActionSource.BoxID); err != nil {
			return nil, actionPermissionInvalid(ctx, "tool box: "+err.Error())
		}
		requirements = append(requirements, interfaces.PermissionRequirement{
			ResourceType: interfaces.PermissionResourceTypeToolBox,
			ResourceID:   actionType.ActionSource.BoxID,
			Operation:    interfaces.PermissionOperationExecute,
		})
	case interfaces.ActionSourceTypeMCP:
		if err := validateStandaloneActionResourceID(actionType.ActionSource.McpID); err != nil {
			return nil, actionPermissionInvalid(ctx, "MCP: "+err.Error())
		}
		requirements = append(requirements, interfaces.PermissionRequirement{
			ResourceType: interfaces.PermissionResourceTypeMCP,
			ResourceID:   actionType.ActionSource.McpID,
			Operation:    interfaces.PermissionOperationExecute,
		})
	default:
		return nil, actionPermissionInvalid(ctx, fmt.Sprintf("unsupported action source type: %s", actionType.ActionSource.Type))
	}

	objectTypeIDs := []string{actionType.ObjectTypeID}
	if actionType.Affect != nil {
		objectTypeIDs = append(objectTypeIDs, actionType.Affect.ObjectTypeID)
	}
	for _, impact := range actionType.ImpactContracts {
		objectTypeIDs = append(objectTypeIDs, impact.ObjectTypeID)
	}
	seenObjectTypes := make(map[string]struct{}, len(objectTypeIDs))
	for _, objectTypeID := range objectTypeIDs {
		objectTypeID = strings.TrimSpace(objectTypeID)
		if objectTypeID == "" {
			continue
		}
		if _, exists := seenObjectTypes[objectTypeID]; exists {
			continue
		}
		seenObjectTypes[objectTypeID] = struct{}{}
		if err := validateActionResourceID(knID, objectTypeID); err != nil {
			return nil, actionPermissionInvalid(ctx, err.Error())
		}
		objectType, exists, err := s.omAccess.GetObjectType(ctx, knID, interfaces.MAIN_BRANCH, objectTypeID)
		if err != nil {
			return nil, actionPermissionUnavailable(ctx, err)
		}
		if !exists {
			return nil, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		}
		if objectType.KNID != "" && objectType.KNID != knID {
			return nil, actionPermissionInvalid(ctx, "object type belongs to another knowledge network")
		}
		requirements = append(requirements, interfaces.PermissionRequirement{
			ResourceType: interfaces.PermissionResourceTypeObjectType,
			ResourceID:   knID + "/" + objectTypeID,
			Operation:    interfaces.PermissionOperationQueryData,
		})
	}

	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].ResourceType != requirements[j].ResourceType {
			return requirements[i].ResourceType < requirements[j].ResourceType
		}
		if requirements[i].ResourceID != requirements[j].ResourceID {
			return requirements[i].ResourceID < requirements[j].ResourceID
		}
		return requirements[i].Operation < requirements[j].Operation
	})
	return requirements, nil
}

func validateActionResourceID(knID, childID string) error {
	if strings.TrimSpace(knID) == "" || strings.TrimSpace(childID) == "" {
		return fmt.Errorf("knowledge-network and child resource ids are required")
	}
	if strings.ContainsAny(knID, "/*") || strings.ContainsAny(childID, "/*") {
		return fmt.Errorf("authorization resource ids cannot contain slash or wildcard")
	}
	return nil
}

func validateStandaloneActionResourceID(resourceID string) error {
	if strings.TrimSpace(resourceID) == "" {
		return fmt.Errorf("resource id is required")
	}
	if strings.Contains(resourceID, "*") {
		return fmt.Errorf("authorization resource ids cannot contain wildcard")
	}
	return nil
}

func actionPermissionInvalid(ctx context.Context, detail string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest,
		oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails(detail)
}

func actionPermissionUnavailable(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(err.Error())
}
