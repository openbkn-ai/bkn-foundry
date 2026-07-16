// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package worker provides background workers for VEGA Manager.
package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/logger"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics/bkn_agent"
	"vega-backend/logics/resource"
	"vega-backend/logics/semantic_understanding_task"
)

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

	if taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusSucceeded ||
		taskInfo.Status == interfaces.SemanticUnderstandingTaskStatusFailed {
		logger.Infof("Semantic understanding task already finished: id=%s, status=%s", taskInfo.ID, taskInfo.Status)
		return nil
	}

	agentTaskID := taskInfo.AgentTaskID
	if agentTaskID == "" {
		agentTaskID, err = sutw.bas.Run(ctx, taskInfo)
		if err != nil {
			_, _ = sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
			return err
		}

		running, err := sutw.suts.MarkRunning(ctx, taskInfo.ID, agentTaskID)
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
		_, _ = sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
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

	applyResult, err := sutw.applyResult(ctx, taskInfo, resultJSON, confidence)
	if err != nil {
		_, _ = sutw.suts.MarkFailed(ctx, taskInfo.ID, err.Error())
		return nil
	}

	if _, err = sutw.suts.MarkSucceeded(ctx, taskInfo.ID, resultJSON, confidence, confidenceDetailJSON); err != nil {
		return err
	}
	if applyResult.DetailJSON != "" {
		_, err = sutw.suts.MarkApplied(ctx, taskInfo.ID, applyResult.Applied, applyResult.DetailJSON)
	}
	return err
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
		"table",
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

func (sutw *SemanticUnderstandingTaskWorker) applyResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string, confidence float64) (*interfaces.SemanticUnderstandingApplyResult, error) {
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
		return sutw.applyResourceResult(ctx, task, resultJSON)
	case interfaces.SemanticUnderstandingTaskScopeCatalog:
		return sutw.applyCatalogResult(ctx, task, resultJSON)
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
	Table  resourceSemanticUnderstandingTableResult   `json:"table"`
	Fields []resourceSemanticUnderstandingFieldResult `json:"fields"`
}

type resourceSemanticUnderstandingTableResult struct {
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
	UpdatedFields   []string `json:"updated_fields,omitempty"`
}

func (sutw *SemanticUnderstandingTaskWorker) applyResourceResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.ResourceID == "" {
		return nil, fmt.Errorf("resource_id is required for resource semantic understanding task")
	}

	var result resourceSemanticUnderstandingResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal resource semantic understanding result failed: %w", err)
	}
	if err := validateConfidence(result.Table.Confidence, "table.confidence"); err != nil {
		return nil, err
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
	for _, field := range result.Fields {
		if field.Name == "" {
			return nil, fmt.Errorf("field name is required")
		}
		if _, ok := seenFields[field.Name]; ok {
			return nil, fmt.Errorf("duplicate field in semantic understanding result: %s", field.Name)
		}
		seenFields[field.Name] = struct{}{}

		property, ok := fieldByName[field.Name]
		if !ok {
			return nil, fmt.Errorf("field %s does not exist in resource schema", field.Name)
		}
		if len(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			return nil, fmt.Errorf("field %s display_name exceeds max length", field.Name)
		}
		if len(field.Description) > interfaces.MaxLength_PropertyDescription {
			return nil, fmt.Errorf("field %s description exceeds max length", field.Name)
		}
		if err := validateConfidence(field.Confidence, fmt.Sprintf("fields[%s].confidence", field.Name)); err != nil {
			return nil, err
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

	resourceUpdated := applyStringByMode(task.ApplyMode, &resourceInfo.Description, result.Table.Description)
	if !resourceUpdated && len(updatedFields) == 0 {
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_resource_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	resourceInfo.Updater = task.Creator
	resourceInfo.UpdateTime = time.Now().UnixMilli()
	if err := sutw.rs.UpdateResource(ctx, resourceInfo); err != nil {
		return nil, err
	}

	detailBytes, err := sonic.Marshal(resourceSemanticUnderstandingApplyDetail{
		ResourceUpdated: resourceUpdated,
		UpdatedFields:   updatedFields,
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

func (sutw *SemanticUnderstandingTaskWorker) applyCatalogResult(ctx context.Context, task *interfaces.SemanticUnderstandingTask, resultJSON string) (*interfaces.SemanticUnderstandingApplyResult, error) {
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
	for _, res := range resources {
		if res == nil {
			continue
		}
		resourceByID[res.ID] = res
		if res.Category == interfaces.ResourceCategoryLogicView {
			logicViewByID[res.ID] = res
		}
	}

	detail := catalogSemanticUnderstandingApplyDetail{}
	for i, view := range result.LogicViews {
		if err := validateConfidence(view.Confidence, fmt.Sprintf("logic_views[%d].confidence", i)); err != nil {
			return nil, err
		}
		if err := validateCatalogLogicViewOutput(view, resourceByID, logicViewByID); err != nil {
			return nil, err
		}

		switch view.Action {
		case "create":
			created, err := sutw.rs.Create(ctx, &interfaces.ResourceRequest{
				CatalogID:        task.CatalogID,
				Name:             view.Name,
				Description:      view.Description,
				Category:         interfaces.ResourceCategoryLogicView,
				Status:           interfaces.ResourceStatusActive,
				LogicDefinition:  view.LogicDefinition,
				SourceIdentifier: view.Name,
			})
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
			if err := sutw.rs.Update(ctx, current, &interfaces.ResourceRequest{
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
				LogicDefinition: view.LogicDefinition,
			}); err != nil {
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
		if err := sutw.rs.UpdateStatus(ctx, obsolete.TargetResourceID, interfaces.ResourceStatusStale, obsolete.Reason); err != nil {
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

func validateCatalogLogicViewOutput(view catalogSemanticUnderstandingLogicView, resourceByID map[string]*interfaces.Resource, logicViewByID map[string]*interfaces.Resource) error {
	switch view.Action {
	case "create":
		if view.TargetResourceID != "" {
			return fmt.Errorf("target_resource_id must be empty when creating logic view")
		}
		if view.Name == "" {
			return fmt.Errorf("logic view name is required when creating logic view")
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
