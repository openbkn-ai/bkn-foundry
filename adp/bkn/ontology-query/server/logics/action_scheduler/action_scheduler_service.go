// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_logs"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/permission"
)

var (
	assOnce    sync.Once
	assService interfaces.ActionSchedulerService
)

type actionSchedulerService struct {
	appSetting    *common.AppSetting
	omAccess      interfaces.OntologyManagerAccess
	aoAccess      interfaces.AgentOperatorAccess
	logsService   interfaces.ActionLogsService
	ots           interfaces.ObjectTypeService
	permissions   interfaces.ActionExecutionPermissionService
	proxy         interfaces.ProxyContextResolver
	functionTrace managedFunctionTrace

	duplicateCheckHook interfaces.DuplicateCheckHook
}

// NewActionSchedulerService creates a singleton instance of ActionSchedulerService
func NewActionSchedulerService(appSetting *common.AppSetting) interfaces.ActionSchedulerService {
	assOnce.Do(func() {
		svc := &actionSchedulerService{
			appSetting:  appSetting,
			omAccess:    logics.OMA,
			aoAccess:    logics.AOA,
			logsService: action_logs.NewActionLogsService(appSetting),
			ots:         object_type.NewObjectTypeService(appSetting),
			proxy:       logics.PCR,
		}
		svc.permissions = permission.NewPermissionService(appSetting)
		svc.functionTrace = newManagedFunctionTraceFromEnv()
		// Default duplicate strategy: reject same kn + action type + instance set + dynamic_params while in-flight within the window.
		svc.duplicateCheckHook = svc.defaultDuplicateCheck
		assService = svc
	})
	return assService
}

// CheckActionExecution verifies the current subject against the trusted,
// published action dependencies without reading instance data or invoking the action.
func (s *actionSchedulerService) CheckActionExecution(ctx context.Context, req *interfaces.ActionExecutionRequest) error {
	if req == nil {
		return actionPermissionInvalid(ctx, "action execution request is required")
	}
	if req.Branch == "" {
		req.Branch = interfaces.MAIN_BRANCH
	}
	if req.Branch != interfaces.MAIN_BRANCH {
		return actionPermissionInvalid(ctx, "only the published main branch can execute actions")
	}
	if s == nil || s.omAccess == nil {
		return actionPermissionUnavailable(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	actionType, _, exists, err := s.omAccess.GetActionType(ctx, req.KNID, interfaces.MAIN_BRANCH, req.ActionTypeID)
	if err != nil {
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return versionErr
		}
		return actionPermissionUnavailable(ctx, err)
	}
	if !exists {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound)
	}
	if err := validateActionOperation(&actionType); err != nil {
		return actionTargetError(ctx, err.Error())
	}
	if missing := logics.MissingActionInputDynamicParamNames(&actionType, req.DynamicParams); len(missing) > 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ActionDynamicParamsMissing", map[string]any{
			"actionType": actionType.ATName, "parameters": logics.FormatMissingParamNames(missing),
		})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails(localizedText)
	}
	_, err = s.authorizeActionType(ctx, req.KNID, &actionType)
	if err != nil {
		return err
	}
	if interfaces.KNVersionPinFromContext(ctx).UsesCallerIdentity() {
		// The draft and history versions run as the caller: there is no proxy binding to check.
		return nil
	}
	_, _, err = s.resolveActionProxyContext(ctx, req.KNID, &actionType)
	return err
}

// ExecuteAction starts async action execution and returns execution_id immediately
func (s *actionSchedulerService) ExecuteAction(ctx context.Context, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionResponse, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ExecuteAction")
	defer span.End()
	if req == nil {
		return nil, actionPermissionInvalid(ctx, "action execution request is required")
	}
	if req.Branch == "" {
		req.Branch = interfaces.MAIN_BRANCH
	}
	if req.Branch != interfaces.MAIN_BRANCH {
		return nil, actionPermissionInvalid(ctx, "only the published main branch can execute actions")
	}

	span.SetAttributes(
		attr.Key("kn_id").String(req.KNID),
		attr.Key("action_type_id").String(req.ActionTypeID),
	)

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
	instances, objDatas, err := s.getInstancesForAction(ctx, &actionType, req)
	if err != nil {
		return nil, err
	}

	// Set instances and object data to request
	req.Instances = instances
	req.ObjDatas = objDatas
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
	span.SetAttributes(attr.Key("instances_count").Int(len(req.Instances)))

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
	span.SetAttributes(attr.Key("execution_mode").String(executionMode))

	// Generate execution ID
	generatedExecutionID, err := uuid.NewV7()
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(fmt.Sprintf("failed to generate execution UUIDv7: %v", err))
	}
	executionID := generatedExecutionID.String()
	actionProxy.ExecutionID = executionID
	var proxySubject *interfaces.AccountInfo
	if !callerIdentity {
		subject := actionProxy.Proxy
		proxySubject = &subject
	}
	now := time.Now().UnixMilli()

	// Determine trigger type (default to manual if not specified)
	triggerType := req.TriggerType
	if triggerType == "" {
		triggerType = interfaces.TriggerTypeManual
	}

	// Create execution record with metadata only (no Results to save space)
	// Results will be stored incrementally during execution
	execution := &interfaces.ActionExecution{
		ID:                      executionID,
		KNID:                    req.KNID,
		ActionTypeID:            actionType.ATID,
		ActionTypeName:          actionType.ATName,
		ActionSourceType:        actionType.ActionSource.Type,
		ActionSource:            actionType.ActionSource,
		ObjectTypeID:            actionType.ObjectTypeID,
		TriggerType:             triggerType,
		Status:                  interfaces.ExecutionStatusPending,
		ExecutionMode:           executionMode,
		TargetCount:             len(req.Instances),
		TotalCount:              invocationCount,
		SuccessCount:            0,
		FailedCount:             0,
		Results:                 []interfaces.ObjectExecutionResult{}, // Empty initially to save space
		DynamicParams:           req.DynamicParams,
		ExecutorID:              executor.ID, // deprecated, kept for backward compatibility
		Executor:                executor,    // full executor info
		StartTime:               now,
		ActionTypeSnapshot:      actionTypeSnapshot, // Save the action type configuration snapshot used during execution.
		PermissionSnapshot:      permissionSnapshot,
		Proxy:                   proxySubject,
		ProxyVersion:            actionProxy.ProxyVersion,
		ProxyModelVersion:       actionProxy.PublishedModelVersion,
		ProxyPermissionSnapshot: proxyPermissionSnapshot,
		InstanceIdentityHash:    instanceHash,
	}

	// Save initial execution record (metadata only)
	if err := s.logsService.CreateExecution(ctx, execution); err != nil {
		logger.Errorf("Failed to create execution record: %v", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(err.Error())
	}

	// Start async execution in goroutine. The caller's credential is carried in
	// memory only: it is never part of the execution record.
	callerCredential, _ := interfaces.CallerRuntimeCredentialFromContext(ctx)
	go s.executeAsync(execution, &actionType, req, callerCredential)

	// Return immediate response
	return &interfaces.ActionExecutionResponse{
		ExecutionID: executionID,
		Status:      interfaces.ExecutionStatusPending,
		Message:     "Action execution started",
		CreatedAt:   now,
	}, nil
}

// GetExecution retrieves execution status and results
func (s *actionSchedulerService) GetExecution(ctx context.Context, knID, executionID string) (*interfaces.ActionExecution, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExecution")
	defer span.End()

	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("execution_id").String(executionID),
	)

	query := &interfaces.ActionLogDetailQuery{
		KNID:         knID,
		LogID:        executionID,
		ResultsLimit: 10000, // Get all results for internal use
	}
	exec, err := s.logsService.GetExecution(ctx, query)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ExecutionNotFound).
			WithErrorDetails(err.Error())
	}

	return exec, nil
}

// Batch size for incremental result storage
const batchSize = 100

// executeAsync executes the action asynchronously with batch storage and cancellation support
func (s *actionSchedulerService) executeAsync(execution *interfaces.ActionExecution,
	actionType *interfaces.ActionType, req *interfaces.ActionExecutionRequest,
	callerCredential interfaces.CallerRuntimeCredential) {

	// Create a new context for async execution
	ctx := context.Background()
	// Restore account info from execution record for downstream API calls (user_id header)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, execution.Executor)
	// A Function-backed action reads BKN as the caller who started it.
	ctx = interfaces.WithCallerRuntimeCredential(ctx, callerCredential)
	traceOutcome := "handed_off"
	if s.functionTrace == nil && callerCredential.ConversationID != "" && callerCredential.InteractionID != "" &&
		callerCredential.ParentOperationID != "" {
		// The HTTP request is gone by the time the worker invokes a Function.
		// Restore only the managed identifiers captured at the authenticated boundary.
		ctx = common.SetTraceContextToCtx(ctx, common.TraceContext{
			ConversationID: callerCredential.ConversationID,
			InteractionID:  callerCredential.InteractionID,
			OperationID:    callerCredential.ParentOperationID,
		})
	}
	if execution.Proxy == nil && strings.HasPrefix(execution.ProxyModelVersion, interfaces.CallerIdentityModelPrefix) {
		// Started on the draft or a history version: the tool runs as the caller who started it.
		ctx = interfaces.WithCallerIdentityExecution(ctx)
	} else if proxyContext, err := trustedActionProxyContext(execution, actionType); err == nil {
		ctx = interfaces.WithTrustedProxyContext(ctx, proxyContext)
	} else {
		logger.Errorf("Execution %s has an invalid proxy snapshot: %v", execution.ID, err)
	}
	logger.Infof("Starting async execution: %s, mode: %s, target instances: %d",
		execution.ID, execution.ExecutionMode, len(req.Instances))

	// Update status to running; an execution cancelled before it started stays cancelled.
	if err := s.logsService.MarkExecutionRunning(ctx, execution.KNID, execution.ID); err != nil {
		logger.Warnf("Failed to update execution status to running: %v", err)
	}
	if s.functionTrace != nil && actionType.ActionSource.Type == interfaces.ActionSourceTypeTool {
		if proxy, ok := interfaces.TrustedProxyContextFromContext(ctx); ok &&
			proxy.Binding.TargetType == interfaces.ProxyTargetTypeFunction &&
			!s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
			if err := s.authorizeExecution(ctx, execution.PermissionSnapshot); err != nil {
				s.failManagedFunctionExecution(ctx, execution, req, err)
				return
			}
			managedCtx, finish, err := s.functionTrace.Open(ctx, execution, callerCredential)
			if err != nil {
				logger.Errorf("Failed to start managed action Function context for execution %s: %v", execution.ID, err)
				s.failManagedFunctionExecution(ctx, execution, req, err)
				return
			}
			ctx = managedCtx
			defer func() { finish(traceOutcome) }()
		}
	}

	if execution.ExecutionMode == interfaces.ExecutionModeOnce {
		switch s.executeOnce(ctx, execution, actionType, req) {
		case interfaces.ExecutionStatusFailed:
			traceOutcome = "failed"
		case interfaces.ExecutionStatusCancelled:
			traceOutcome = "cancelled"
		}
		return
	}

	// Execute objects in batches
	successCount := 0
	failedCount := 0
	cancelledCount := 0
	results := resultBuffer{}
	cancelled := false
	var lastCancellationCheck time.Time

	for i, objData := range req.ObjDatas {
		if shouldCheckExecutionCancellation(i, len(req.ObjDatas), time.Since(lastCancellationCheck)) {
			lastCancellationCheck = time.Now()
			if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
				logger.Infof("Execution %s cancelled, stopping at object %d/%d", execution.ID, i, len(req.ObjDatas))
				cancelled = true
				// Mark remaining objects as cancelled
				for j := i; j < len(req.Instances); j++ {
					results.add(interfaces.ObjectExecutionResult{
						ObjectSystemInfo: req.Instances[j],
						Status:           interfaces.ObjectStatusCancelled,
						ErrorMessage:     "execution cancelled",
					})
					cancelledCount++
				}
				break
			}
		}

		startTime := time.Now().UnixMilli()

		// Build parameters for this object
		params, err := s.buildExecutionParams(actionType, objData, req.DynamicParams, req.Instances[i].InstanceIdentity)
		if err != nil {
			endTime := time.Now().UnixMilli()
			results.add(interfaces.ObjectExecutionResult{
				ObjectSystemInfo: req.Instances[i],
				Status:           interfaces.ObjectStatusFailed,
				ErrorMessage:     fmt.Sprintf("Failed to build parameters: %v", err),
				StartTime:        startTime,
				EndTime:          endTime,
				DurationMs:       endTime - startTime,
			})
			failedCount++
			continue
		}

		// Execute based on action source type
		params, result, execErr := s.invokeActionSource(withActionInvocation(ctx, i), execution.PermissionSnapshot, actionType, params)

		endTime := time.Now().UnixMilli()
		if execErr != nil {
			results.add(interfaces.ObjectExecutionResult{
				ObjectSystemInfo: req.Instances[i],
				Status:           interfaces.ObjectStatusFailed,
				Parameters:       params,
				Result:           result,
				ErrorMessage:     execErr.Error(),
				StartTime:        startTime,
				EndTime:          endTime,
				DurationMs:       endTime - startTime,
			})
			failedCount++
		} else {
			results.add(interfaces.ObjectExecutionResult{
				ObjectSystemInfo: req.Instances[i],
				Status:           interfaces.ObjectStatusSuccess,
				Parameters:       params,
				Result:           result,
				StartTime:        startTime,
				EndTime:          endTime,
				DurationMs:       endTime - startTime,
			})
			successCount++
		}

		completedCount := i + 1
		if shouldUpdateExecutionProgress(completedCount, len(req.ObjDatas)) {
			s.flushResults(ctx, execution, &results)
			s.updateExecutionProgress(ctx, execution, successCount, failedCount)
			logger.Debugf("Execution %s progress: %d/%d completed", execution.ID, completedCount, len(req.ObjDatas))
		}
	}

	// Determine final status
	var finalStatus string
	if cancelled {
		finalStatus = interfaces.ExecutionStatusCancelled
	} else if failedCount == len(req.Instances) {
		finalStatus = interfaces.ExecutionStatusFailed
	} else {
		finalStatus = interfaces.ExecutionStatusCompleted
	}
	if finalStatus == interfaces.ExecutionStatusFailed {
		traceOutcome = "failed"
	} else if finalStatus == interfaces.ExecutionStatusCancelled {
		traceOutcome = "cancelled"
	}

	endTime := time.Now().UnixMilli()

	// Store what is still pending (the last partial batch, or every instance the cancel
	// skipped) before the terminal write, so a finished execution has all its results.
	s.flushFinalResults(ctx, execution, &results)

	// Write the final execution record. A cancel that lands after the last cancellation
	// check still wins: the log keeps the cancelled status the user was told about.
	if err := s.logsService.FinishExecution(ctx, execution.KNID, execution.ID, &interfaces.ExecutionOutcome{
		Status:       finalStatus,
		SuccessCount: successCount,
		FailedCount:  failedCount,
		EndTime:      endTime,
		DurationMs:   endTime - execution.StartTime,
	}); err != nil {
		logger.Errorf("Failed to update execution record: %v", err)
	}

	logger.Infof("Completed async execution: %s, success: %d, failed: %d, cancelled: %d",
		execution.ID, successCount, failedCount, cancelledCount)
}

func (s *actionSchedulerService) failManagedFunctionExecution(ctx context.Context,
	execution *interfaces.ActionExecution, req *interfaces.ActionExecutionRequest, cause error) {
	now := time.Now().UnixMilli()
	results := resultBuffer{}
	if execution.ExecutionMode == interfaces.ExecutionModeOnce {
		results.add(interfaces.ObjectExecutionResult{
			Targets: req.Instances, Status: interfaces.ObjectStatusFailed,
			ErrorMessage: cause.Error(), StartTime: now, EndTime: now,
		})
	} else {
		for _, target := range req.Instances {
			results.add(interfaces.ObjectExecutionResult{
				ObjectSystemInfo: target, Status: interfaces.ObjectStatusFailed,
				ErrorMessage: cause.Error(), StartTime: now, EndTime: now,
			})
		}
	}
	s.flushFinalResults(ctx, execution, &results)
	if err := s.logsService.FinishExecution(ctx, execution.KNID, execution.ID, &interfaces.ExecutionOutcome{
		Status: interfaces.ExecutionStatusFailed, FailedCount: execution.TotalCount,
		EndTime: now, DurationMs: now - execution.StartTime,
	}); err != nil {
		logger.Errorf("Failed to record managed Function setup failure for execution %s: %v", execution.ID, err)
	}
}

// resolveExecutionMode runs object and relation operations for each explicit target.
// Independent tool calls have one execution context and one invocation.
func resolveExecutionMode(actionType *interfaces.ActionType) string {
	if actionType.OperationConfig != nil && actionType.OperationConfig.Type != "tool" {
		return interfaces.ExecutionModePerInstance
	}
	return interfaces.ExecutionModeOnce
}

// invokeActionSource runs the configured action source once and returns the parameters that
// were actually submitted, so the recorded result reflects the real request payload.
func (s *actionSchedulerService) invokeActionSource(ctx context.Context,
	permissionSnapshot []interfaces.PermissionRequirement, actionType *interfaces.ActionType,
	params map[string]any) (map[string]any, any, error) {
	if err := s.authorizeExecution(ctx, permissionSnapshot); err != nil {
		return params, nil, err
	}
	if _, ok := interfaces.TrustedProxyContextFromContext(ctx); !ok {
		return params, nil, actionPermissionUnavailable(ctx, fmt.Errorf("execution proxy snapshot is missing"))
	}

	switch actionType.ActionSource.Type {
	case interfaces.ActionSourceTypeTool:
		if s.functionTrace != nil {
			if proxy, ok := interfaces.TrustedProxyContextFromContext(ctx); ok &&
				proxy.Binding.TargetType == interfaces.ProxyTargetTypeFunction {
				result, err := s.functionTrace.Run(ctx, actionType.ActionSource.ToolID, func(runCtx context.Context) (any, error) {
					return ExecuteTool(runCtx, s.aoAccess, actionType, params)
				})
				return params, result, err
			}
		}
		result, err := ExecuteTool(ctx, s.aoAccess, actionType, params)
		return params, result, err
	case interfaces.ActionSourceTypeMCP:
		result, err := ExecuteMCP(ctx, s.aoAccess, actionType, params)
		return params, result, err
	default:
		return params, nil, fmt.Errorf("unsupported action source type: %s", actionType.ActionSource.Type)
	}
}

// executeOnce handles ExecutionModeOnce: one invocation covering every matched instance.
// The matched instances are recorded as the targets of that invocation, so the execution log
// still shows what the action was aimed at.
func (s *actionSchedulerService) executeOnce(ctx context.Context, execution *interfaces.ActionExecution,
	actionType *interfaces.ActionType, req *interfaces.ActionExecutionRequest) string {

	startTime := time.Now().UnixMilli()
	localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ActionInstancesMerged", map[string]any{"count": len(req.Instances)})
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	result := interfaces.ObjectExecutionResult{
		ObjectSystemInfo: interfaces.ObjectSystemInfo{
			InstanceIdentity: map[string]any{},
			Display:          localizedText,
		},
		Targets:   req.Instances,
		StartTime: startTime,
	}

	successCount := 0
	failedCount := 0

	// The per-instance loop checks for cancellation before each invocation; the single
	// invocation of this mode needs the same guard, otherwise a cancelled execution would
	// still fire its tool call.
	if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
		logger.Infof("Execution %s cancelled before its single invocation was issued", execution.ID)
		result.Status = interfaces.ObjectStatusCancelled
		result.ErrorMessage = "execution cancelled"
		endTime := time.Now().UnixMilli()
		result.EndTime = endTime
		result.DurationMs = endTime - startTime
		s.finishOnce(ctx, execution, result, interfaces.ExecutionStatusCancelled, 0, 0, endTime)
		return interfaces.ExecutionStatusCancelled
	}

	// No parameter reads an instance property in this mode, so an empty object payload
	// resolves exactly the parameters any matched instance would have resolved.
	params, err := s.buildExecutionParams(actionType, map[string]any{}, req.DynamicParams)
	if err != nil {
		result.Status = interfaces.ObjectStatusFailed
		result.ErrorMessage = fmt.Sprintf("Failed to build parameters: %v", err)
		failedCount = 1
	} else {
		sentParams, invokeResult, execErr := s.invokeActionSource(withActionInvocation(ctx, 0), execution.PermissionSnapshot, actionType, params)
		result.Parameters = sentParams
		if execErr != nil {
			result.Status = interfaces.ObjectStatusFailed
			result.Result = invokeResult
			result.ErrorMessage = execErr.Error()
			failedCount = 1
		} else {
			result.Status = interfaces.ObjectStatusSuccess
			result.Result = invokeResult
			successCount = 1
		}
	}

	endTime := time.Now().UnixMilli()
	result.EndTime = endTime
	result.DurationMs = endTime - startTime

	finalStatus := interfaces.ExecutionStatusCompleted
	if failedCount > 0 {
		finalStatus = interfaces.ExecutionStatusFailed
	}
	// A cancel that lands while the tool call is in flight must not be overwritten by the
	// terminal status of a call the user already asked to stop. The invocation happened, so
	// its result is still recorded — only the execution status stays cancelled.
	if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
		logger.Infof("Execution %s was cancelled while its single invocation was in flight", execution.ID)
		finalStatus = interfaces.ExecutionStatusCancelled
	}

	s.finishOnce(ctx, execution, result, finalStatus, successCount, failedCount, endTime)

	logger.Infof("Completed aggregated execution: %s, targets: %d, invocations: 1, status: %s",
		execution.ID, len(req.Instances), finalStatus)
	return finalStatus
}

// finishOnce writes the terminal record of an ExecutionModeOnce run.
func (s *actionSchedulerService) finishOnce(ctx context.Context, execution *interfaces.ActionExecution,
	result interfaces.ObjectExecutionResult, finalStatus string, successCount, failedCount int, endTime int64) {

	results := resultBuffer{pending: []interfaces.ObjectExecutionResult{result}}
	s.flushFinalResults(ctx, execution, &results)

	if err := s.logsService.FinishExecution(ctx, execution.KNID, execution.ID, &interfaces.ExecutionOutcome{
		Status:       finalStatus,
		SuccessCount: successCount,
		FailedCount:  failedCount,
		EndTime:      endTime,
		DurationMs:   endTime - execution.StartTime,
	}); err != nil {
		logger.Errorf("Failed to update execution record: %v", err)
	}
}

// cancellationCheckInterval bounds how long a cancel can go unnoticed while a large
// execution keeps invoking; the check reads the status field only, so it stays cheap.
const cancellationCheckInterval = time.Second

func shouldCheckExecutionCancellation(index, total int, sinceLastCheck time.Duration) bool {
	return index == 0 || total <= batchSize || index%batchSize == 0 || sinceLastCheck >= cancellationCheckInterval
}

func shouldUpdateExecutionProgress(completed, total int) bool {
	return total <= batchSize || completed%batchSize == 0
}

// isExecutionCancelled checks if the execution has been cancelled. It reads the status
// field only, so the check stays cheap however many results the execution has.
func (s *actionSchedulerService) isExecutionCancelled(ctx context.Context, knID, execID string) bool {
	status, err := s.logsService.GetExecutionStatus(ctx, knID, execID)
	if err != nil {
		logger.Warnf("Failed to check execution status: %v", err)
		return false
	}
	return status == interfaces.ExecutionStatusCancelled
}

// updateExecutionProgress updates the execution progress counters (batch update)
func (s *actionSchedulerService) updateExecutionProgress(ctx context.Context, execution *interfaces.ActionExecution, successCount, failedCount int) {
	if err := s.logsService.UpdateExecutionProgress(ctx, execution.KNID, execution.ID, &interfaces.ExecutionProgress{
		SuccessCount: successCount,
		FailedCount:  failedCount,
	}); err != nil {
		logger.Warnf("Failed to update execution progress: %v", err)
	}
}

// resultBuffer holds the results produced since the last flush. stored counts the results
// already written, which is also the position of pending[0] within the execution.
type resultBuffer struct {
	pending []interfaces.ObjectExecutionResult
	stored  int
}

func (b *resultBuffer) add(result interfaces.ObjectExecutionResult) {
	b.pending = append(b.pending, result)
}

// flushResults appends the pending results to the results index. Results that fail to store
// stay pending and are retried at the same positions on the next flush, which overwrites
// rather than duplicates them.
func (s *actionSchedulerService) flushResults(ctx context.Context, execution *interfaces.ActionExecution, results *resultBuffer) {
	if len(results.pending) == 0 {
		return
	}
	if err := s.logsService.AppendResults(ctx, execution.KNID, execution.ID, results.stored, results.pending); err != nil {
		logger.Warnf("Failed to store %d results of execution %s: %v", len(results.pending), execution.ID, err)
		return
	}
	results.stored += len(results.pending)
	results.pending = nil
}

// finalFlushBackoff is the wait before each retry of the last flush of an execution. Earlier
// flushes are retried by the next one; the last flush has no next one.
var finalFlushBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

// flushFinalResults stores the results still pending when an execution stops, retrying with
// backoff. Results that still cannot be stored are reported: the terminal write that follows
// records the counters of what ran, and the results page would otherwise silently miss them.
func (s *actionSchedulerService) flushFinalResults(ctx context.Context, execution *interfaces.ActionExecution, results *resultBuffer) {
	s.flushResults(ctx, execution, results)
	for _, wait := range finalFlushBackoff {
		if len(results.pending) == 0 {
			return
		}
		time.Sleep(wait)
		s.flushResults(ctx, execution, results)
	}
	if len(results.pending) > 0 {
		logger.Errorf("Execution %s finished with %d results that could not be stored (positions %d-%d)",
			execution.ID, len(results.pending), results.stored, results.stored+len(results.pending)-1)
	}
}

// buildExecutionParams builds the execution parameters from action type parameters and object data.
// Uses getNestedValue to support dot-separated nested parameter names (e.g. "props.headers").
func (s *actionSchedulerService) buildExecutionParams(actionType *interfaces.ActionType,
	instance map[string]any, dynamicParams map[string]any, identities ...map[string]any) (map[string]any, error) {
	params := make(map[string]any)
	for _, param := range actionType.Parameters {
		var value any
		switch param.ValueFrom {
		case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
			propName, ok := param.Value.(string)
			if !ok || propName == "" {
				return nil, fmt.Errorf("parameter %q requires a property name", param.Name)
			}
			value = getNestedValue(instance, propName)
			if value == nil {
				return nil, fmt.Errorf("missing property %q for parameter %q", propName, param.Name)
			}
		case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
			value = param.Value
		case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
			value = logics.ActionDynamicParamGetValue(dynamicParams, param.Name)
			if value == nil {
				return nil, fmt.Errorf("missing dynamic input %q", param.Name)
			}
		case "source_identity", "target_identity":
			if actionType.OperationConfig == nil || actionType.OperationConfig.Type != "relation" || len(identities) != 1 {
				return nil, fmt.Errorf("parameter %q requires a resolved relation target", param.Name)
			}
			endpoint := "source"
			if param.ValueFrom == "target_identity" {
				endpoint = "target"
			}
			identity, ok := identities[0][endpoint].(map[string]any)
			if !ok || len(identity) != 1 {
				return nil, fmt.Errorf("parameter %q requires one endpoint primary key", param.Name)
			}
			for _, item := range identity {
				value = item
			}
			if value == nil {
				return nil, fmt.Errorf("parameter %q has a missing endpoint identity", param.Name)
			}
		default:
			return nil, fmt.Errorf("unsupported value_from %q for parameter %q", param.ValueFrom, param.Name)
		}
		if err := logics.ValidateActionValue(param.Type, value); err != nil {
			return nil, fmt.Errorf("parameter %q: %w", param.Name, err)
		}
		setNestedValue(params, param.Name, value)
	}
	return params, nil
}
