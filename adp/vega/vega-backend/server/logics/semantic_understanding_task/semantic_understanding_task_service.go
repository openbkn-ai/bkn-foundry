// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package semantic_understanding_task provides semantic-understanding task management.
package semantic_understanding_task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

var (
	sutServiceOnce sync.Once
	sutService     interfaces.SemanticUnderstandingTaskService
)

type semanticUnderstandingTaskService struct {
	appSetting *common.AppSetting
	suta       interfaces.SemanticUnderstandingTaskAccess
	ra         interfaces.ResourceAccess
	ca         interfaces.CatalogAccess
}

func NewSemanticUnderstandingTaskService(appSetting *common.AppSetting) interfaces.SemanticUnderstandingTaskService {
	sutServiceOnce.Do(func() {
		sutService = &semanticUnderstandingTaskService{
			appSetting: appSetting,
			suta:       logics.SUTA,
			ra:         logics.RA,
			ca:         logics.CA,
		}
	})
	return sutService
}

func (suts *semanticUnderstandingTaskService) CreateResourceTask(ctx context.Context, resourceID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.CreateResourceTask")
	defer span.End()

	if resourceID == "" {
		span.SetStatus(codes.Error, "Resource id is required")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("resource_id is required")
	}

	resource, err := suts.ra.GetByID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	task, err := normalizeResourceSemanticUnderstandingRequest(resource, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid semantic understanding task request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails(err.Error())
	}
	return suts.createTask(ctx, task)
}

func (suts *semanticUnderstandingTaskService) CreateCatalogTask(ctx context.Context, catalogID string, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.CreateCatalogTask")
	defer span.End()

	if catalogID == "" {
		span.SetStatus(codes.Error, "Catalog id is required")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("catalog_id is required")
	}

	catalog, err := suts.ca.GetByID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	resources, err := suts.ra.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog resources failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}

	task, err := normalizeCatalogSemanticUnderstandingRequest(catalog, resources, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid semantic understanding task request")
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails(err.Error())
	}
	return suts.createTask(ctx, task)
}

func (suts *semanticUnderstandingTaskService) createTask(ctx context.Context, task *interfaces.SemanticUnderstandingTask) (*interfaces.SemanticUnderstandingTask, error) {
	activeTask, err := suts.suta.FindActiveByInputHash(ctx, task.Scope, task.InputHash)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	if activeTask != nil {
		return activeTask, nil
	}

	accountInfo := accountInfoFromContext(ctx)

	now := time.Now().UnixMilli()
	task.ID = xid.New().String()
	task.Status = interfaces.SemanticUnderstandingTaskStatusPending
	task.Creator = accountInfo
	task.CreateTime = now
	task.UpdateTime = now
	if err := suts.suta.Create(ctx, task); err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_CreateResourcesFailed).
			WithErrorDetails(err.Error())
	}

	return task, nil
}

func (suts *semanticUnderstandingTaskService) GetByID(ctx context.Context, id string) (*interfaces.SemanticUnderstandingTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.GetByID")
	defer span.End()

	task, err := suts.suta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get semantic understanding task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	return task, nil
}

func (suts *semanticUnderstandingTaskService) List(ctx context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTask, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.List")
	defer span.End()

	tasks, total, err := suts.suta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List semantic understanding tasks failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}
	return tasks, total, nil
}

func (suts *semanticUnderstandingTaskService) Delete(ctx context.Context, ids []string, ignoreMissing bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.Delete")
	defer span.End()

	seen := make(map[string]struct{}, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	tasks, err := suts.suta.GetByIDs(ctx, uniqueIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Get semantic understanding tasks failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_InternalError_FilterResourcesFailed).
			WithErrorDetails(err.Error())
	}

	toDelete := make([]string, 0, len(tasks))
	runningIDs := make([]string, 0)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.Status == interfaces.SemanticUnderstandingTaskStatusPending || task.Status == interfaces.SemanticUnderstandingTaskStatusRunning {
			runningIDs = append(runningIDs, task.ID)
			continue
		}
		toDelete = append(toDelete, task.ID)
	}

	if len(runningIDs) > 0 {
		span.SetStatus(codes.Error, "Some semantic understanding tasks are pending or running")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": runningIDs})
	}
	if len(tasks) != len(uniqueIDs) && !ignoreMissing {
		taskByID := make(map[string]struct{}, len(tasks))
		for _, task := range tasks {
			if task != nil {
				taskByID[task.ID] = struct{}{}
			}
		}
		missingIDs := make([]string, 0, len(uniqueIDs)-len(tasks))
		for _, id := range uniqueIDs {
			if _, ok := taskByID[id]; !ok {
				missingIDs = append(missingIDs, id)
			}
		}
		span.SetStatus(codes.Error, "Some semantic understanding tasks not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_SemanticUnderstandingTask_NotFound).
			WithErrorDetails(map[string]any{"missing_ids": missingIDs})
	}

	if _, err := suts.suta.DeleteByIDs(ctx, toDelete); err != nil {
		span.SetStatus(codes.Error, "Delete semantic understanding tasks failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (suts *semanticUnderstandingTaskService) MarkRunning(ctx context.Context, id string, agentTaskID string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.MarkRunning")
	defer span.End()

	if agentTaskID == "" {
		return false, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("agent_task_id is required")
	}
	return suts.suta.MarkRunning(ctx, id, agentTaskID)
}

func (suts *semanticUnderstandingTaskService) MarkSucceeded(ctx context.Context, id string, resultJSON string, confidence float64, confidenceDetailJSON string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.MarkSucceeded")
	defer span.End()

	if confidence < 0 || confidence > 1 {
		return false, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("confidence must be between 0 and 1")
	}
	return suts.suta.MarkSucceeded(ctx, id, resultJSON, confidence, confidenceDetailJSON)
}

func (suts *semanticUnderstandingTaskService) MarkFailed(ctx context.Context, id string, failureDetail string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.MarkFailed")
	defer span.End()

	return suts.suta.MarkFailed(ctx, id, failureDetail)
}

func (suts *semanticUnderstandingTaskService) MarkApplied(ctx context.Context, id string, applied bool, catalogApplyDetailJSON string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.MarkApplied")
	defer span.End()

	return suts.suta.MarkApplied(ctx, id, applied, time.Now().UnixMilli(), catalogApplyDetailJSON)
}

func normalizeResourceSemanticUnderstandingRequest(resource *interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	normalized := defaultSemanticUnderstandingRequest()
	if req != nil {
		*normalized = *req
		if req.ApplyMode == "" {
			normalized.ApplyMode = interfaces.SemanticUnderstandingApplyModeFillEmpty
		}
		if req.Language == "" {
			normalized.Language = interfaces.DefaultSemanticUnderstandingLanguage
		}
		if normalized.ConfidenceThreshold == nil {
			defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
			normalized.ConfidenceThreshold = &defaultThreshold
		}
	}
	if err := validateSemanticUnderstandingRequest(normalized); err != nil {
		return nil, err
	}
	if normalized.IncludeSampleRows {
		if normalized.SamplePolicy == nil {
			return nil, fmt.Errorf("sample_policy is required when include_sample_rows is true")
		}
		if !normalized.SamplePolicy.Masked {
			return nil, fmt.Errorf("sample_policy.masked must be true")
		}
		if normalized.SamplePolicy.MaxRows <= 0 {
			return nil, fmt.Errorf("sample_policy.max_rows must be greater than 0")
		}
	}
	input, inputHash, err := buildResourceSemanticUnderstandingInput(resource, normalized)
	if err != nil {
		return nil, err
	}
	return &interfaces.SemanticUnderstandingTask{
		Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
		CatalogID:           resource.CatalogID,
		ResourceID:          resource.ID,
		AgentID:             interfaces.SemanticUnderstandingResourceAgentID,
		Input:               input,
		InputHash:           inputHash,
		ApplyMode:           normalized.ApplyMode,
		ConfidenceThreshold: *normalized.ConfidenceThreshold,
	}, nil
}

func normalizeCatalogSemanticUnderstandingRequest(catalog *interfaces.Catalog, resources []*interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, error) {
	normalized := defaultSemanticUnderstandingRequest()
	if req != nil {
		*normalized = *req
		if req.ApplyMode == "" {
			normalized.ApplyMode = interfaces.SemanticUnderstandingApplyModeFillEmpty
		}
		if req.Language == "" {
			normalized.Language = interfaces.DefaultSemanticUnderstandingLanguage
		}
		if normalized.ConfidenceThreshold == nil {
			defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
			normalized.ConfidenceThreshold = &defaultThreshold
		}
	}
	if normalized.IncludeSampleRows || normalized.SamplePolicy != nil {
		return nil, fmt.Errorf("sample rows are only supported for resource semantic understanding task")
	}
	if err := validateSemanticUnderstandingRequest(normalized); err != nil {
		return nil, err
	}
	input, inputHash, err := buildCatalogSemanticUnderstandingInput(catalog, resources, normalized)
	if err != nil {
		return nil, err
	}
	return &interfaces.SemanticUnderstandingTask{
		Scope:               interfaces.SemanticUnderstandingTaskScopeCatalog,
		CatalogID:           catalog.ID,
		AgentID:             interfaces.SemanticUnderstandingCatalogAgentID,
		Input:               input,
		InputHash:           inputHash,
		ApplyMode:           normalized.ApplyMode,
		ConfidenceThreshold: *normalized.ConfidenceThreshold,
	}, nil
}

func validateSemanticUnderstandingRequest(req *interfaces.CreateSemanticUnderstandingTaskRequest) error {
	switch req.ApplyMode {
	case interfaces.SemanticUnderstandingApplyModeDryRun,
		interfaces.SemanticUnderstandingApplyModeFillEmpty,
		interfaces.SemanticUnderstandingApplyModeForce:
	default:
		return fmt.Errorf("invalid apply_mode")
	}
	if req.ConfidenceThreshold == nil || *req.ConfidenceThreshold < 0 || *req.ConfidenceThreshold > 1 {
		return fmt.Errorf("confidence_threshold must be between 0 and 1")
	}
	return nil
}

func defaultSemanticUnderstandingRequest() *interfaces.CreateSemanticUnderstandingTaskRequest {
	defaultThreshold := interfaces.DefaultSemanticUnderstandingConfidenceThreshold
	return &interfaces.CreateSemanticUnderstandingTaskRequest{
		Language:            interfaces.DefaultSemanticUnderstandingLanguage,
		ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
		ConfidenceThreshold: &defaultThreshold,
	}
}

type resourceAgentInput struct {
	Resource   resourceAgentInputResource `json:"resource"`
	SampleRows []map[string]any           `json:"sample_rows,omitempty"`
	Options    resourceAgentInputOptions  `json:"options"`
}

type resourceAgentInputResource struct {
	ID                string                       `json:"id"`
	Name              string                       `json:"name"`
	Category          string                       `json:"category"`
	Database          string                       `json:"database,omitempty"`
	SourceIdentifier  string                       `json:"source_identifier"`
	SourceDescription string                       `json:"source_description,omitempty"`
	Description       string                       `json:"description,omitempty"`
	SchemaDefinition  []resourceAgentInputProperty `json:"schema_definition"`
}

type resourceAgentInputProperty struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	OriginalName        string `json:"original_name,omitempty"`
	OriginalType        string `json:"original_type,omitempty"`
	OriginalDescription string `json:"original_description,omitempty"`
	DisplayName         string `json:"display_name,omitempty"`
	Description         string `json:"description,omitempty"`
}

type resourceAgentInputOptions struct {
	Language            string                                        `json:"language"`
	ApplyMode           string                                        `json:"apply_mode"`
	ConfidenceThreshold float64                                       `json:"confidence_threshold"`
	IncludeSampleRows   bool                                          `json:"include_sample_rows"`
	SamplePolicy        *interfaces.SemanticUnderstandingSamplePolicy `json:"sample_policy,omitempty"`
}

type catalogAgentInput struct {
	Catalog            catalogAgentInputCatalog        `json:"catalog"`
	Resources          []catalogAgentInputResource     `json:"resources"`
	ExistingLogicViews []catalogAgentInputExistingView `json:"existing_logic_views"`
	Options            catalogAgentInputOptions        `json:"options"`
}

type catalogAgentInputCatalog struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type catalogAgentInputResource struct {
	ID                string                       `json:"id"`
	Name              string                       `json:"name"`
	Description       string                       `json:"description,omitempty"`
	Category          string                       `json:"category"`
	Database          string                       `json:"database,omitempty"`
	SourceIdentifier  string                       `json:"source_identifier"`
	SourceDescription string                       `json:"source_description,omitempty"`
	SchemaDefinition  []resourceAgentInputProperty `json:"schema_definition"`
}

type catalogAgentInputExistingView struct {
	ID              string                            `json:"id"`
	Name            string                            `json:"name"`
	Description     string                            `json:"description,omitempty"`
	SourceResources []string                          `json:"source_resources,omitempty"`
	LogicDefinition []*interfaces.LogicDefinitionNode `json:"logic_definition,omitempty"`
}

type catalogAgentInputOptions struct {
	Language            string  `json:"language"`
	ApplyMode           string  `json:"apply_mode"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
}

func buildResourceSemanticUnderstandingInput(resource *interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (string, string, error) {
	input := resourceAgentInput{
		Resource: buildResourceAgentInputResource(resource),
		Options: resourceAgentInputOptions{
			Language:            req.Language,
			ApplyMode:           req.ApplyMode,
			ConfidenceThreshold: *req.ConfidenceThreshold,
			IncludeSampleRows:   req.IncludeSampleRows,
			SamplePolicy:        req.SamplePolicy,
		},
	}
	if req.IncludeSampleRows {
		input.SampleRows = []map[string]any{}
	}
	return marshalSemanticUnderstandingInput(input)
}

func buildCatalogSemanticUnderstandingInput(catalog *interfaces.Catalog, resources []*interfaces.Resource, req *interfaces.CreateSemanticUnderstandingTaskRequest) (string, string, error) {
	sort.SliceStable(resources, func(i, j int) bool {
		return resources[i].ID < resources[j].ID
	})

	input := catalogAgentInput{
		Catalog: catalogAgentInputCatalog{
			ID:   catalog.ID,
			Name: catalog.Name,
		},
		Resources:          []catalogAgentInputResource{},
		ExistingLogicViews: []catalogAgentInputExistingView{},
		Options: catalogAgentInputOptions{
			Language:            req.Language,
			ApplyMode:           req.ApplyMode,
			ConfidenceThreshold: *req.ConfidenceThreshold,
		},
	}
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		if resource.Category == interfaces.ResourceCategoryLogicView {
			input.ExistingLogicViews = append(input.ExistingLogicViews, buildCatalogAgentInputExistingView(resource))
			continue
		}
		input.Resources = append(input.Resources, buildCatalogAgentInputResource(resource))
	}
	return marshalSemanticUnderstandingInput(input)
}

func buildResourceAgentInputResource(resource *interfaces.Resource) resourceAgentInputResource {
	return resourceAgentInputResource{
		ID:               resource.ID,
		Name:             resource.Name,
		Category:         resource.Category,
		Database:         resource.Database,
		SourceIdentifier: resource.SourceIdentifier,
		Description:      resource.Description,
		SchemaDefinition: buildResourceAgentInputProperties(resource.SchemaDefinition),
	}
}

func buildCatalogAgentInputResource(resource *interfaces.Resource) catalogAgentInputResource {
	return catalogAgentInputResource{
		ID:               resource.ID,
		Name:             resource.Name,
		Description:      resource.Description,
		Category:         resource.Category,
		Database:         resource.Database,
		SourceIdentifier: resource.SourceIdentifier,
		SchemaDefinition: buildResourceAgentInputProperties(resource.SchemaDefinition),
	}
}

func buildCatalogAgentInputExistingView(resource *interfaces.Resource) catalogAgentInputExistingView {
	return catalogAgentInputExistingView{
		ID:              resource.ID,
		Name:            resource.Name,
		Description:     resource.Description,
		LogicDefinition: resource.LogicDefinition,
	}
}

func buildResourceAgentInputProperties(properties []*interfaces.Property) []resourceAgentInputProperty {
	result := make([]resourceAgentInputProperty, 0, len(properties))
	for _, property := range properties {
		if property == nil {
			continue
		}
		result = append(result, resourceAgentInputProperty{
			Name:                property.Name,
			Type:                property.Type,
			OriginalName:        property.OriginalName,
			OriginalType:        property.OriginalType,
			OriginalDescription: property.OriginalDescription,
			DisplayName:         property.DisplayName,
			Description:         property.Description,
		})
	}
	return result
}

func marshalSemanticUnderstandingInput(input any) (string, string, error) {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return "", "", err
	}
	inputJSON := string(inputBytes)
	sum := sha256.Sum256([]byte(inputJSON))
	return inputJSON, hex.EncodeToString(sum[:]), nil
}

func accountInfoFromContext(ctx context.Context) interfaces.AccountInfo {
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		if ai, ok := v.(interfaces.AccountInfo); ok {
			return ai
		}
	}
	return interfaces.AccountInfo{}
}
