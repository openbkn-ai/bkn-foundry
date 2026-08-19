// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package build_task provides BuildTask management business logic.
package build_task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/user_mgmt"
)

var (
	btServiceOnce sync.Once
	btService     interfaces.BuildTaskService
)

const buildTaskDispatchBuffer = 1

type buildTaskService struct {
	appSetting *common.AppSetting
	bta        interfaces.BuildTaskAccess
	cs         interfaces.CatalogService
	lim        interfaces.LocalIndexManager // When deleting a task, drop its local index. Test Injection mock
	mfs        interfaces.ModelFactoryService
	rs         interfaces.ResourceService
	ums        interfaces.UserMgmtService

	dispatchCh chan struct{}
}

var activeBuildTaskStatuses = []string{
	interfaces.BuildTaskStatusPending,
	interfaces.BuildTaskStatusRunning,
	interfaces.BuildTaskStatusStopping,
}

// NewBuildTaskService creates a new BuildTaskService.
func NewBuildTaskService(appSetting *common.AppSetting, rs interfaces.ResourceService) interfaces.BuildTaskService {
	btServiceOnce.Do(func() {
		btService = &buildTaskService{
			appSetting: appSetting,
			bta:        logics.BTA,
			cs:         catalog.NewCatalogService(appSetting),
			lim:        local_index.NewLocalIndexManager(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
			rs:         rs,
			ums:        user_mgmt.NewUserMgmtService(appSetting),

			dispatchCh: make(chan struct{}, buildTaskDispatchBuffer),
		}
	})
	return btService
}

func (bts *buildTaskService) DispatchSignal() <-chan struct{} {
	return bts.dispatchCh
}

func (bts *buildTaskService) RequestDispatch() {
	select {
	case bts.dispatchCh <- struct{}{}:
	default:
	}
}

// Create creates a new build task. resource_id is taken from req.
func (bts *buildTaskService) Create(ctx context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create build task")
	defer span.End()

	resourceID := req.ResourceID
	resource, err := bts.rs.GetByID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	// Creating a task is a write on the table it builds, so it needs task_manage
	// there — GetByID above only proves the caller may see the table (#472).
	if err := bts.rs.CheckResourcePermission(ctx, resourceID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return "", err
	}

	if resource.Category != interfaces.ResourceCategoryTable {
		span.SetStatus(codes.Error, "Resource category is not table")
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails("Resource category must be table")
	}
	executeType, err := normalizeCreateBuildTaskExecuteType(ctx, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid execute type")
		return "", err
	}
	if err := validateBuildKeyFields(ctx, resource); err != nil {
		span.SetStatus(codes.Error, "Invalid build key fields")
		return "", err
	}
	req.ExecuteType = executeType

	cat, err := bts.cs.GetByID(ctx, resource.CatalogID, false)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if cat == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if !cat.Enabled {
		span.SetStatus(codes.Error, "Catalog is disabled")
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}

	if err := bts.rejectIfResourceHasActiveTask(ctx, resourceID, ""); err != nil {
		span.SetStatus(codes.Error, "Resource already has active build task")
		return "", err
	}

	buildTask, err := bts.newBuildTaskFromCreateRequest(ctx, resource, req)
	if err != nil {
		return "", err
	}
	if err := validateBuildTaskAnalyzers(ctx, bts.lim, buildTask); err != nil {
		span.SetStatus(codes.Error, "Invalid fulltext analyzer")
		return "", err
	}

	if err := bts.bta.Create(ctx, buildTask); err != nil {
		otellog.LogError(ctx, "Create build task failed", err)
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	bts.RequestDispatch()

	span.SetStatus(codes.Ok, "")
	return buildTask.ID, nil
}

func validateBuildTaskAnalyzers(ctx context.Context, indexManager interfaces.LocalIndexManager, buildTask *interfaces.BuildTask) error {
	if indexManager == nil {
		return nil
	}
	if buildTask == nil || buildTask.IndexConfig == nil {
		return nil
	}
	for field, feature := range buildTask.IndexConfig.Features {
		if feature.Fulltext == nil || strings.TrimSpace(feature.Fulltext.Analyzer) == "" {
			continue
		}
		available, err := indexManager.ValidateAnalyzer(ctx, feature.Fulltext.Analyzer)
		if err != nil {
			var unavailableErr *interfaces.IndexCapabilitiesUnavailableError
			if errors.As(err, &unavailableErr) {
				return rest.NewHTTPError(ctx, http.StatusServiceUnavailable, verrors.VegaBackend_IndexCapability_InternalError_Unavailable).
					WithErrorDetails(err.Error())
			}
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_ValidateAnalyzerFailed).
				WithErrorDetails(err.Error())
		}
		if !available {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_Analyzer).
				WithErrorDetails(fmt.Sprintf("analyzer %q for field %q is unavailable", feature.Fulltext.Analyzer, field))
		}
	}
	return nil
}

func validateBuildKeyFields(ctx context.Context, resource *interfaces.Resource) error {
	if resource.IndexConfig == nil || len(resource.IndexConfig.BuildKeyFields) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields).
			WithErrorDetails("build task requires at least one build_key_fields entry")
	}

	schemaFields := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	unsupportedFields := make([]string, 0)
	for _, prop := range resource.SchemaDefinition {
		if prop == nil {
			continue
		}
		schemaFields[prop.Name] = prop
		if prop.Type == interfaces.DataType_Other {
			unsupportedFields = append(unsupportedFields, fmt.Sprintf("%s (original_type: %s)", prop.Name, prop.OriginalType))
		}
	}
	if len(unsupportedFields) > 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields).
			WithErrorDetails(fmt.Sprintf("resource schema contains unsupported fields: %s", strings.Join(unsupportedFields, ", ")))
	}

	seen := make(map[string]struct{}, len(resource.IndexConfig.BuildKeyFields))
	for _, fieldName := range resource.IndexConfig.BuildKeyFields {
		prop, exists := schemaFields[fieldName]
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields field %q is not in the resource schema", fieldName))
		}
		if _, duplicate := seen[fieldName]; duplicate {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields contains duplicate field %q", fieldName))
		}
		if !interfaces.DataType_IsBuildKey(prop.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields field %q has unsupported type %q", fieldName, prop.Type))
		}
		seen[fieldName] = struct{}{}
	}
	return nil
}

func normalizeCreateBuildTaskExecuteType(ctx context.Context, req *interfaces.CreateBuildTaskRequest) (string, error) {
	if req.Mode == interfaces.BuildTaskModeStreaming {
		if req.ExecuteType != "" {
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidExecuteType).
				WithErrorDetails("execute_type is only supported for batch build tasks")
		}
		return "", nil
	}
	if req.ExecuteType == "" {
		return interfaces.BuildTaskExecuteTypeFull, nil
	}
	if req.ExecuteType != interfaces.BuildTaskExecuteTypeIncremental && req.ExecuteType != interfaces.BuildTaskExecuteTypeFull {
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidExecuteType).
			WithErrorDetails("Invalid execute type")
	}
	return req.ExecuteType, nil
}

func (bts *buildTaskService) rejectIfResourceHasActiveTask(ctx context.Context, resourceID string, excludeTaskID string) error {
	tasks, err := bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 2},
		ResourceID:            resourceID,
		Statuses:              activeBuildTaskStatuses,
	})
	if err != nil {
		otellog.LogError(ctx, "Check active build task failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	for _, task := range tasks {
		if excludeTaskID != "" && task.ID == excludeTaskID {
			continue
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_Exist).
			WithErrorDetails("Resource already has an active build task")
	}
	return nil
}

func (bts *buildTaskService) newBuildTaskFromCreateRequest(ctx context.Context, resource *interfaces.Resource, req *interfaces.CreateBuildTaskRequest) (*interfaces.BuildTask, error) {
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	now := time.Now().UnixMilli()
	buildTask := &interfaces.BuildTask{
		ID:          xid.New().String(),
		ResourceID:  resource.ID,
		CatalogID:   resource.CatalogID,
		Status:      interfaces.BuildTaskStatusPending,
		Mode:        req.Mode,
		ExecuteType: req.ExecuteType,
		Creator:     accountInfo,
		CreateTime:  now,
	}

	if err := bts.fillBuildTaskIndexSnapshot(ctx, resource, buildTask); err != nil {
		return nil, err
	}

	return buildTask, nil
}

func (bts *buildTaskService) fillBuildTaskIndexSnapshot(ctx context.Context, resource *interfaces.Resource, buildTask *interfaces.BuildTask) error {
	for _, property := range resource.SchemaDefinition {
		if property == nil {
			continue
		}
		seenFeatureTypes := make(map[string]struct{}, len(property.Features))
		for _, feature := range property.Features {
			if feature.FeatureType == "" {
				continue
			}
			if _, exists := seenFeatureTypes[feature.FeatureType]; exists {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("property %q has more than one %q feature", property.Name, feature.FeatureType))
			}
			seenFeatureTypes[feature.FeatureType] = struct{}{}
		}
	}
	defaultEmbeddingModel := ""
	defaultFulltextAnalyzer := ""
	buildTask.IndexConfig = &interfaces.BuildTaskIndexConfig{
		Features: map[string]interfaces.BuildTaskFieldIndexFeature{},
	}
	if resource.IndexConfig != nil {
		buildTask.IndexConfig.BuildKeyFields = append([]string(nil), resource.IndexConfig.BuildKeyFields...)
		defaultEmbeddingModel = resource.IndexConfig.DefaultEmbeddingModel
		defaultFulltextAnalyzer = resource.IndexConfig.DefaultFulltextAnalyzer
	}

	embeddingModelCache := map[string]*interfaces.SmallModel{}

	for _, prop := range resource.SchemaDefinition {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			fieldName := prop.Name
			if feature.RefProperty != "" {
				fieldName = feature.RefProperty
			}
			switch feature.FeatureType {
			case interfaces.PropertyFeatureType_Vector:
				modelID := stringConfigValue(feature.Config, "embedding_model")
				if modelID == "" {
					modelID = defaultEmbeddingModel
				}
				if modelID == "" {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_EmbeddingModel).
						WithErrorDetails(fmt.Sprintf("embedding model is required for vector field %q; set config.embedding_model or index_config.default_embedding_model", fieldName))
				}
				model, ok := embeddingModelCache[modelID]
				if !ok {
					var err error
					model, err = bts.mfs.GetModelByID(ctx, modelID)
					if err != nil {
						if errors.Is(err, interfaces.ErrModelNotFound) {
							return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_BuildTask_InvalidParameter_EmbeddingModel).
								WithErrorDetails(fmt.Sprintf("embedding model ID %q for vector field %q not found", modelID, fieldName))
						}
						return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
							WithErrorDetails(err.Error())
					}
					if model == nil || model.ModelID == "" {
						return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_CreateFailed).
							WithErrorDetails(fmt.Sprintf("model %q is unavailable", modelID))
					}
					embeddingModelCache[modelID] = model
				}
				embeddingModel := *model
				fieldConfig := buildTask.IndexConfig.Features[fieldName]
				fieldConfig.Vector = &embeddingModel
				buildTask.IndexConfig.Features[fieldName] = fieldConfig
			case interfaces.PropertyFeatureType_Fulltext:
				analyzer := stringConfigValue(feature.Config, "analyzer")
				if analyzer == "" {
					analyzer = defaultFulltextAnalyzer
				}
				fulltextConfig := interfaces.BuildTaskFulltextConfig{
					Analyzer: analyzer,
				}
				fieldConfig := buildTask.IndexConfig.Features[fieldName]
				fieldConfig.Fulltext = &fulltextConfig
				buildTask.IndexConfig.Features[fieldName] = fieldConfig
			}
		}
	}

	if len(buildTask.IndexConfig.Features) == 0 && len(buildTask.IndexConfig.BuildKeyFields) == 0 {
		buildTask.IndexConfig = nil
	}
	return nil
}

func stringConfigValue(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// GetByID retrieves a build task by ID.
func (bts *buildTaskService) GetByID(ctx context.Context, id string) (*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get build task")
	defer span.End()

	buildTask, err := bts.bta.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask != nil {
		// A task is read through the table it builds (#472).
		if err := bts.rs.CheckResourcePermission(ctx, buildTask.ResourceID,
			interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
			span.SetStatus(codes.Error, "Permission denied")
			return nil, err
		}
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}
	if err := bts.populateBuildTaskReferences(ctx, []*interfaces.BuildTask{buildTask}); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate build task references: %v", err)
	}

	accountInfos := []*interfaces.AccountInfo{&buildTask.Creator}
	if err := bts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate build task account names: %v", err)
	}

	span.SetStatus(codes.Ok, "")
	return buildTask, nil
}

func (bts *buildTaskService) InternalGetByID(ctx context.Context, id string) (*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalGetByID")
	defer span.End()

	return bts.bta.GetByID(ctx, id)
}

func (bts *buildTaskService) InternalGetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.BuildTask, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalGetByCatalogID")
	defer span.End()

	return bts.bta.GetByCatalogID(ctx, catalogID)
}

func (bts *buildTaskService) InternalList(ctx context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalList")
	defer span.End()

	return bts.bta.InternalList(ctx, params)
}

func (bts *buildTaskService) InternalSetProgress(
	ctx context.Context, tx *sql.Tx, id string, progress interfaces.BuildTaskProgress,
) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalSetProgress")
	defer span.End()

	return bts.bta.SetProgress(ctx, tx, id, progress, time.Now().UnixMilli())
}

func (bts *buildTaskService) InternalMarkRunning(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalMarkRunning")
	defer span.End()

	return bts.bta.MarkRunning(ctx, id, time.Now().UnixMilli())
}

func (bts *buildTaskService) InternalMarkFailed(ctx context.Context, id, detail string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalMarkFailed")
	defer span.End()

	return bts.bta.MarkFailed(ctx, id, detail, time.Now().UnixMilli())
}

func (bts *buildTaskService) InternalMarkCancelled(ctx context.Context, id, detail string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalMarkCancelled")
	defer span.End()

	return bts.bta.MarkCancelled(ctx, id, detail, time.Now().UnixMilli())
}

func (bts *buildTaskService) InternalMarkStopped(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalMarkStopped")
	defer span.End()

	return bts.bta.MarkStopped(ctx, id, time.Now().UnixMilli())
}

func (bts *buildTaskService) InternalMarkCompleted(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalMarkCompleted")
	defer span.End()

	return bts.bta.MarkCompleted(ctx, tx, id, time.Now().UnixMilli())
}

// PopulateBuildTaskReferences batch completion task related resources and show directory field. It only queries the current situation
// The entity referenced by the returned task should be avoided to prevent the task list from being triggered by the front end to load the full resource/directory.
func (bts *buildTaskService) populateBuildTaskReferences(ctx context.Context, buildTasks []*interfaces.BuildTask) error {
	if len(buildTasks) == 0 {
		return nil
	}

	resourceIDs := make([]string, 0, len(buildTasks))
	resourceIDSet := make(map[string]struct{}, len(buildTasks))
	catalogIDs := make([]string, 0, len(buildTasks))
	catalogIDSet := make(map[string]struct{}, len(buildTasks))
	for _, buildTask := range buildTasks {
		if buildTask.ResourceID != "" {
			if _, exists := resourceIDSet[buildTask.ResourceID]; !exists {
				resourceIDSet[buildTask.ResourceID] = struct{}{}
				resourceIDs = append(resourceIDs, buildTask.ResourceID)
			}
		}
		if buildTask.CatalogID != "" {
			if _, exists := catalogIDSet[buildTask.CatalogID]; !exists {
				catalogIDSet[buildTask.CatalogID] = struct{}{}
				catalogIDs = append(catalogIDs, buildTask.CatalogID)
			}
		}
	}

	var referenceErrors []error
	resourcesByID := make(map[string]*interfaces.Resource, len(resourceIDs))
	resources, err := bts.rs.InternalGetByIDs(ctx, resourceIDs)
	if err != nil {
		referenceErrors = append(referenceErrors, err)
	} else {
		for _, resource := range resources {
			resourcesByID[resource.ID] = resource
		}
	}

	catalogsByID := make(map[string]*interfaces.Catalog, len(catalogIDs))
	catalogs, err := bts.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		referenceErrors = append(referenceErrors, err)
	} else {
		for _, catalog := range catalogs {
			catalogsByID[catalog.ID] = catalog
		}
	}

	for _, buildTask := range buildTasks {
		if resource := resourcesByID[buildTask.ResourceID]; resource != nil {
			buildTask.ResourceName = resource.Name
		}
		if catalog := catalogsByID[buildTask.CatalogID]; catalog != nil {
			buildTask.CatalogName = catalog.Name
		}
	}
	return errors.Join(referenceErrors...)
}

func (bts *buildTaskService) populateBuildTaskSummaryReferences(ctx context.Context, tasks []*interfaces.BuildTaskSummary) error {
	if len(tasks) == 0 {
		return nil
	}
	resourceIDs := make([]string, 0, len(tasks))
	resourceIDSet := make(map[string]struct{}, len(tasks))
	catalogIDs := make([]string, 0, len(tasks))
	catalogIDSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if _, ok := resourceIDSet[task.ResourceID]; !ok && task.ResourceID != "" {
			resourceIDSet[task.ResourceID] = struct{}{}
			resourceIDs = append(resourceIDs, task.ResourceID)
		}
		if _, ok := catalogIDSet[task.CatalogID]; !ok && task.CatalogID != "" {
			catalogIDSet[task.CatalogID] = struct{}{}
			catalogIDs = append(catalogIDs, task.CatalogID)
		}
	}
	var referenceErrors []error
	resources, err := bts.rs.InternalGetByIDs(ctx, resourceIDs)
	if err != nil {
		referenceErrors = append(referenceErrors, err)
	}
	resourceByID := make(map[string]*interfaces.Resource, len(resources))
	for _, resource := range resources {
		resourceByID[resource.ID] = resource
	}
	catalogs, err := bts.cs.InternalGetByIDs(ctx, catalogIDs)
	if err != nil {
		referenceErrors = append(referenceErrors, err)
	}
	catalogByID := make(map[string]*interfaces.Catalog, len(catalogs))
	for _, catalog := range catalogs {
		catalogByID[catalog.ID] = catalog
	}
	for _, task := range tasks {
		if resource := resourceByID[task.ResourceID]; resource != nil {
			task.ResourceName = resource.Name
		}
		if catalog := catalogByID[task.CatalogID]; catalog != nil {
			task.CatalogName = catalog.Name
		}
	}
	return errors.Join(referenceErrors...)
}

func (bts *buildTaskService) InternalGetStatus(ctx context.Context, id string) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.InternalGetStatus")
	defer span.End()

	return bts.bta.GetStatus(ctx, id)
}

// List retrieves build tasks with filters and pagination.
func (bts *buildTaskService) List(ctx context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List build tasks")
	defer span.End()

	buildTasks, total, err := bts.bta.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List build tasks failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if err := bts.populateBuildTaskSummaryReferences(ctx, buildTasks); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate build task references: %v", err)
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(buildTasks))
	for _, bt := range buildTasks {
		accountInfos = append(accountInfos, &bt.Creator)
	}
	if err := bts.ums.GetAccountNames(ctx, accountInfos); err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate build task account names: %v", err)
	}

	span.SetStatus(codes.Ok, "")
	return buildTasks, total, nil
}

// Start transitions a stopped or failed task to pending before signaling the local worker.
// The worker persists running only after it claims the queued task.
func (bts *buildTaskService) Start(ctx context.Context, taskID string, reset bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Start build task")
	defer span.End()

	buildTask, err := bts.bta.GetByID(ctx, taskID)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}
	if err := bts.rs.CheckResourcePermission(ctx, buildTask.ResourceID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	// Failed tasks may also be restarted; otherwise they become dead ends that must be deleted and rebuilt.
	if buildTask.Status != interfaces.BuildTaskStatusStopped &&
		buildTask.Status != interfaces.BuildTaskStatusFailed {
		span.SetStatus(codes.Error, "Invalid state transition for start")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails(fmt.Sprintf("cannot start task in status: %s", buildTask.Status))
	}

	cat, err := bts.cs.GetByID(ctx, buildTask.CatalogID, false)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if cat == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if !cat.Enabled {
		span.SetStatus(codes.Error, "Catalog is disabled")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}

	if err := bts.rejectIfResourceHasActiveTask(ctx, buildTask.ResourceID, buildTask.ID); err != nil {
		span.SetStatus(codes.Error, "Resource already has active build task")
		return err
	}
	if err := bts.validateStartBuildTaskStillCurrent(ctx, buildTask); err != nil {
		span.SetStatus(codes.Error, "Build task is no longer current")
		return err
	}
	if err := validateBuildTaskAnalyzers(ctx, bts.lim, buildTask); err != nil {
		span.SetStatus(codes.Error, "Invalid fulltext analyzer")
		return err
	}

	// Persist pending before signaling the worker. This prevents a stopped task
	// from being skipped and keeps the running transition owned by execution.
	resetProgress := reset && buildTask.ExecuteType == interfaces.BuildTaskExecuteTypeFull
	updated, err := bts.bta.MarkPending(ctx, taskID, resetProgress)
	if err != nil {
		otellog.LogError(ctx, "Update build task status failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}
	if !updated {
		span.SetStatus(codes.Error, "Build task status changed while starting")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails("build task status changed while starting; retry with the latest task state")
	}

	bts.RequestDispatch()

	span.SetStatus(codes.Ok, "")
	return nil
}

func (bts *buildTaskService) validateStartBuildTaskStillCurrent(ctx context.Context, buildTask *interfaces.BuildTask) error {
	resource, err := bts.rs.GetByID(ctx, buildTask.ResourceID)
	if err != nil {
		otellog.LogError(ctx, "Get resource failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	currentSnapshot := &interfaces.BuildTask{ResourceID: resource.ID, CatalogID: resource.CatalogID}
	if err := bts.fillBuildTaskIndexSnapshot(ctx, resource, currentSnapshot); err != nil {
		return err
	}
	if !reflect.DeepEqual(buildTask.IndexConfig, currentSnapshot.IndexConfig) {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails("resource index config has changed; create a new build task instead")
	}
	if err := validateBuildKeyFields(ctx, resource); err != nil {
		return err
	}

	tasks, err := bts.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{
			Limit:     1,
			Sort:      interfaces.BuildTaskSortCreateTime,
			Direction: interfaces.DESC_DIRECTION,
		},
		ResourceID: buildTask.ResourceID,
		Statuses:   []string{interfaces.BuildTaskStatusCompleted},
	})
	if err != nil {
		otellog.LogError(ctx, "Check latest completed build task failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 && tasks[0].ID != buildTask.ID && tasks[0].CreateTime > buildTask.CreateTime {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails("resource already has a newer completed build task")
	}
	return nil
}

// Stop transitions a queued task directly to stopped, or a running task to stopping.
func (bts *buildTaskService) Stop(ctx context.Context, taskID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Stop build task")
	defer span.End()

	buildTask, err := bts.bta.GetByID(ctx, taskID)
	if err != nil {
		span.SetStatus(codes.Error, "Get build task failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if buildTask == nil {
		span.SetStatus(codes.Error, "Build task not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound)
	}
	if err := bts.rs.CheckResourcePermission(ctx, buildTask.ResourceID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	if buildTask.Status != interfaces.BuildTaskStatusRunning &&
		buildTask.Status != interfaces.BuildTaskStatusPending {
		span.SetStatus(codes.Error, "Invalid state transition for stop")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails(fmt.Sprintf("cannot stop task in status: %s", buildTask.Status))
	}

	// running → stopping: Notifies the worker to exit at the inter-batch checkpoint.
	// pending → stopped: If there are no workers in the queue to observe stopping, stop directly.
	// When dequeuing, if the worker detects "stopped", it will skip and will not be revived for execution.
	var updated bool
	if buildTask.Status == interfaces.BuildTaskStatusPending {
		updated, err = bts.bta.MarkStopped(ctx, taskID, time.Now().UnixMilli())
	} else {
		updated, err = bts.bta.MarkStopping(ctx, taskID)
	}
	if err != nil {
		otellog.LogError(ctx, "Update build task status failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}
	if !updated {
		span.SetStatus(codes.Error, "Build task status changed while stopping")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_InvalidStateTransition).
			WithErrorDetails("build task status changed while stopping; retry with the latest task state")
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteByIDs atomically deletes build tasks by IDs after pre-validating existence and status.
//
// Behavior:
//   - Input IDs are de-duplicated while preserving their first-seen order.
//   - Loads each id; if any missing, returns 404 BuildTask.NotFound with {missing_ids: [...]}
//     unless ignoreMissing=true (then missing ids are dropped from the delete set).
//   - If any task is in running/stopping status, returns 409 HasRunningExecution with {running_ids: [...]}.
//     This check cannot be bypassed.
//   - If any task owns the resource's current LocalIndexName, returns 409 ActiveIndexInUse
//     unless deleteActiveIndex=true. When deleteActiveIndex=true, clears LocalIndexName before deleting.
//   - Deletes all validated task rows in one database statement after dropping their indexes.
func (bts *buildTaskService) DeleteByIDs(ctx context.Context, ids []string, ignoreMissing bool, deleteActiveIndex bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BuildTaskService.DeleteByIDs")
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

	toDelete := make([]*interfaces.BuildTask, 0, len(uniqueIDs))
	missingIDs := make([]string, 0)
	runningIDs := make([]string, 0)
	activeIndexes := make([]map[string]string, 0)
	activeResources := make(map[string]*interfaces.Resource)

	for _, id := range uniqueIDs {
		buildTask, err := bts.bta.GetByID(ctx, id)
		if err != nil {
			span.SetStatus(codes.Error, "Get build task failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}
		if buildTask == nil {
			missingIDs = append(missingIDs, id)
			continue
		}
		if buildTask.Status == interfaces.BuildTaskStatusRunning || buildTask.Status == interfaces.BuildTaskStatusStopping {
			runningIDs = append(runningIDs, id)
			continue
		}
		// Checked per task, before anything is deleted: a batch is one transaction,
		// so one unauthorized id must stop the whole request rather than delete the
		// rest of it (#472).
		if err := bts.rs.CheckResourcePermission(ctx, buildTask.ResourceID, interfaces.OPERATION_TYPE_TASK_MANAGE); err != nil {
			span.SetStatus(codes.Error, "Permission denied")
			return err
		}
		resource, err := bts.rs.GetByID(ctx, buildTask.ResourceID)
		if err != nil {
			span.SetStatus(codes.Error, "Get resource failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}
		if resource != nil {
			idx := interfaces.BuildIndexName(buildTask.ResourceID, buildTask.ID)
			if resource.LocalIndexName == idx {
				activeIndexes = append(activeIndexes, map[string]string{
					"resource_id":   buildTask.ResourceID,
					"build_task_id": buildTask.ID,
					"index_name":    idx,
				})
				activeResources[buildTask.ID] = resource
			}
		}
		toDelete = append(toDelete, buildTask)
	}

	if len(runningIDs) > 0 {
		span.SetStatus(codes.Error, "Some tasks are running or stopping")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
			WithErrorDetails(map[string]any{"running_ids": runningIDs})
	}
	if len(missingIDs) > 0 && !ignoreMissing {
		span.SetStatus(codes.Error, "Some build tasks not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_BuildTask_NotFound).
			WithErrorDetails(map[string]any{"missing_ids": missingIDs})
	}
	if len(activeIndexes) > 0 && !deleteActiveIndex {
		span.SetStatus(codes.Error, "Some build task indexes are currently used by resources")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_ActiveIndexInUse).
			WithErrorDetails(map[string]any{"active_indexes": activeIndexes})
	}
	if deleteActiveIndex {
		for taskID, resource := range activeResources {
			if err := bts.rs.InternalUpdateLocalIndexName(ctx, nil, resource.ID, ""); err != nil {
				span.SetStatus(codes.Error, "Clear active local index failed")
				return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
					WithErrorDetails(map[string]any{
						"build_task_id": taskID,
						"resource_id":   resource.ID,
						"error":         err.Error(),
					})
			}
		}
	}

	deleteIDs := make([]string, 0, len(toDelete))
	for _, bt := range toDelete {
		// Drop the index on a best-effort basis before deleting the task row, consistent with resource and catalog cascades.
		// Semantic consistency is maintained to prevent the deletion of a single UI task from leaving an orphan index (#66 only covers the two paths of resources and directories).
		idx := interfaces.BuildIndexName(bt.ResourceID, bt.ID)
		if err := bts.lim.DeleteIndex(ctx, idx); err != nil {
			otellog.LogError(ctx, fmt.Sprintf("Drop index %s for build task %s failed", idx, bt.ID), err)
		}
		deleteIDs = append(deleteIDs, bt.ID)
	}
	if len(deleteIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	deleted, err := bts.bta.DeleteByIDs(ctx, deleteIDs)
	if err != nil {
		otellog.LogError(ctx, "Delete build tasks failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}
	if deleted != int64(len(deleteIDs)) {
		err = fmt.Errorf("expected to delete %d build tasks, deleted %d", len(deleteIDs), deleted)
		otellog.LogError(ctx, "Delete build tasks affected unexpected rows", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
