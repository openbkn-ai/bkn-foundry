// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"ontology-query/common"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

func (s *actionSchedulerService) authorizeActionType(ctx context.Context, knID string,
	actionType *interfaces.ActionType) ([]interfaces.PermissionRequirement, error) {
	if !common.GetAuthEnabled() {
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
	if !common.GetAuthEnabled() {
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

	requirements := []interfaces.PermissionRequirement{
		{
			ResourceType: interfaces.PermissionResourceTypeKnowledgeNetwork,
			ResourceID:   knID,
			Operation:    interfaces.PermissionOperationExecute,
		},
		{
			ResourceType: interfaces.PermissionResourceTypeActionType,
			ResourceID:   knID + "/" + actionType.ATID,
			Operation:    interfaces.PermissionOperationExecute,
		},
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

func (s *actionSchedulerService) resolveActionProxyContext(
	ctx context.Context, knID string, actionType *interfaces.ActionType,
) (*interfaces.TrustedProxyContext, []interfaces.PermissionRequirement, error) {
	binding, requirement, err := actionProxyBinding(ctx, knID, actionType)
	if err != nil {
		return nil, nil, err
	}
	if s == nil || s.proxy == nil {
		return nil, nil, actionPermissionUnavailable(ctx, fmt.Errorf("knowledge network proxy resolver is not configured"))
	}
	proxy, err := s.proxy.Resolve(ctx, binding)
	if err != nil {
		return nil, nil, err
	}
	return proxy, []interfaces.PermissionRequirement{requirement}, nil
}

func actionProxyBinding(ctx context.Context, knID string,
	actionType *interfaces.ActionType) (interfaces.TrustedProxyBinding, interfaces.PermissionRequirement, error) {
	if actionType == nil {
		return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{},
			actionPermissionInvalid(ctx, "action type is required")
	}
	if err := validateActionResourceID(knID, actionType.ATID); err != nil {
		return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{}, actionPermissionInvalid(ctx, err.Error())
	}

	targetType := ""
	targetID := ""
	switch actionType.ActionSource.Type {
	case interfaces.ActionSourceTypeTool:
		targetType = interfaces.ProxyTargetTypeToolBox
		targetID = actionType.ActionSource.BoxID
		if err := validateStandaloneActionResourceID(actionType.ActionSource.ToolID); err != nil {
			return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{},
				actionPermissionInvalid(ctx, "tool: "+err.Error())
		}
	case interfaces.ActionSourceTypeMCP:
		targetType = interfaces.ProxyTargetTypeMCP
		targetID = actionType.ActionSource.McpID
		if strings.TrimSpace(actionType.ActionSource.ToolName) == "" && strings.TrimSpace(actionType.ActionSource.ToolID) == "" {
			return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{},
				actionPermissionInvalid(ctx, "MCP tool name is required")
		}
	default:
		return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{},
			actionPermissionInvalid(ctx, fmt.Sprintf("unsupported action source type: %s", actionType.ActionSource.Type))
	}
	if err := validateStandaloneActionResourceID(targetID); err != nil {
		return interfaces.TrustedProxyBinding{}, interfaces.PermissionRequirement{},
			actionPermissionInvalid(ctx, targetType+": "+err.Error())
	}

	binding := interfaces.TrustedProxyBinding{
		KNID:       knID,
		ChildType:  interfaces.PermissionResourceTypeActionType,
		ChildID:    actionType.ATID,
		TargetType: targetType,
		TargetID:   targetID,
		Operation:  interfaces.PermissionOperationExecute,
	}
	requirement := interfaces.PermissionRequirement{
		ResourceType: targetType,
		ResourceID:   targetID,
		Operation:    interfaces.PermissionOperationExecute,
	}
	return binding, requirement, nil
}

func trustedActionProxyContext(execution *interfaces.ActionExecution,
	actionType *interfaces.ActionType) (*interfaces.TrustedProxyContext, error) {
	if execution == nil || actionType == nil {
		return nil, fmt.Errorf("execution and action type are required")
	}
	binding, requirement, err := actionProxyBinding(context.Background(), execution.KNID, actionType)
	if err != nil {
		return nil, err
	}
	if execution.Executor.ID == "" || execution.Executor.Type == "" ||
		execution.Proxy == nil || execution.Proxy.ID == "" || execution.Proxy.Type != interfaces.ProxyAccountTypeApp ||
		execution.ProxyVersion <= 0 || execution.ProxyModelVersion == "" ||
		len(execution.ProxyPermissionSnapshot) != 1 || execution.ProxyPermissionSnapshot[0] != requirement {
		return nil, fmt.Errorf("execution dual-principal snapshot is incomplete")
	}
	return &interfaces.TrustedProxyContext{
		Caller:                execution.Executor,
		Proxy:                 *execution.Proxy,
		ProxyVersion:          execution.ProxyVersion,
		PublishedModelVersion: execution.ProxyModelVersion,
		Binding:               binding,
		ExecutionID:           execution.ID,
	}, nil
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
