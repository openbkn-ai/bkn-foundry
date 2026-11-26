// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"uuid"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_target"
)

// A record-event action runs only on its schedule: bkn-backend's schedule worker calls the
// internal execute route, as the schedule's execution subject, with trigger_type scheduled and no
// targets. The execution record is written first; the targets are selected afterwards, as the
// executor, and handed to the event ledger in one rule check. A selection that fails fails the
// execution, and the ledger is not called, so the events stay as they are.

// eventCheckMessageMax is the longest message the ledger keeps with a check.
const eventCheckMessageMax = 1024

func eventActionRefused(ctx context.Context, status int, detail string) error {
	code := oerrors.OntologyQuery_ActionExecution_InvalidParameter
	if status == http.StatusForbidden {
		code = httperrors.PublicError_Forbidden
	}
	return httperrors.NewHTTPError(ctx, status, code).WithErrorDetails(detail)
}

// eventPermissionRequirements are what a check needs before it reads anything: reading the
// objects of the bound object type. The Cypher query, the function and the metric are each
// authorized again by the service that runs them.
func eventPermissionRequirements(ctx context.Context, knID string, actionType *interfaces.ActionType) ([]interfaces.PermissionRequirement, error) {
	if err := validateActionResourceID(knID, actionType.ObjectTypeID); err != nil {
		return nil, actionPermissionInvalid(ctx, err.Error())
	}
	return []interfaces.PermissionRequirement{{
		ResourceType: interfaces.PermissionResourceTypeObjectType,
		ResourceID:   knID + "/" + actionType.ObjectTypeID,
		Operation:    interfaces.PermissionOperationQueryData,
	}}, nil
}

// checkEventExecution verifies the current subject could run the record-event action's check.
func (s *actionSchedulerService) checkEventExecution(ctx context.Context, req *interfaces.ActionExecutionRequest,
	actionType *interfaces.ActionType) error {
	requirements, err := eventPermissionRequirements(ctx, req.KNID, actionType)
	if err != nil {
		return err
	}
	if s.permissions == nil {
		return actionPermissionUnavailable(ctx, fmt.Errorf("action permission service is not configured"))
	}
	return s.permissions.RequirePermissions(ctx, requirements)
}

// executeEventAction accepts one scheduled check of a record-event action and starts it.
func (s *actionSchedulerService) executeEventAction(ctx context.Context, req *interfaces.ActionExecutionRequest,
	actionType *interfaces.ActionType, actionTypeSnapshot map[string]any) (*interfaces.ActionExecutionResponse, error) {
	if !req.Internal || req.TriggerType != interfaces.TriggerTypeScheduled {
		return nil, eventActionRefused(ctx, http.StatusForbidden,
			"a record-event action runs only on its schedule; run its schedule to check now")
	}
	if len(req.InstanceIdentities) != 0 || req.RelationTarget != nil || len(req.ObjectInput) != 0 || len(req.DynamicParams) != 0 {
		return nil, eventActionRefused(ctx, http.StatusBadRequest,
			"a record-event action selects its targets at each check: _instance_identities must be empty, and it takes no input")
	}
	if interfaces.KNVersionPinFromContext(ctx).UsesCallerIdentity() {
		return nil, eventActionRefused(ctx, http.StatusBadRequest, "a record-event action checks the running version only")
	}
	executor, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if executor.ID == "" || executor.Type == "" {
		return nil, actionPermissionInvalid(ctx, "a record-event action checks as its schedule's execution subject")
	}
	if err := s.checkEventExecution(ctx, req, actionType); err != nil {
		return nil, err
	}
	required, err := s.actionGovernanceRequired(ctx, req.KNID, req.ActionTypeID)
	if err != nil {
		return nil, err
	}
	if required {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_GovernanceRequired).
			WithErrorDetails("record-event actions do not support governance permits")
	}
	requirements, err := eventPermissionRequirements(ctx, req.KNID, actionType)
	if err != nil {
		return nil, err
	}

	fingerprints, err := computeDuplicateFingerprints(nil, nil, actionType.OperationConfig, actionType.RuleVersion)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(fmt.Sprintf("failed to compute duplicate fingerprint: %v", err))
	}
	req.InstanceIdentityHash = fingerprints.primary
	req.AlternateInstanceIdentityHash = ""
	if s.duplicateCheckHook != nil {
		proceed, dupErr := s.duplicateCheckHook(ctx, req)
		if dupErr != nil {
			var httpErr *httperrors.HTTPError
			if errors.As(dupErr, &httpErr) {
				return nil, httpErr
			}
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_QueryExecutionsFailed).
				WithErrorDetails(dupErr.Error())
		}
		if !proceed {
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_ActionExecution_DuplicateExecution).
				WithErrorDetails(fmt.Sprintf("A check of action_type_id=%s is still running", req.ActionTypeID))
		}
	}

	generatedExecutionID := uuid.NewV7()
	now := time.Now().UnixMilli()
	execution := &interfaces.ActionExecution{
		ID:                   generatedExecutionID.String(),
		KNID:                 req.KNID,
		ActionTypeID:         actionType.ATID,
		ActionTypeName:       actionType.ATName,
		ActionSourceType:     actionType.ActionSource.Type,
		ActionSource:         actionType.ActionSource,
		ObjectTypeID:         actionType.ObjectTypeID,
		TriggerType:          interfaces.TriggerTypeScheduled,
		Status:               interfaces.ExecutionStatusPending,
		ExecutionMode:        interfaces.ExecutionModeOnce,
		TotalCount:           1, // one rule check
		Results:              []interfaces.ObjectExecutionResult{},
		ExecutorID:           executor.ID,
		Executor:             executor,
		StartTime:            now,
		ActionTypeSnapshot:   actionTypeSnapshot,
		PermissionSnapshot:   requirements,
		InstanceIdentityHash: fingerprints.primary,
	}
	if err := s.logsService.CreateExecution(ctx, execution); err != nil {
		logger.Errorf("Failed to create execution record: %v", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CreateExecutionFailed).
			WithErrorDetails(err.Error())
	}
	go s.executeEventAsync(execution, actionType)
	return &interfaces.ActionExecutionResponse{
		ExecutionID: execution.ID,
		Status:      interfaces.ExecutionStatusPending,
		Message:     "Action execution started",
		CreatedAt:   now,
	}, nil
}

// eventSelector selects the targets of record-event actions with this service's dependencies.
func (s *actionSchedulerService) eventSelector() *action_target.Selector {
	return &action_target.Selector{
		Models: s.omAccess, Objects: s.ots, Queries: s.queries, Rules: s.eventRules, Tools: s.aoAccess,
		Metrics: logics.ObjectMetricQueryService(), Limit: logics.ActionExecutionMaxObjects,
	}
}

// executeEventAsync runs one check: select the targets, then hand them to the ledger. Its one
// result carries the selected targets, so the action logs can be filtered by them.
func (s *actionSchedulerService) executeEventAsync(execution *interfaces.ActionExecution, actionType *interfaces.ActionType) {
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, execution.Executor)
	if err := s.logsService.MarkExecutionRunning(ctx, execution.KNID, execution.ID); err != nil {
		logger.Warnf("Failed to update execution status to running: %v", err)
	}
	checkTime := time.UnixMilli(execution.StartTime)
	result := interfaces.ObjectExecutionResult{
		ObjectSystemInfo: interfaces.ObjectSystemInfo{InstanceIdentity: map[string]any{}, Display: "rule check"},
		StartTime:        time.Now().UnixMilli(),
		Parameters: map[string]any{
			"event_type":   actionType.OperationConfig.EventClass,
			"rule_version": actionType.RuleVersion,
			"check_time":   checkTime.UTC().Format(time.RFC3339),
		},
	}
	finish := func(status string, targets int) {
		result.EndTime = time.Now().UnixMilli()
		result.DurationMs = new(result.EndTime - result.StartTime)
		success, failed := 0, 0
		switch status {
		case interfaces.ExecutionStatusCompleted:
			result.Status, success = interfaces.ObjectStatusSuccess, 1
		case interfaces.ExecutionStatusCancelled:
			result.Status = interfaces.ObjectStatusCancelled
		default:
			result.Status, failed = interfaces.ObjectStatusFailed, 1
		}
		results := resultBuffer{pending: []interfaces.ObjectExecutionResult{result}}
		s.flushFinalResults(ctx, execution, &results)
		if err := s.logsService.FinishExecution(ctx, execution.KNID, execution.ID, &interfaces.ExecutionOutcome{
			Status: status, SuccessCount: success, FailedCount: failed, TargetCount: &targets,
			EndTime: result.EndTime, DurationMs: result.EndTime - execution.StartTime,
		}); err != nil {
			logger.Errorf("Failed to update execution record: %v", err)
		}
		logger.Infof("Completed rule check: %s, action type %s, targets: %d, status: %s",
			execution.ID, execution.ActionTypeID, targets, status)
	}
	fail := func(reason string) {
		result.ErrorMessage = reason
		finish(interfaces.ExecutionStatusFailed, len(result.Targets))
	}

	if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
		result.ErrorMessage = "execution cancelled"
		finish(interfaces.ExecutionStatusCancelled, 0)
		return
	}
	if err := s.authorizeExecution(ctx, execution.PermissionSnapshot); err != nil {
		fail(err.Error())
		return
	}
	clock, err := action_target.EventCheckClock(actionType, checkTime)
	if err != nil {
		fail(err.Error())
		return
	}
	selection, err := s.eventSelector().Select(ctx, execution.KNID, interfaces.MAIN_BRANCH, actionType, clock)
	if err != nil {
		fail("select targets: " + err.Error())
		return
	}
	result.Targets = make([]interfaces.ObjectSystemInfo, 0, len(selection.Targets))
	for _, target := range selection.Targets {
		result.Targets = append(result.Targets, interfaces.ObjectSystemInfo{InstanceIdentity: target.Identity, Display: target.Name})
	}
	result.Parameters["hit_count"] = len(selection.Targets)
	result.Parameters["limited"] = selection.Limited
	if selection.Message != "" {
		result.Parameters["message"] = selection.Message
	}
	if s.isExecutionCancelled(ctx, execution.KNID, execution.ID) {
		result.ErrorMessage = "execution cancelled"
		finish(interfaces.ExecutionStatusCancelled, len(result.Targets))
		return
	}
	if s.eventRules == nil {
		fail("the event ledger is not configured")
		return
	}
	recorded, err := s.eventRules.RecordRuleCheck(ctx, execution.KNID, execution.ID, eventRuleCheck(actionType, selection))
	if err != nil {
		fail("record the check: " + action_target.DependencyReason(err))
		return
	}
	result.Result = map[string]any{"check": recorded.Check, "event_count": len(recorded.Events)}
	finish(interfaces.ExecutionStatusCompleted, len(result.Targets))
}

// eventRuleCheck is the rule check a selection records.
func eventRuleCheck(actionType *interfaces.ActionType, selection *action_target.Selection) *interfaces.EventRuleCheckRequest {
	hits := make([]interfaces.EventHit, 0, len(selection.Targets))
	for _, target := range selection.Targets {
		hits = append(hits, target.EventHit)
	}
	message := selection.Message
	if utf8.RuneCountInString(message) > eventCheckMessageMax {
		message = string([]rune(message)[:eventCheckMessageMax])
	}
	return &interfaces.EventRuleCheckRequest{
		EventType:    actionType.OperationConfig.EventClass,
		ActionTypeID: actionType.ATID,
		RuleVersion:  actionType.RuleVersion,
		Status:       interfaces.EventRuleCheckStatusOK,
		Message:      message,
		Hits:         hits,
		Covered:      selection.Covered,
	}
}

// trialVersion reads a trial's branch as the version it names. bkn-backend names the branch it
// saves to: main, or on a versioned network its draft (or a version branch v<N>). This service
// reads only main and names other versions with kn_version, so the branch becomes the version
// pin of the trial; reading the draft then reads data as the caller.
func trialVersion(ctx context.Context, branch string) (context.Context, error) {
	if branch == "" || branch == interfaces.MAIN_BRANCH {
		return ctx, nil
	}
	if pin := interfaces.KNVersionPinFromContext(ctx); pin != nil && pin.QueryString() != "" {
		return ctx, fmt.Errorf("name the version with branch or with kn_version, not both")
	}
	version := ""
	switch {
	case branch == interfaces.DRAFT_BRANCH:
		version = interfaces.KNVersionDraft
	case strings.HasPrefix(branch, "v"):
		if n, err := strconv.Atoi(branch[1:]); err == nil && n > 0 && branch[1:] == strconv.Itoa(n) {
			version = branch[1:]
		}
	}
	if version == "" {
		return ctx, fmt.Errorf("branch %q cannot be tried: the branch is main, draft or v<N>", branch)
	}
	pin, err := interfaces.NewKNVersionPin(version, "")
	if err != nil {
		return ctx, err
	}
	return interfaces.WithKNVersionPin(ctx, pin), nil
}

// TrialAction selects what a record-event action definition would find if it checked now, as
// the caller, without recording anything. The handler has checked the caller may change the
// network.
func (s *actionSchedulerService) TrialAction(ctx context.Context, knID, branch string,
	action *interfaces.ActionType) (*interfaces.ActionTrialResponse, error) {
	if action == nil || action.OperationConfig == nil || action.OperationConfig.Type != interfaces.ActionOperationEvent {
		return nil, eventActionRefused(ctx, http.StatusBadRequest, "only a record-event action (operation_config.type event) can be tried")
	}
	if !action_target.EventActionsLicensed() {
		return nil, eventActionRefused(ctx, http.StatusBadRequest,
			fmt.Sprintf("unsupported action operation %q", action.OperationConfig.Type))
	}
	ctx, err := trialVersion(ctx, branch)
	if err != nil {
		return nil, eventActionRefused(ctx, http.StatusBadRequest, err.Error())
	}
	clock, err := action_target.EventCheckClock(action, time.Now())
	if err != nil {
		return nil, eventActionRefused(ctx, http.StatusBadRequest, err.Error())
	}
	selection, err := s.eventSelector().Select(ctx, knID, interfaces.MAIN_BRANCH, action, clock)
	if err != nil {
		var refused *action_target.SelectionError
		if errors.As(err, &refused) {
			return nil, eventActionRefused(ctx, http.StatusBadRequest, refused.Reason)
		}
		return nil, err
	}
	trial := &interfaces.EventTrialResult{
		Subject:          selection.Subject.OTID,
		Hits:             make([]interfaces.EventHit, 0, min(len(selection.Targets), interfaces.EventTrialHitsMax)),
		HitCount:         len(selection.Targets),
		Limited:          selection.Limited,
		RowFilterApplied: selection.RowFilterApplied,
	}
	for _, target := range selection.Targets[:min(len(selection.Targets), interfaces.EventTrialHitsMax)] {
		hit := target.EventHit
		hit.Detail = nil
		trial.Hits = append(trial.Hits, hit)
	}
	return &interfaces.ActionTrialResponse{Event: trial}, nil
}
