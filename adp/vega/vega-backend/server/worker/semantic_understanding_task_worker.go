// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/bkn_agent"
	"vega-backend/logics/resource"
	"vega-backend/logics/semantic_understanding_task"
)

var semanticUnderstandingSourceIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// SemanticUnderstandingTaskWorker handles semantic-understanding execution tasks.
type SemanticUnderstandingTaskWorker struct {
	appSetting *common.AppSetting
	suts       interfaces.SemanticUnderstandingTaskService
	bas        interfaces.BknAgentService
	rs         interfaces.ResourceService
}

// NewSemanticUnderstandingTaskWorker creates a semantic-understanding task worker.
func NewSemanticUnderstandingTaskWorker(appSetting *common.AppSetting) *SemanticUnderstandingTaskWorker {
	return &SemanticUnderstandingTaskWorker{
		appSetting: appSetting,
		suts:       semantic_understanding_task.NewSemanticUnderstandingTaskService(appSetting),
		bas:        bkn_agent.NewBknAgentService(appSetting),
		rs:         resource.NewResourceService(appSetting),
	}
}

// HandleTask runs a semantic-understanding task through bkn-agent and persists the result.
func (sutw *SemanticUnderstandingTaskWorker) HandleTask(ctx context.Context, task *asynq.Task) error {
	var msg interfaces.SemanticUnderstandingTaskMessage
	if err := sonic.Unmarshal(task.Payload(), &msg); err != nil {
		logger.Errorf("Failed to unmarshal semantic understanding task message: %v", err)
		return err
	}

	taskID := msg.TaskID
	logger.Infof("Starting semantic understanding task: %s", taskID)

	taskInfo, err := sutw.suts.InternalGetByID(ctx, taskID)
	if err != nil {
		logger.Errorf("Failed to get semantic understanding task %s: %v", taskID, err)
		return err
	}
	if taskInfo == nil {
		return fmt.Errorf("semantic understanding task %s not found", taskID)
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, taskInfo.Creator)

	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusFailed {
		logger.Infof("Semantic understanding task already finished: id=%s, status=%s", taskInfo.ID, taskInfo.Status)
		return nil
	}
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusSucceeded {
		if taskInfo.AppliedTime != 0 {
			logger.Infof("Semantic understanding task already applied: id=%s", taskInfo.ID)
			return nil
		}
		return sutw.applyAndMark(ctx, taskInfo)
	}

	agentTaskID := taskInfo.AgentTaskID
	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusPending {
		claimed, err := sutw.suts.ClaimRunning(ctx, taskInfo.ID)
		if err != nil {
			return err
		}
		if !claimed {
			logger.Infof("Semantic understanding task was not claimed for running: id=%s", taskInfo.ID)
			return nil
		}
	}
	if agentTaskID == "" {
		agentTaskID, err = sutw.bas.Run(ctx, taskInfo)
		if err != nil {
			return err
		}

		running, err := sutw.suts.SetAgentTaskID(ctx, taskInfo.ID, agentTaskID)
		if err != nil {
			return err
		}
		if !running {
			logger.Infof("Semantic understanding task was not claimed for running: id=%s", taskInfo.ID)
			return nil
		}
	}

	agentTask, err := sutw.bas.WaitResult(ctx, agentTaskID)
	if err != nil {
		return err
	}
	if agentTask.Status == interfaces.BknAgentTaskStatusFailed {
		_, _ = sutw.suts.MarkFailed(ctx, taskInfo.ID, bknAgentFailureDetail(agentTask))
		return nil
	}

	resultJSON, confidence, confidenceDetailJSON, err := parseBknAgentResult(agentTask)
	if err != nil {
		_, _ = sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
		return nil
	}

	succeeded, err := sutw.suts.MarkSucceeded(ctx, taskInfo.ID, resultJSON, confidence, confidenceDetailJSON)
	if err != nil {
		return err
	}
	if !succeeded {
		return nil
	}
	taskInfo.ResultJSON = resultJSON
	taskInfo.Confidence = confidence
	return sutw.applyAndMark(ctx, taskInfo)
}

func (sutw *SemanticUnderstandingTaskWorker) applyAndMark(ctx context.Context, task *interfaces.SemanticUnderstandingTask) error {
	if logics.DB == nil {
		applyResult, err := sutw.applyResult(ctx, task, task.ResultJSON, task.Confidence, nil)
		if err != nil {
			return err
		}
		_, err = sutw.suts.MarkApplied(ctx, task.ID, applyResult.Applied, applyResult.DetailJSON)
		return err
	}

	tx, err := logics.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin semantic understanding apply transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	applyResult, err := sutw.applyResult(ctx, task, task.ResultJSON, task.Confidence, tx)
	if err != nil {
		return err
	}
	if _, err := sutw.suts.InternalMarkApplied(ctx, tx, task.ID, applyResult.Applied, applyResult.DetailJSON); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func bknAgentFailureDetail(agentTask *interfaces.BknAgentTask) string {
	if agentTask == nil {
		return "agent task failed"
	}
	if agentTask.FailureDetail != "" {
		return agentTask.FailureDetail
	}
	if agentTask.Error != "" {
		return agentTask.Error
	}
	return fmt.Sprintf("agent task %s failed", agentTask.TaskID)
}

func parseBknAgentResult(agentTask *interfaces.BknAgentTask) (string, float64, string, error) {
	if agentTask == nil {
		return "", 0, "", fmt.Errorf("agent task result is required")
	}
	result := agentTask.Result
	if len(result) == 0 {
		result = agentTask.ResultJSON
	}
	if len(result) == 0 {
		return "", 0, "", fmt.Errorf("agent task result is empty")
	}
	result, err := extractBknAgentResultJSON(result)
	if err != nil {
		return "", 0, "", err
	}

	resultObject := map[string]sonic.NoCopyRawMessage{}
	if err := sonic.Unmarshal(result, &resultObject); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task result failed: %w", err)
	}

	var confidence float64
	confidenceRaw, ok := resultObject["confidence"]
	if !ok {
		return "", 0, "", fmt.Errorf("agent task result missing confidence")
	}
	if err := sonic.Unmarshal(confidenceRaw, &confidence); err != nil {
		return "", 0, "", fmt.Errorf("unmarshal agent task confidence failed: %w", err)
	}
	if confidence < 0 || confidence > 1 {
		return "", 0, "", fmt.Errorf("agent task confidence must be between 0 and 1")
	}

	detail := make(map[string]sonic.NoCopyRawMessage)
	for _, key := range []string{
		"resource",
		"fields",
		"logic_views",
		"obsolete_logic_views",
		"warnings",
		"confidence_detail",
		"confidence_details",
	} {
		if value, ok := resultObject[key]; ok {
			detail[key] = value
		}
	}
	detailJSON, err := sonic.Marshal(detail)
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal confidence detail failed: %w", err)
	}

	return string(result), confidence, string(detailJSON), nil
}

func extractBknAgentResultJSON(result []byte) ([]byte, error) {
	start := -1
	for i, b := range result {
		if b == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("agent task result missing json object")
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(result); i++ {
		b := result[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return result[start : i+1], nil
			}
		}
	}

	return nil, fmt.Errorf("agent task result json object is incomplete")
}

func (sutw *SemanticUnderstandingTaskWorker) applyResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, confidence float64, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if confidence < task.ConfidenceThreshold {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:              "confidence_below_threshold",
			Confidence:          confidence,
			ConfidenceThreshold: task.ConfidenceThreshold,
			Scope:               task.Scope,
		})
	}
	if task.ApplyMode == interfaces.SemanticUnderstandingApplyModeDryRun {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "dry_run",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	switch task.Scope {
	case interfaces.SemanticUnderstandingTaskScopeResource:
		return sutw.applyResourceResult(ctx, task, resultJSON, tx)
	case interfaces.SemanticUnderstandingTaskScopeCatalog:
		return sutw.applyCatalogResult(ctx, task, resultJSON, tx)
	default:
		return nil, fmt.Errorf("unsupported semantic understanding task scope: %s", task.Scope)
	}
}

func skippedApplyResult(detail interfaces.SemanticUnderstandingSkippedApplyDetail) (*interfaces.SemanticUnderstandingApplyResult, error) {
	detailBytes, err := sonic.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal semantic understanding skipped apply detail failed: %w", err)
	}
	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    false,
		DetailJSON: string(detailBytes),
	}, nil
}

type resourceSemanticUnderstandingResult struct {
	Resource resourceSemanticUnderstandingResourceResult `json:"resource"`
	Fields   []resourceSemanticUnderstandingFieldResult  `json:"fields"`
}

type resourceSemanticUnderstandingResourceResult struct {
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

type resourceSemanticUnderstandingFieldResult struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

type resourceSemanticUnderstandingApplyDetail struct {
	ResourceUpdated bool     `json:"resource_updated"`
	UpdatedResource []string `json:"updated_resource,omitempty"`
	UpdatedFields   []string `json:"updated_fields,omitempty"`
	SkippedFields   []string `json:"skipped_fields,omitempty"`
}

func (sutw *SemanticUnderstandingTaskWorker) applyResourceResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.ResourceID == "" {
		return nil, fmt.Errorf("resource_id is required for resource semantic understanding task")
	}

	var result resourceSemanticUnderstandingResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal resource semantic understanding result failed: %w", err)
	}
	if err := validateConfidence(result.Resource.Confidence, "resource.confidence"); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(result.Resource.DisplayName) > interfaces.NAME_MAX_LENGTH {
		return nil, fmt.Errorf("resource display_name exceeds max length")
	}

	resourceInfo, err := sutw.rs.GetByID(ctx, task.ResourceID)
	if err != nil {
		return nil, err
	}
	if resourceInfo == nil {
		return nil, fmt.Errorf("resource %s not found", task.ResourceID)
	}

	fieldByName := make(map[string]*interfaces.Property, len(resourceInfo.SchemaDefinition))
	for _, property := range resourceInfo.SchemaDefinition {
		if property != nil {
			fieldByName[property.Name] = property
		}
	}

	seenFields := make(map[string]struct{}, len(result.Fields))
	updatedFields := make([]string, 0)
	skippedFields := make([]string, 0)
	for _, field := range result.Fields {
		if field.Name == "" {
			skippedFields = append(skippedFields, "<empty>: missing name")
			continue
		}
		if _, ok := seenFields[field.Name]; ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: duplicate", field.Name))
			continue
		}
		seenFields[field.Name] = struct{}{}

		property, ok := fieldByName[field.Name]
		if !ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: not found", field.Name))
			continue
		}
		if utf8.RuneCountInString(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name exceeds max length", field.Name))
			continue
		}
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: description exceeds max length", field.Name))
			continue
		}
		if err := validateConfidence(field.Confidence, fmt.Sprintf("fields[%s].confidence", field.Name)); err != nil {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: invalid confidence", field.Name))
			continue
		}
		if field.Confidence != nil && *field.Confidence < task.ConfidenceThreshold {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: confidence below threshold", field.Name))
			continue
		}

		fieldUpdated := false
		if applyStringByMode(task.ApplyMode, &property.DisplayName, field.DisplayName) {
			fieldUpdated = true
		}
		if applyStringByMode(task.ApplyMode, &property.Description, field.Description) {
			fieldUpdated = true
		}
		if fieldUpdated {
			updatedFields = append(updatedFields, field.Name)
		}
	}

	updatedResource := make([]string, 0, 2)
	if applyStringByMode(task.ApplyMode, &resourceInfo.Name, result.Resource.DisplayName) {
		updatedResource = append(updatedResource, "name")
	}
	if applyStringByMode(task.ApplyMode, &resourceInfo.Description, result.Resource.Description) {
		updatedResource = append(updatedResource, "description")
	}
	resourceUpdated := len(updatedResource) > 0
	if !resourceUpdated && len(updatedFields) == 0 {
		if len(skippedFields) > 0 {
			detailBytes, err := sonic.Marshal(resourceSemanticUnderstandingApplyDetail{SkippedFields: skippedFields})
			if err != nil {
				return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
			}
			return &interfaces.SemanticUnderstandingApplyResult{Applied: false, DetailJSON: string(detailBytes)}, nil
		}
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_resource_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	resourceInfo.Updater = task.Creator
	resourceInfo.UpdateTime = time.Now().UnixMilli()
	if tx != nil {
		err = sutw.rs.InternalUpdate(ctx, tx, resourceInfo)
	} else {
		err = sutw.rs.UpdateResource(ctx, resourceInfo)
	}
	if err != nil {
		return nil, err
	}

	detailBytes, err := sonic.Marshal(resourceSemanticUnderstandingApplyDetail{
		ResourceUpdated: resourceUpdated,
		UpdatedResource: updatedResource,
		UpdatedFields:   updatedFields,
		SkippedFields:   skippedFields,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
	}

	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    true,
		DetailJSON: string(detailBytes),
	}, nil
}

func applyStringByMode(mode string, current *string, next string) bool {
	if next == "" {
		return false
	}
	switch mode {
	case interfaces.SemanticUnderstandingApplyModeFillEmpty:
		if *current != "" {
			return false
		}
	case interfaces.SemanticUnderstandingApplyModeForce:
	default:
		return false
	}
	if *current == next {
		return false
	}
	*current = next
	return true
}

func validateConfidence(confidence *float64, path string) error {
	if confidence == nil {
		return nil
	}
	if *confidence < 0 || *confidence > 1 {
		return fmt.Errorf("%s must be between 0 and 1", path)
	}
	return nil
}

type catalogSemanticUnderstandingResult struct {
	LogicViews         []catalogSemanticUnderstandingLogicView    `json:"logic_views"`
	ObsoleteLogicViews []catalogSemanticUnderstandingObsoleteView `json:"obsolete_logic_views"`
}

type catalogSemanticUnderstandingLogicView struct {
	Action           string                            `json:"action"`
	TargetResourceID string                            `json:"target_resource_id"`
	Name             string                            `json:"name"`
	SourceIdentifier string                            `json:"source_identifier"`
	Description      string                            `json:"description"`
	SourceResources  []string                          `json:"source_resources"`
	LogicDefinition  []*interfaces.LogicDefinitionNode `json:"logic_definition"`
	Confidence       *float64                          `json:"confidence,omitempty"`
}

type catalogSemanticUnderstandingObsoleteView struct {
	TargetResourceID string   `json:"target_resource_id"`
	Reason           string   `json:"reason"`
	Confidence       *float64 `json:"confidence,omitempty"`
}

type catalogSemanticUnderstandingApplyDetail struct {
	CreatedResourceIDs []string `json:"created_resource_ids,omitempty"`
	UpdatedResourceIDs []string `json:"updated_resource_ids,omitempty"`
	StaledResourceIDs  []string `json:"staled_resource_ids,omitempty"`
}

func (sutw *SemanticUnderstandingTaskWorker) applyCatalogResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, tx *sql.Tx) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.CatalogID == "" {
		return nil, fmt.Errorf("catalog_id is required for catalog semantic understanding task")
	}

	var result catalogSemanticUnderstandingResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal catalog semantic understanding result failed: %w", err)
	}

	resources, err := sutw.rs.GetByCatalogID(ctx, task.CatalogID)
	if err != nil {
		return nil, err
	}
	resourceByID := make(map[string]*interfaces.Resource, len(resources))
	logicViewByID := make(map[string]*interfaces.Resource)
	sourceIdentifiers := make(map[string]struct{}, len(resources))
	for _, res := range resources {
		if res == nil {
			continue
		}
		resourceByID[res.ID] = res
		if res.SourceIdentifier != "" {
			sourceIdentifiers[res.SourceIdentifier] = struct{}{}
		}
		if res.Category == interfaces.ResourceCategoryLogicView {
			logicViewByID[res.ID] = res
		}
	}

	detail := catalogSemanticUnderstandingApplyDetail{}
	for i, view := range result.LogicViews {
		if err := validateConfidence(view.Confidence, fmt.Sprintf("logic_views[%d].confidence", i)); err != nil {
			return nil, err
		}
		if err := validateCatalogLogicViewOutput(view, resourceByID, logicViewByID, sourceIdentifiers); err != nil {
			return nil, err
		}

		switch view.Action {
		case "create":
			sourceIdentifiers[view.SourceIdentifier] = struct{}{}
			req := &interfaces.ResourceRequest{
				CatalogID:        task.CatalogID,
				Name:             view.Name,
				SourceIdentifier: view.SourceIdentifier,
				Description:      view.Description,
				Category:         interfaces.ResourceCategoryLogicView,
				Status:           interfaces.ResourceStatusActive,
				LogicDefinition:  view.LogicDefinition,
			}
			var created *interfaces.Resource
			if tx != nil {
				created, err = sutw.rs.InternalCreate(ctx, tx, req)
			} else {
				created, err = sutw.rs.Create(ctx, req)
			}
			if err != nil {
				return nil, err
			}
			if created != nil {
				detail.CreatedResourceIDs = append(detail.CreatedResourceIDs, created.ID)
			}
		case "update":
			current := logicViewByID[view.TargetResourceID]
			nextDescription := current.Description
			applyStringByMode(task.ApplyMode, &nextDescription, view.Description)
			nextLogicDefinition := current.LogicDefinition
			if task.ApplyMode == interfaces.SemanticUnderstandingApplyModeForce {
				nextLogicDefinition = view.LogicDefinition
			}
			next := &interfaces.ResourceRequest{
				ID:              current.ID,
				CatalogID:       current.CatalogID,
				Name:            current.Name,
				Tags:            current.Tags,
				Description:     nextDescription,
				Category:        current.Category,
				Status:          current.Status,
				Database:        current.Database,
				SourceMetadata:  current.SourceMetadata,
				IndexConfig:     current.IndexConfig,
				LogicDefinition: nextLogicDefinition,
			}
			if tx != nil {
				current.Description = nextDescription
				current.LogicDefinition = nextLogicDefinition
				current.Updater = task.Creator
				current.UpdateTime = time.Now().UnixMilli()
				err = sutw.rs.InternalUpdate(ctx, tx, current)
			} else {
				err = sutw.rs.Update(ctx, current, next)
			}
			if err != nil {
				return nil, err
			}
			detail.UpdatedResourceIDs = append(detail.UpdatedResourceIDs, current.ID)
		}
	}

	for i, obsolete := range result.ObsoleteLogicViews {
		if err := validateConfidence(obsolete.Confidence, fmt.Sprintf("obsolete_logic_views[%d].confidence", i)); err != nil {
			return nil, err
		}
		if obsolete.TargetResourceID == "" {
			return nil, fmt.Errorf("obsolete_logic_views[%d].target_resource_id is required", i)
		}
		if _, ok := logicViewByID[obsolete.TargetResourceID]; !ok {
			return nil, fmt.Errorf("obsolete logic view %s does not exist in catalog input", obsolete.TargetResourceID)
		}
		if tx != nil {
			err = sutw.rs.InternalUpdateStatus(ctx, tx, obsolete.TargetResourceID, interfaces.ResourceStatusStale, obsolete.Reason)
		} else {
			err = sutw.rs.UpdateStatus(ctx, obsolete.TargetResourceID, interfaces.ResourceStatusStale, obsolete.Reason)
		}
		if err != nil {
			return nil, err
		}
		detail.StaledResourceIDs = append(detail.StaledResourceIDs, obsolete.TargetResourceID)
	}

	if len(detail.CreatedResourceIDs) == 0 && len(detail.UpdatedResourceIDs) == 0 && len(detail.StaledResourceIDs) == 0 {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_catalog_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	detailBytes, err := sonic.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal catalog semantic understanding apply detail failed: %w", err)
	}
	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    true,
		DetailJSON: string(detailBytes),
	}, nil
}

func validateCatalogLogicViewOutput(view catalogSemanticUnderstandingLogicView, resourceByID map[string]*interfaces.Resource, logicViewByID map[string]*interfaces.Resource, sourceIdentifiers map[string]struct{}) error {
	switch view.Action {
	case "create":
		if view.TargetResourceID != "" {
			return fmt.Errorf("target_resource_id must be empty when creating logic view")
		}
		if view.Name == "" {
			return fmt.Errorf("logic view name is required when creating logic view")
		}
		if !semanticUnderstandingSourceIdentifierPattern.MatchString(view.SourceIdentifier) {
			return fmt.Errorf("source_identifier must be lower snake_case when creating logic view")
		}
		if _, exists := sourceIdentifiers[view.SourceIdentifier]; exists {
			return fmt.Errorf("source_identifier %s already exists in catalog input", view.SourceIdentifier)
		}
	case "update":
		if view.TargetResourceID == "" {
			return fmt.Errorf("target_resource_id is required when updating logic view")
		}
		if _, ok := logicViewByID[view.TargetResourceID]; !ok {
			return fmt.Errorf("logic view %s does not exist in catalog input", view.TargetResourceID)
		}
	default:
		return fmt.Errorf("unsupported logic view action: %s", view.Action)
	}
	if len(view.LogicDefinition) == 0 {
		return fmt.Errorf("logic_definition is required for logic view action %s", view.Action)
	}
	for _, sourceResourceID := range view.SourceResources {
		if _, ok := resourceByID[sourceResourceID]; !ok {
			return fmt.Errorf("source resource %s does not exist in catalog input", sourceResourceID)
		}
	}
	return nil
}
