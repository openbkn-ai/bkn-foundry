// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	attr "go.opentelemetry.io/otel/attribute"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/locale"
	"ontology-query/logics"
	"ontology-query/logics/action_logs"
	"ontology-query/logics/object_type"
	"ontology-query/logics/permission"
)

// Environment variable for max execution objects limit
const (
	envMaxExecutionObjects     = "ACTION_EXECUTION_MAX_OBJECTS"
	defaultMaxExecutionObjects = 10000
)

// maxExecutionObjects is the maximum number of objects allowed in a single execution
var maxExecutionObjects = defaultMaxExecutionObjects

func init() {
	if val := os.Getenv(envMaxExecutionObjects); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			maxExecutionObjects = n
			logger.Infof("Action execution max objects limit set to %d", maxExecutionObjects)
		}
	}
}

var (
	assOnce    sync.Once
	assService interfaces.ActionSchedulerService
)

type actionSchedulerService struct {
	appSetting  *common.AppSetting
	omAccess    interfaces.OntologyManagerAccess
	aoAccess    interfaces.AgentOperatorAccess
	logsService interfaces.ActionLogsService
	ots         interfaces.ObjectTypeService
	permissions interfaces.ActionExecutionPermissionService

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
		}
		if common.GetAuthEnabled() && ActionExecutionPEPEnabled() {
			svc.permissions = permission.NewPermissionService(appSetting)
		}
		// Default duplicate strategy: reject same kn + action type + instance set + dynamic_params while in-flight within the window.
		svc.duplicateCheckHook = svc.defaultDuplicateCheck
		assService = svc
	})
	return assService
}

// CheckActionExecution verifies the current subject against the trusted,
// published action dependencies without reading instance data or invoking the action.
func (s *actionSchedulerService) CheckActionExecution(ctx context.Context, req *interfaces.ActionExecutionRequest) error {
	if !ActionExecutionPEPEnabled() {
		return nil
	}
	if req == nil || req.Branch != interfaces.MAIN_BRANCH {
		return actionPermissionInvalid(ctx, "only the published main branch can execute actions")
	}
	if s == nil || s.omAccess == nil {
		return actionPermissionUnavailable(ctx, fmt.Errorf("ontology model access is not configured"))
	}
	actionType, _, exists, err := s.omAccess.GetActionType(ctx, req.KNID, interfaces.MAIN_BRANCH, req.ActionTypeID)
	if err != nil {
		return actionPermissionUnavailable(ctx, err)
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound)
	}
	if missing := logics.MissingActionInputDynamicParamNames(&actionType, req.DynamicParams); len(missing) > 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "ActionDynamicParamsMissing", map[string]any{
				"actionType": actionType.ATName, "parameters": logics.FormatMissingParamNames(missing),
			}))
	}
	_, err = s.authorizeActionType(ctx, req.KNID, &actionType)
	return err
}

// ExecuteAction starts async action execution and returns execution_id immediately
func (s *actionSchedulerService) ExecuteAction(ctx context.Context, req *interfaces.ActionExecutionRequest) (*interfaces.ActionExecutionResponse, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ExecuteAction")
	defer span.End()

	span.SetAttributes(
		attr.Key("kn_id").String(req.KNID),
		attr.Key("action_type_id").String(req.ActionTypeID),
	)

	// Get action type from ontology-manager first (needed for both scan mode and normal mode)
	actionType, actionTypeSnapshot, exists, err := s.omAccess.GetActionType(ctx, req.KNID, req.Branch, req.ActionTypeID)
	if err != nil {
		logger.Errorf("Failed to get action type: %v", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_GetActionTypeFailed).
			WithErrorDetails(err.Error())
	}
	if !exists {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound).
			WithErrorDetails(fmt.Sprintf("Action type not found: %s", req.ActionTypeID))
	}

	if missing := logics.MissingActionInputDynamicParamNames(&actionType, req.DynamicParams); len(missing) > 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "ActionDynamicParamsMissing", map[string]any{
				"actionType": actionType.ATName, "parameters": logics.FormatMissingParamNames(missing),
			}))
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

	// Get instances based on action type configuration and request parameters
	instances, objDatas, err := s.getInstancesForAction(ctx, &actionType, req.KNID, req.Branch, req.InstanceIdentities)
	if err != nil {
		return nil, err
	}

	// Set instances and object data to request
	req.Instances = instances
	req.ObjDatas = objDatas

	// If no matching instances found after scanning, return appropriate response
	if len(req.Instances) == 0 {
		logger.Infof("No matching instances found for action type %s", req.ActionTypeID)
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails("No matching instances found for the action type condition")
	}

	logger.Infof("Found %d matching instances for action type %s", len(req.Instances), req.ActionTypeID)
	span.SetAttributes(attr.Key("instances_count").Int(len(req.Instances)))

	// Check execution objects limit
	if len(req.Instances) > maxExecutionObjects {
		logger.Warnf("Execution objects count %d exceeds limit %d", len(req.Instances), maxExecutionObjects)
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Number of objects (%d) exceeds the maximum limit (%d). Please reduce the scope or adjust the ACTION_EXECUTION_MAX_OBJECTS environment variable.",
				len(req.Instances), maxExecutionObjects))
	}

	instanceHash, err := computeDuplicateFingerprint(req.Instances, req.DynamicParams)
	if err != nil {
		logger.Errorf("Failed to compute duplicate fingerprint: %v", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(fmt.Sprintf("failed to compute duplicate fingerprint: %v", err))
	}
	req.InstanceIdentityHash = instanceHash

	// Duplicate check hook (default: reject in-flight same kn + action type + instance set + dynamic_params)
	if s.duplicateCheckHook != nil {
		proceed, dupErr := s.duplicateCheckHook(ctx, req)
		if dupErr != nil {
			if httpErr, ok := dupErr.(*rest.HTTPError); ok {
				return nil, httpErr
			}
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_QueryExecutionsFailed).
				WithErrorDetails(dupErr.Error())
		}
		if !proceed {
			return nil, rest.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_DuplicateExecution).
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
	executionID := xid.New().String()
	now := time.Now().UnixMilli()

	// Determine trigger type (default to manual if not specified)
	triggerType := req.TriggerType
	if triggerType == "" {
		triggerType = interfaces.TriggerTypeManual
	}

	// Create execution record with metadata only (no Results to save space)
	// Results will be stored incrementally during execution
	execution := &interfaces.ActionExecution{
		ID:                   executionID,
		KNID:                 req.KNID,
		ActionTypeID:         actionType.ATID,
		ActionTypeName:       actionType.ATName,
		ActionSourceType:     actionType.ActionSource.Type,
		ActionSource:         actionType.ActionSource,
		ObjectTypeID:         actionType.ObjectTypeID,
		TriggerType:          triggerType,
		Status:               interfaces.ExecutionStatusPending,
		ExecutionMode:        executionMode,
		TargetCount:          len(req.Instances),
		TotalCount:           invocationCount,
		SuccessCount:         0,
		FailedCount:          0,
		Results:              []interfaces.ObjectExecutionResult{}, // Empty initially to save space
		DynamicParams:        req.DynamicParams,
		ExecutorID:           executor.ID, // deprecated, kept for backward compatibility
		Executor:             executor,    // full executor info
		StartTime:            now,
		ActionTypeSnapshot:   actionTypeSnapshot, // Save the action type configuration snapshot used during execution.
		PermissionSnapshot:   permissionSnapshot,
		InstanceIdentityHash: instanceHash,
	}

	// Save initial execution record (metadata only)
	if err := s.logsService.CreateExecution(ctx, execution); err != nil {
		logger.Errorf("Failed to create execution record: %v", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(err.Error())
	}

	// Start async execution in goroutine
	go s.executeAsync(execution, &actionType, req)

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
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ExecutionNotFound).
			WithErrorDetails(err.Error())
	}

	return exec, nil
}

// Batch size for incremental result storage
const batchSize = 100

// executeAsync executes the action asynchronously with batch storage and cancellation support
func (s *actionSchedulerService) executeAsync(execution *interfaces.ActionExecution,
	actionType *interfaces.ActionType, req *interfaces.ActionExecutionRequest) {

	// Create a new context for async execution
	ctx := context.Background()
	// Restore account info from execution record for downstream API calls (user_id header)
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, execution.Executor)

	logger.Infof("Starting async execution: %s, mode: %s, target instances: %d",
		execution.ID, execution.ExecutionMode, len(req.Instances))

	// Update status to running
	if err := s.logsService.UpdateExecution(ctx, execution.KNID, execution.ID, map[string]any{
		"status": interfaces.ExecutionStatusRunning,
	}); err != nil {
		logger.Warnf("Failed to update execution status to running: %v", err)
	}

	if execution.ExecutionMode == interfaces.ExecutionModeOnce {
		s.executeOnce(ctx, execution, actionType, req)
		return
	}

	// Execute objects in batches
	successCount := 0
	failedCount := 0
	cancelledCount := 0
	allResults := []interfaces.ObjectExecutionResult{}
	cancelled := false

	for i, objData := range req.ObjDatas {
		if shouldCheckExecutionCancellation(i, len(req.ObjDatas)) {
			if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
				logger.Infof("Execution %s cancelled, stopping at object %d/%d", execution.ID, i, len(req.ObjDatas))
				cancelled = true
				// Mark remaining objects as cancelled
				for j := i; j < len(req.Instances); j++ {
					allResults = append(allResults, interfaces.ObjectExecutionResult{
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
		params, err := s.buildExecutionParams(actionType, objData, req.DynamicParams)
		if err != nil {
			endTime := time.Now().UnixMilli()
			allResults = append(allResults, interfaces.ObjectExecutionResult{
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
		params, result, execErr := s.invokeActionSource(ctx, execution.PermissionSnapshot, actionType, params, req.DynamicParams)

		endTime := time.Now().UnixMilli()
		if execErr != nil {
			allResults = append(allResults, interfaces.ObjectExecutionResult{
				ObjectSystemInfo: req.Instances[i],
				Status:           interfaces.ObjectStatusFailed,
				Parameters:       params,
				ErrorMessage:     execErr.Error(),
				StartTime:        startTime,
				EndTime:          endTime,
				DurationMs:       endTime - startTime,
			})
			failedCount++
		} else {
			allResults = append(allResults, interfaces.ObjectExecutionResult{
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
			s.updateExecutionProgress(ctx, execution, successCount, failedCount, allResults)
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

	endTime := time.Now().UnixMilli()

	// Update final execution record
	updates := map[string]any{
		"status":        finalStatus,
		"success_count": successCount,
		"failed_count":  failedCount,
		"results":       allResults,
		"end_time":      endTime,
		"duration_ms":   endTime - execution.StartTime,
	}

	if err := s.logsService.UpdateExecution(ctx, execution.KNID, execution.ID, updates); err != nil {
		logger.Errorf("Failed to update execution record: %v", err)
	}

	logger.Infof("Completed async execution: %s, success: %d, failed: %d, cancelled: %d",
		execution.ID, successCount, failedCount, cancelledCount)
}

// resolveExecutionMode decides whether the tool has to be invoked once per matched instance.
//
// A parameter sourced from an instance property makes every instance resolve a different
// parameter set, so each instance needs its own invocation. Without any such parameter every
// matched instance resolves a byte-identical parameter set — const values and the
// execution-level dynamic_params are the only inputs — and invoking once per instance would
// submit the very same request N times, i.e. repeat the same side effect N times.
func resolveExecutionMode(actionType *interfaces.ActionType) string {
	for _, param := range actionType.Parameters {
		if param.ValueFrom == interfaces.LOGIC_PARAMS_VALUE_FROM_PROP {
			return interfaces.ExecutionModePerInstance
		}
	}
	return interfaces.ExecutionModeOnce
}

// invokeActionSource runs the configured action source once and returns the parameters that
// were actually submitted, so the recorded result reflects the real request payload.
func (s *actionSchedulerService) invokeActionSource(ctx context.Context,
	permissionSnapshot []interfaces.PermissionRequirement, actionType *interfaces.ActionType,
	params map[string]any, dynamicParams map[string]any) (map[string]any, any, error) {
	if err := s.authorizeExecution(ctx, permissionSnapshot); err != nil {
		return params, nil, err
	}

	switch actionType.ActionSource.Type {
	case interfaces.ActionSourceTypeTool:
		result, err := ExecuteTool(ctx, s.aoAccess, actionType, params)
		return params, result, err
	case interfaces.ActionSourceTypeMCP:
		// MCP tools define their own input schema, so forward dynamic_params not declared by the action type.
		params = buildMCPParameters(params, dynamicParams)
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
	actionType *interfaces.ActionType, req *interfaces.ActionExecutionRequest) {

	startTime := time.Now().UnixMilli()
	result := interfaces.ObjectExecutionResult{
		ObjectSystemInfo: interfaces.ObjectSystemInfo{
			InstanceIdentity: map[string]any{},
			Display: locale.ValidationDetail(ctx, "ActionInstancesMerged", map[string]any{
				"count": len(req.Instances),
			}),
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
		return
	}

	// No parameter reads an instance property in this mode, so an empty object payload
	// resolves exactly the parameters any matched instance would have resolved.
	params, err := s.buildExecutionParams(actionType, map[string]any{}, req.DynamicParams)
	if err != nil {
		result.Status = interfaces.ObjectStatusFailed
		result.ErrorMessage = fmt.Sprintf("Failed to build parameters: %v", err)
		failedCount = 1
	} else {
		sentParams, invokeResult, execErr := s.invokeActionSource(ctx, execution.PermissionSnapshot, actionType, params, req.DynamicParams)
		result.Parameters = sentParams
		if execErr != nil {
			result.Status = interfaces.ObjectStatusFailed
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
}

// finishOnce writes the terminal record of an ExecutionModeOnce run.
func (s *actionSchedulerService) finishOnce(ctx context.Context, execution *interfaces.ActionExecution,
	result interfaces.ObjectExecutionResult, finalStatus string, successCount, failedCount int, endTime int64) {

	updates := map[string]any{
		"status":        finalStatus,
		"success_count": successCount,
		"failed_count":  failedCount,
		"results":       []interfaces.ObjectExecutionResult{result},
		"end_time":      endTime,
		"duration_ms":   endTime - execution.StartTime,
	}
	if err := s.logsService.UpdateExecution(ctx, execution.KNID, execution.ID, updates); err != nil {
		logger.Errorf("Failed to update execution record: %v", err)
	}
}

func shouldCheckExecutionCancellation(index, total int) bool {
	return index == 0 || total <= batchSize || index%batchSize == 0
}

func shouldUpdateExecutionProgress(completed, total int) bool {
	return total <= batchSize || completed%batchSize == 0
}

// isExecutionCancelled checks if the execution has been cancelled
func (s *actionSchedulerService) isExecutionCancelled(ctx context.Context, knID, execID string) bool {
	query := &interfaces.ActionLogDetailQuery{
		KNID:         knID,
		LogID:        execID,
		ResultsLimit: 0, // Only need metadata, not results
	}
	exec, err := s.logsService.GetExecution(ctx, query)
	if err != nil {
		logger.Warnf("Failed to check execution status: %v", err)
		return false
	}
	return exec.Status == interfaces.ExecutionStatusCancelled
}

// updateExecutionProgress updates the execution progress (batch update)
func (s *actionSchedulerService) updateExecutionProgress(ctx context.Context, execution *interfaces.ActionExecution, successCount, failedCount int, results []interfaces.ObjectExecutionResult) {
	updates := map[string]any{
		"success_count": successCount,
		"failed_count":  failedCount,
		"results":       results,
	}
	if err := s.logsService.UpdateExecution(ctx, execution.KNID, execution.ID, updates); err != nil {
		logger.Warnf("Failed to update execution progress: %v", err)
	}
}

// getInstancesForAction gets instances based on action type configuration and request parameters.
// Handles all combinations: bound/unbound object type, with/without identities, action types (add/update/delete).
func (s *actionSchedulerService) getInstancesForAction(ctx context.Context, actionType *interfaces.ActionType,
	knID, branch string, instanceIdentities []map[string]any) ([]interfaces.ObjectSystemInfo, []map[string]any, error) {

	var instances []interfaces.ObjectSystemInfo
	var objDatas []map[string]any

	// Check if object type is bound
	isObjectTypeBound := actionType.ObjectTypeID != ""

	if !isObjectTypeBound {
		// Case: unbound object type.
		if len(instanceIdentities) == 0 {
			// Case 4: unbound object type + without identities → construct a temporary virtual instance.
			logger.Infof("Action type %s has no bound object type and no identities provided, creating virtual instance", actionType.ATID)
			virtualInstance := interfaces.ObjectSystemInfo{
				InstanceIdentity: map[string]any{},
			}
			virtualObjData := map[string]any{}
			instances = append(instances, virtualInstance)
			objDatas = append(objDatas, virtualObjData)
		} else {
			// Case 5: unbound object type + with identities → construct instances by identities.
			logger.Infof("Action type %s has no bound object type, constructing instances from identities", actionType.ATID)
			for _, identity := range instanceIdentities {
				instanceInfo := interfaces.ObjectSystemInfo{
					InstanceIdentity: identity,
				}
				objData := make(map[string]any)
				for k, v := range identity {
					objData[k] = v
				}
				instances = append(instances, instanceInfo)
				objDatas = append(objDatas, objData)
			}
		}
		return instances, objDatas, nil
	}

	// Case: bound object type.
	hasIdentities := len(instanceIdentities) > 0
	isAddAction := actionType.ActionType == "add"

	if !hasIdentities {
		// Case 1: bound object type + no identities -> scan instances that satisfy the action condition.
		logger.Infof("No _instance_identities provided, scanning all matching instances for action type %s", actionType.ATID)
		condition := actionType.Condition

		objectQuery := &interfaces.ObjectQueryBaseOnObjectType{
			ActualCondition: condition,
			PageQuery: interfaces.PageQuery{
				Limit:     interfaces.MAX_LIMIT,
				NeedTotal: true,
			},
			KNID:         knID,
			Branch:       branch,
			ObjectTypeID: actionType.ObjectTypeID,
			CommonQueryParameters: interfaces.CommonQueryParameters{
				IncludeTypeInfo: false,
			},
		}

		objects, err := s.ots.GetObjectsByObjectTypeID(ctx, objectQuery)
		if err != nil {
			logger.Errorf("Failed to scan matching instances: %v", err)
			return nil, nil, err
		}

		for _, objData := range objects.Datas {
			instanceInfo := interfaces.ObjectSystemInfo{
				InstanceIdentity: map[string]any{},
			}
			if instance_id, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]; ok {
				instanceInfo.InstanceID = instance_id
			}
			if identity, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; ok {
				if identityMap, ok := identity.(map[string]any); ok {
					instanceInfo.InstanceIdentity = identityMap
				}
			}
			if display, ok := objData[interfaces.SYSTEM_PROPERTY_DISPLAY]; ok {
				instanceInfo.Display = display
			}
			instances = append(instances, instanceInfo)
			objDatas = append(objDatas, objData)
		}
	} else {
		// Case: bound object type + with identities.
		if isAddAction {
			// Case 2: bound object type + identities + add -> query first; if not found, construct an instance and evaluate conditions; if found, filter by identities and action conditions.
			logger.Infof("Add action type with identities provided, checking instances first for action type %s", actionType.ATID)

			// First, query instances only by identities (without action condition)
			instanceCondition := logics.BuildInstanceIdentitiesCondition(instanceIdentities)
			instanceQuery := &interfaces.ObjectQueryBaseOnObjectType{
				ActualCondition: instanceCondition,
				PageQuery: interfaces.PageQuery{
					Limit:     interfaces.MAX_LIMIT,
					NeedTotal: true,
				},
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: actionType.ObjectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeTypeInfo: false,
				},
			}

			instanceObjects, err := s.ots.GetObjectsByObjectTypeID(ctx, instanceQuery)
			if err != nil {
				logger.Errorf("Failed to query instances by identities: %v", err)
				return nil, nil, err
			}

			if len(instanceObjects.Datas) == 0 {
				// All instances not found: construct instances from identities and evaluate condition
				logger.Infof("No instances found by identities, constructing instances and evaluating condition")
				objectType, exists, err := s.omAccess.GetObjectType(ctx, knID, branch, actionType.ObjectTypeID)
				if err != nil {
					logger.Errorf("Failed to get object type: %v", err)
					return nil, nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_GetActionTypeFailed).
						WithErrorDetails(fmt.Sprintf("Failed to get object type: %v", err))
				}
				if !exists {
					return nil, nil, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound).
						WithErrorDetails(fmt.Sprintf("Object type not found: %s", actionType.ObjectTypeID))
				}

				for _, identity := range instanceIdentities {
					// Evaluate condition if exists
					if actionType.Condition != nil {
						satisfies, err := logics.EvaluateInstanceAgainstCondition(ctx, identity, actionType.Condition, &objectType)
						if err != nil {
							logger.Errorf("Error evaluating condition for instance[%v], error: %v", identity, err)
							continue
						}
						if !satisfies {
							// Condition not satisfied, skip
							continue
						}
					}

					// Condition satisfied (or no condition), construct instance
					instanceInfo := interfaces.ObjectSystemInfo{
						InstanceIdentity: identity,
					}
					objData := make(map[string]any)
					for k, v := range identity {
						objData[k] = v
					}
					instances = append(instances, instanceInfo)
					objDatas = append(objDatas, objData)
				}
			} else {
				// Instances found: filter by identities and action condition
				logger.Infof("Instances found by identities, filtering by action condition")
				var condition *cond.CondCfg
				if actionType.Condition != nil {
					condition = &cond.CondCfg{
						Operation: "and",
						SubConds:  []*cond.CondCfg{instanceCondition, actionType.Condition},
					}
				} else {
					condition = instanceCondition
				}

				filteredQuery := &interfaces.ObjectQueryBaseOnObjectType{
					ActualCondition: condition,
					PageQuery: interfaces.PageQuery{
						Limit:     interfaces.MAX_LIMIT,
						NeedTotal: true,
					},
					KNID:         knID,
					Branch:       branch,
					ObjectTypeID: actionType.ObjectTypeID,
					CommonQueryParameters: interfaces.CommonQueryParameters{
						IncludeTypeInfo: false,
					},
				}

				filteredObjects, err := s.ots.GetObjectsByObjectTypeID(ctx, filteredQuery)
				if err != nil {
					logger.Errorf("Failed to filter instances: %v", err)
					return nil, nil, err
				}

				for _, objData := range filteredObjects.Datas {
					instanceInfo := interfaces.ObjectSystemInfo{
						InstanceIdentity: map[string]any{},
					}
					if instance_id, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]; ok {
						instanceInfo.InstanceID = instance_id
					}
					if identity, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; ok {
						if identityMap, ok := identity.(map[string]any); ok {
							instanceInfo.InstanceIdentity = identityMap
						}
					}
					if display, ok := objData[interfaces.SYSTEM_PROPERTY_DISPLAY]; ok {
						instanceInfo.Display = display
					}
					instances = append(instances, instanceInfo)
					objDatas = append(objDatas, objData)
				}
			}
		} else {
			// Case 3: bound object type + identities + update/delete -> filter instances by identities and action conditions.
			logger.Infof("_instance_identities provided, filtering instances by identities and action condition for action type %s", actionType.ATID)
			var condition *cond.CondCfg
			instanceCondition := logics.BuildInstanceIdentitiesCondition(instanceIdentities)
			if actionType.Condition != nil {
				condition = &cond.CondCfg{
					Operation: "and",
					SubConds:  []*cond.CondCfg{instanceCondition, actionType.Condition},
				}
			} else {
				condition = instanceCondition
			}

			objectQuery := &interfaces.ObjectQueryBaseOnObjectType{
				ActualCondition: condition,
				PageQuery: interfaces.PageQuery{
					Limit:     interfaces.MAX_LIMIT,
					NeedTotal: true,
				},
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: actionType.ObjectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeTypeInfo: false,
				},
			}

			objects, err := s.ots.GetObjectsByObjectTypeID(ctx, objectQuery)
			if err != nil {
				logger.Errorf("Failed to filter matching instances: %v", err)
				return nil, nil, err
			}

			for _, objData := range objects.Datas {
				instanceInfo := interfaces.ObjectSystemInfo{
					InstanceIdentity: map[string]any{},
				}
				if instance_id, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]; ok {
					instanceInfo.InstanceID = instance_id
				}
				if identity, ok := objData[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; ok {
					if identityMap, ok := identity.(map[string]any); ok {
						instanceInfo.InstanceIdentity = identityMap
					}
				}
				if display, ok := objData[interfaces.SYSTEM_PROPERTY_DISPLAY]; ok {
					instanceInfo.Display = display
				}
				instances = append(instances, instanceInfo)
				objDatas = append(objDatas, objData)
			}
		}
	}

	return instances, objDatas, nil
}

// buildExecutionParams builds the execution parameters from action type parameters and object data.
// Uses getNestedValue to support dot-separated nested parameter names (e.g. "props.headers").
func (s *actionSchedulerService) buildExecutionParams(actionType *interfaces.ActionType,
	instance map[string]any, dynamicParams map[string]any) (map[string]any, error) {

	params := make(map[string]any)

	for _, param := range actionType.Parameters {
		switch param.ValueFrom {
		case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
			if propName, ok := param.Value.(string); ok {
				if val := getNestedValue(instance, propName); val != nil {
					setNestedValue(params, param.Name, val)
				}
			}
		case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
			setNestedValue(params, param.Name, param.Value)
		case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
			if val := logics.ActionDynamicParamGetValue(dynamicParams, param.Name); val != nil {
				setNestedValue(params, param.Name, val)
			}
		}
	}

	return params, nil
}
