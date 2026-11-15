// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.
//
// Admission guards extracted from action_scheduler_service.go. The original
// attribution is retained with the moved implementation.

package action_scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"net/http"
	"time"
)

type preparedAction struct {
	action           interfaces.ActionType
	snapshot         map[string]any
	executor         interfaces.AccountInfo
	permissions      []interfaces.PermissionRequirement
	proxy            *interfaces.TrustedProxyContext
	proxyPermissions []interfaces.PermissionRequirement
	callerIdentity   bool
	mode             string
	invocationCount  int
	fingerprint      string
	observation      *interfaces.ActionExecutionInspection
}

func observePreparation(req *interfaces.ActionExecutionRequest, phase string) {
	if req != nil && req.Inspection != nil {
		req.Inspection.Phase = phase
	}
}

// The normal executor and inspection share every admission guard. Inspection
// never allocates an execution ID, creates a task or invokes an external source.
func (s *actionSchedulerService) prepareActionExecution(ctx context.Context, req *interfaces.ActionExecutionRequest) (*preparedAction, error) {
	if req != nil && req.Inspection == nil {
		req.Inspection = &interfaces.ActionExecutionInspection{}
	}
	if req == nil {
		return nil, actionPermissionInvalid(ctx, "action execution request is required")
	}
	observePreparation(req, "input")
	if req.Branch == "" {
		req.Branch = interfaces.MAIN_BRANCH
	}
	if req.Branch != interfaces.MAIN_BRANCH {
		return nil, actionPermissionInvalid(ctx, "only the published main branch can execute actions")
	}

	observePreparation(req, "definition")
	// Load the published single-operation definition before target resolution.
	actionType, actionTypeSnapshot, exists, err := s.omAccess.GetActionType(ctx, req.KNID, req.Branch, req.ActionTypeID)
	if err != nil {
		logger.Errorf("Failed to get action type: %v", err)
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return nil, versionErr
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_GetActionTypeFailed).
			WithErrorDetails(err.Error())
	}
	if !exists {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound).
			WithErrorDetails(fmt.Sprintf("Action type not found: %s", req.ActionTypeID))
	}

	if err := validateActionOperation(&actionType); err != nil {
		return nil, actionTargetError(ctx, err.Error())
	}
	observePreparation(req, "input")
	if missing := logics.MissingActionInputDynamicParamNames(&actionType, req.DynamicParams); len(missing) > 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ActionDynamicParamsMissing", map[string]any{
			"actionType": actionType.ATName, "parameters": logics.FormatMissingParamNames(missing),
		})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails(localizedText)
	}

	// Resolve the authenticated execution subject before any instance scan or
	// physical data read. The permission snapshot is entirely server-derived.
	executor := interfaces.AccountInfo{}
	if accountInfo, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ok {
		executor = accountInfo
	}
	observePreparation(req, "authorization")
	permissionSnapshot, err := s.authorizeActionType(ctx, req.KNID, &actionType)
	if err != nil {
		return nil, err
	}
	// The proxy's grants cover only the running version. On the draft or a history version the
	// action runs with the caller's own identity: no proxy, and the token records the version.
	var (
		actionProxy             = &interfaces.TrustedProxyContext{}
		proxyPermissionSnapshot []interfaces.PermissionRequirement
	)
	// Decided once: the instance read below can resolve the pin and change its answer.
	observePreparation(req, "proxy_authorization")
	pin := interfaces.KNVersionPinFromContext(ctx)
	callerIdentity := pin.UsesCallerIdentity()
	if callerIdentity {
		actionProxy.PublishedModelVersion = interfaces.CallerIdentityModelPrefix + pin.Token()
	} else {
		actionProxy, proxyPermissionSnapshot, err = s.resolveActionProxyContext(ctx, req.KNID, &actionType)
		if err != nil {
			return nil, err
		}
	}

	// Resolve the explicitly requested operation targets.
	observePreparation(req, "target")
	instances, objDatas, err := s.getInstancesForAction(ctx, &actionType, req)
	if err != nil {
		return nil, err
	}

	// Set instances and object data to request
	req.Instances = instances
	req.ObjDatas = objDatas
	observePreparation(req, "parameters")
	for index, data := range objDatas {
		if _, err := s.buildExecutionParams(&actionType, data, req.DynamicParams, instances[index].InstanceIdentity); err != nil {
			identity, _ := json.Marshal(instances[index].InstanceIdentity)
			return nil, actionTargetError(ctx, fmt.Sprintf("target %d identity %s: %v", index+1, identity, err))
		}
	}

	// Never accept an execution without a resolved target context.
	if len(req.Instances) == 0 {
		logger.Infof("No matching instances found for action type %s", req.ActionTypeID)
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails("No matching instances found for the action type condition")
	}

	logger.Infof("Resolved %d explicit targets for action type %s", len(req.Instances), req.ActionTypeID)

	// Check execution objects limit. A condition scan stops one object past the
	// limit, so the count only proves the match set is larger than the limit.
	if len(req.Instances) > logics.ActionExecutionMaxObjects {
		logger.Warnf("More than %d objects match action type %s", logics.ActionExecutionMaxObjects, req.ActionTypeID)
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("More than %d objects match this action, which exceeds the maximum limit. Please reduce the scope or adjust the ACTION_EXECUTION_MAX_OBJECTS environment variable.",
				logics.ActionExecutionMaxObjects))
	}

	instanceHash, err := computeDuplicateFingerprint(req.Instances, req.DynamicParams, actionType.OperationConfig, req.ObjectInput)
	if err != nil {
		logger.Errorf("Failed to compute duplicate fingerprint: %v", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(fmt.Sprintf("failed to compute duplicate fingerprint: %v", err))
	}
	req.InstanceIdentityHash = instanceHash
	req.Inspection.Fingerprint = instanceHash
	req.Inspection.Operation = actionType.OperationConfig

	observePreparation(req, "duplicate")
	// Duplicate check hook (default: reject in-flight same kn + action type + instance set + dynamic_params)
	if s.duplicateCheckHook != nil {
		proceed, dupErr := s.duplicateCheckHook(ctx, req)
		if dupErr != nil {
			if httpErr, ok := dupErr.(*httperrors.HTTPError); ok {
				return nil, httpErr
			}
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_QueryExecutionsFailed).
				WithErrorDetails(dupErr.Error())
		}
		if !proceed {
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_DuplicateExecution).
				WithErrorDetails(fmt.Sprintf(
					"Duplicate execution detected for action_type_id=%s within %ds window",
					req.ActionTypeID, duplicateWindowSeconds,
				))
		}
	}

	// Resolve how many times the tool actually has to be invoked for this execution.
	executionMode := resolveExecutionMode(&actionType)
	invocationCount := len(req.Instances)
	if executionMode == interfaces.ExecutionModeOnce {
		invocationCount = 1
		logger.Infof("Action type %s has no instance-dependent parameter, %d matched instances collapse into a single invocation",
			actionType.ATID, len(req.Instances))
	}

	observation := req.Inspection
	observation.KNID, observation.ActionTypeID, observation.ObservedAt = req.KNID, req.ActionTypeID, time.Now().UnixMilli()
	observation.Phase, observation.Decision = "complete", "accepted"
	observation.Mode, observation.TargetCount, observation.TotalCount = executionMode, len(req.Instances), invocationCount
	observation.Operation = actionType.OperationConfig
	observation.Fingerprint = instanceHash
	observation.DuplicateWindowSeconds = duplicateWindowSeconds
	observation.DuplicateCheckEnabled = s.duplicateCheckHook != nil && duplicateWindowSeconds > 0
	for _, target := range req.Instances {
		observation.Targets = append(observation.Targets, target.InstanceIdentity)
	}
	return &preparedAction{action: actionType, snapshot: actionTypeSnapshot, executor: executor, permissions: permissionSnapshot, proxy: actionProxy, proxyPermissions: proxyPermissionSnapshot, callerIdentity: callerIdentity, mode: executionMode, invocationCount: invocationCount, fingerprint: instanceHash, observation: observation}, nil
}

func (s *actionSchedulerService) InspectActionExecution(ctx context.Context, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionInspection, error) {
	if req == nil {
		return nil, actionPermissionInvalid(ctx, "action execution request is required")
	}
	req.Inspection = &interfaces.ActionExecutionInspection{KNID: req.KNID, ActionTypeID: req.ActionTypeID, ObservedAt: time.Now().UnixMilli()}
	_, err := s.prepareActionExecution(ctx, req)
	if err == nil {
		return req.Inspection, nil
	}
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return nil, err
	}
	incomplete := req.Inspection.Decision == "unavailable"
	req.Inspection.Decision = "unavailable"
	req.Inspection.ErrorCode = httpErr.ErrorCode
	if httpErr.HTTPCode == http.StatusForbidden && req.Inspection.Phase != "proxy_authorization" {
		req.Inspection.Phase = "authorization"
	}
	if !incomplete && (httpErr.HTTPCode == http.StatusBadRequest || httpErr.HTTPCode == http.StatusForbidden || httpErr.HTTPCode == http.StatusConflict || httpErr.HTTPCode == http.StatusNotFound) {
		req.Inspection.Decision = "rejected"
	}
	return req.Inspection, nil
}
