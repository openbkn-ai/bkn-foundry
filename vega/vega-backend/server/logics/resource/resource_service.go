// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package resource provides Resource management business logic.
package resource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/local_index"
	model_factory "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/model_factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/query_error"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/user_mgmt"
)

var (
	rServiceOnce sync.Once
	rService     interfaces.ResourceService
)

const (
	resourceParentCleanupTimeout     = 30 * time.Second
	resourcePermissionCleanupTimeout = 30 * time.Second
)

type resourceService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	cs         interfaces.CatalogService
	ds         interfaces.DatasetService
	ps         interfaces.PermissionService
	ra         interfaces.ResourceAccess
	ums        interfaces.UserMgmtService
	bta        interfaces.BuildTaskAccess
	bsa        interfaces.BuildScheduleAccess
	dta        interfaces.DiscoverTaskAccess
	lim        interfaces.LocalIndexManager
	cf         interfaces.ConnectorFactory
	mfs        interfaces.ModelFactoryService
}

// NewResourceService creates a new ResourceService.
func NewResourceService(appSetting *common.AppSetting, datasetService interfaces.DatasetService) interfaces.ResourceService {
	rServiceOnce.Do(func() {
		rService = &resourceService{
			appSetting: appSetting,
			db:         logics.DB,
			cs:         catalog.NewCatalogService(appSetting),
			ds:         datasetService,
			ps:         permission.NewPermissionService(appSetting),
			ra:         logics.RA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			bta:        logics.BTA,
			bsa:        logics.BSA,
			dta:        logics.DTA,
			lim:        local_index.NewLocalIndexManager(appSetting),
			cf:         factory.NewConnectorFactory(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
		}
	})
	return rService
}

// checkResourcePermission asks bkn-safe for the effective decision on the
// resource itself. Resource inheritance and operation requirements belong to
// bkn-safe; Vega does not translate or supplement that decision.
func (rs *resourceService) checkResourcePermission(ctx context.Context,
	resourceID string, builtin bool, op string) error {
	if builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in resources are restricted to the built-in administrator")
	}
	return rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: resourceID,
	}, []string{op})
}

// Create creates a new Resource.
func (rs *resourceService) Create(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create resource")
	defer span.End()

	allowed, parentCatalog, err := rs.cs.CheckCatalogPermission(ctx, req.CatalogID,
		[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Check catalog permission failed")
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.ErrorCode == verrors.VegaBackend_Catalog_NotFound {
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_CatalogNotFound).
				WithErrorDetails(fmt.Sprintf("catalog %s not found", req.CatalogID))
		}
		return nil, err
	}
	if !allowed {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("Access denied: insufficient permissions for catalog's resource_manage operation.")
	}

	// Check a caller-supplied ID only after catalog authorization so create
	// cannot be used to probe existing Resource IDs.
	if req.ID != "" {
		exists, err := rs.checkExistByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_IDExists).
				WithErrorDetails(fmt.Sprintf("id %s already exists", req.ID))
		}
	}

	resourceBuiltin := req.Builtin != nil && *req.Builtin
	if resourceBuiltin != parentCatalog.Builtin {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).WithErrorDetails("resource built_in must match its catalog built_in value")
	}
	if req.Category != interfaces.ResourceCategoryDataset && req.Category != interfaces.ResourceCategoryLogicalView {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_CategoryNotCreatable).
			WithErrorDetails("only dataset and logical_view resources can be created by users; physical resources require discovery")
	}
	resource, err := rs.InternalCreate(ctx, req)
	if err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// prepareNewResource 按类别准备并初始化记录；HTTP 基础字段和公开 Dataset 类型规则仍由请求层负责。
// 内部物理资源保留连接器给出的源 Schema，不执行用户建模的默认值或能力校验。
func (rs *resourceService) prepareNewResource(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	id := req.ID
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate resource UUIDv7: %w", err)
		}
		id = generatedID.String()
	}

	now := time.Now().UnixMilli()
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	resource := &interfaces.Resource{
		ID:               id,
		CatalogID:        req.CatalogID,
		Name:             req.Name,
		Tags:             req.Tags,
		Description:      req.Description,
		Category:         req.Category,
		Builtin:          req.Builtin != nil && *req.Builtin,
		Enabled:          true,
		Status:           req.Status,
		Schema:           req.Schema,
		SourceIdentifier: req.SourceIdentifier,
		SourceMetadata:   req.SourceMetadata,
		SchemaDefinition: req.SchemaDefinition,
		IndexConfig:      req.IndexConfig,
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusUnavailable,
		Creator:          account,
		CreateTime:       now,
		Updater:          account,
		UpdateTime:       now,
	}
	switch resource.Category {
	case interfaces.ResourceCategoryDataset:
		AddDefaultStringAndTextFeatures(resource.SchemaDefinition, resource.IndexConfig)
		if err := validatePropertyFeatures(ctx, resource.SchemaDefinition); err != nil {
			return nil, err
		}
		if err := validateKeywordConfig(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
			return nil, err
		}
		if err := validateDatasetVectorOutputs(ctx, resource.SchemaDefinition); err != nil {
			return nil, err
		}
		// 能力检查必须早于模型调用，并包含省略后将保存为 true 的默认值。
		completeResourceFilterability(resource)
		filterabilityEnabled := false
		for _, property := range resource.SchemaDefinition {
			if *property.Filterable {
				filterabilityEnabled = true
				break
			}
		}
		if filterabilityEnabled {
			if err := mapFilterabilityError(ctx, rs.lim.ValidateFilterability(resource.SchemaDefinition)); err != nil {
				return nil, err
			}
		}
		if err := rs.validateIndexConfigModels(ctx, resource.SchemaDefinition, resource.IndexConfig, resource.Category); err != nil {
			return nil, err
		}
		if err := rs.validateIndexConfigAnalyzers(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
			return nil, err
		}
	case interfaces.ResourceCategoryLogicalView:
		logicalType, fields, err := PrepareLogicalView(ctx, req)
		if err != nil {
			return nil, err
		}
		resource.LogicalType = logicalType
		resource.LogicalViewDefinition = req.LogicalViewDefinition
		resource.SchemaDefinition = fields
		resource.SourceMetadata = req.SourceMetadata
		resource.Schema = ""
		resource.SourceIdentifier = id
		if err := validatePropertyFeatures(ctx, resource.SchemaDefinition); err != nil {
			return nil, err
		}
		completeResourceFilterability(resource)
	}
	if err := encodeResourceLogicFields(resource); err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).WithErrorDetails("failed to encode resource definition")
	}
	return resource, nil
}

// mapFilterabilityError 仅转换过滤能力错误，不读取业务对象或选择执行通道。
func mapFilterabilityError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if httpErr := query_error.DecimalIndexHTTPError(ctx, err); httpErr != nil {
		return httpErr
	}
	if reason, ok := interfaces.RequestSideQueryError(err); ok {
		return unsupportedResourceUpdateError(ctx, reason)
	}
	if errors.Is(err, factory.ErrConnectorEntitlementDenied) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Connector_EntitlementDenied).WithErrorDetails("connector entitlement does not allow filterability validation")
	}
	if errors.Is(err, factory.ErrConnectorDisabled) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Connector_Disabled).WithErrorDetails("connector is disabled")
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).WithErrorDetails("failed to validate field filterability")
}

// Get retrieves a Resource by ID.
func (rs *resourceService) GetByID(ctx context.Context, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resource")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, nil, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	decodeResourceLogicFields(resource)
	completeResourceFilterability(resource)
	populateResourceColumnCount(resource)

	// Apply the fixed internal guard before checking resource permissions.
	if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in resources are restricted to the built-in administrator")
	}

	matchResoucesMap, err := rs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{resource.ID}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	if resrc, exist := matchResoucesMap[resource.ID]; exist {
		resource.Operations = resrc.Operations // The operations that the user is currently permitted to perform
	} else {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
	}

	accountInfos := []*interfaces.AccountInfo{&resource.Creator, &resource.Updater}
	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate resource account names: %v", err)
	}
	span.SetStatus(codes.Ok, "")
	return resource, nil
}

func (rs *resourceService) CheckResourcePermission(ctx context.Context, resourceID string, op string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.CheckResourcePermission")
	defer span.End()

	if resourceID == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("resource_id is required")
	}
	resource, err := rs.ra.GetByID(ctx, nil, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if resource == nil {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
	}

	return rs.checkResourcePermission(ctx, resource.ID, resource.Builtin, op)
}

func (rs *resourceService) InternalGetByID(ctx context.Context, tx *sql.Tx, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByID")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	decodeResourceLogicFields(resource)
	completeResourceFilterability(resource)
	populateResourceColumnCount(resource)
	return resource, nil
}

// InternalGetByIDs is used by the server to batch read the basic information of resources internally without performing permission filtering or loading extended fields.
func (rs *resourceService) InternalGetByIDs(ctx context.Context, ids []string) (map[string]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByIDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return map[string]*interfaces.Resource{}, nil
	}
	resourcesByID, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, err
	}
	for _, resource := range resourcesByID {
		decodeResourceLogicFields(resource)
		completeResourceFilterability(resource)
		populateResourceColumnCount(resource)
	}
	span.SetStatus(codes.Ok, "")
	return resourcesByID, nil
}

// InternalGetByCatalogID is used by the server to internally read the complete resource information under the directory without performing permission filtering.
func (rs *resourceService) InternalGetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByCatalogID")
	defer span.End()

	resources, err := rs.ra.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, err
	}
	for _, resource := range resources {
		decodeResourceLogicFields(resource)
		completeResourceFilterability(resource)
		populateResourceColumnCount(resource)
	}
	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetByIDs retrieves Resources by IDs.
func (rs *resourceService) GetByIDs(ctx context.Context, ids []string, includeRowCount bool) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resources by IDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Resource{}, nil
	}

	resourcesByID, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	resources := make([]*interfaces.Resource, 0, len(resourcesByID))
	for _, id := range ids {
		if resource, exists := resourcesByID[id]; exists {
			resources = append(resources, resource)
		}
	}
	for _, resource := range resources {
		decodeResourceLogicFields(resource)
		completeResourceFilterability(resource)
		populateResourceColumnCount(resource)
	}

	if !interfaces.IsBuiltinAdmin(ctx) {
		for _, resource := range resources {
			if resource.Builtin {
				return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
					WithErrorDetails("built-in resources are restricted to the built-in administrator")
			}
		}
	}
	matchResoucesMap, err := rs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		ids, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	accountInfos := make([]*interfaces.AccountInfo, 0)
	for _, resource := range resources {
		if resrc, exist := matchResoucesMap[resource.ID]; exist {
			resource.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
		accountInfos = append(accountInfos, &resource.Creator, &resource.Updater)
	}

	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate resource account names: %v", err)
	}
	rs.populateResourceRowCounts(ctx, resources, includeRowCount)

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

func (rs *resourceService) populateResourceRowCounts(ctx context.Context, resources []*interfaces.Resource, includeRowCount bool) {
	if !includeRowCount {
		for _, resource := range resources {
			resource.RowCount = nil
			resource.EstimatedRowCount = nil
			resource.RowCountTime = nil
		}
		return
	}
	for _, resource := range resources {
		resource.EstimatedRowCount = nil
		if resource.Category == interfaces.ResourceCategoryDataset {
			resource.RowCount = nil
			resource.RowCountTime = nil
			count, err := rs.ds.CountDocuments(ctx, resource)
			if err != nil {
				logger.Warnf("Failed to populate dataset row count for resource %s: %v", resource.ID, err)
				continue
			}
			resource.RowCount = &count
			countTime := time.Now().UnixMilli()
			resource.RowCountTime = &countTime
			continue
		}
		properties, _ := resource.SourceMetadata["properties"].(map[string]any)
		resource.EstimatedRowCount = sourceMetadataRowCount(properties["estimated_row_count"])
		if resource.RowCount == nil || resource.RowCountTime != nil && *resource.RowCountTime <= 0 {
			resource.RowCountTime = nil
		}
	}
}

// decodeResourceLogicFields maps stored values to the fields owned by the resource category.
func decodeResourceLogicFields(resource *interfaces.Resource) {
	if resource == nil {
		return
	}
	resource.LogicalType = ""
	resource.LogicalViewDefinition = nil
	if resource.Category == interfaces.ResourceCategoryLogicalView {
		resource.LogicalType = resource.LogicType
		if resource.LogicDefinitionStr != "" {
			// Preserve the existing read behavior; view validation handles invalid stored definitions.
			_ = common.UnmarshalPreciseJSON([]byte(resource.LogicDefinitionStr), &resource.LogicalViewDefinition)
		}
	}
}

// completeResourceFilterability resolves legacy schemas on read and normal saves.
func completeResourceFilterability(resource *interfaces.Resource) {
	if resource == nil {
		return
	}
	for _, property := range resource.SchemaDefinition {
		property.SetDefaultFilterable()
	}
}

// encodeResourceLogicFields leaves other categories' stored values intact.
func encodeResourceLogicFields(resource *interfaces.Resource) error {
	if resource.Category != interfaces.ResourceCategoryLogicalView {
		return nil
	}
	encoded, err := json.Marshal(resource.LogicalViewDefinition)
	if err != nil {
		return err
	}
	definition := string(encoded)
	if definition == "null" {
		return fmt.Errorf("logical_view_definition is required")
	}
	resource.LogicType = resource.LogicalType
	resource.LogicDefinitionStr = definition
	return nil
}

func populateResourceColumnCount(resource *interfaces.Resource) {
	if resource == nil {
		return
	}
	if resource.SchemaDefinition == nil {
		resource.ColumnCount = nil
		return
	}
	count := len(resource.SchemaDefinition)
	resource.ColumnCount = &count
}

func sourceMetadataRowCount(value any) *int64 {
	count, ok := common.NumberAsInt64(value)
	if !ok || count < 0 {
		return nil
	}
	return &count
}

// List lists Resources with filters.
func (rs *resourceService) List(ctx context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.ResourceSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List resources")
	defer span.End()

	params.IncludeBuiltin = interfaces.IsBuiltinAdmin(ctx)
	// Query the ids of all resources
	refs, err := rs.ra.ListPermissionRefs(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List resource IDs failed")
		return []*interfaces.ResourceSummary{}, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	if len(refs) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.ResourceSummary{}, 0, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ResourceID)
	}

	// Filter the array of ids with viewing permissions based on the permissions
	// Batch processing, 10,000 ids per batch, fix permission interface error prepared statement contains too many placeholders
	batchSize := 10000
	// All authorized resources and their operation permissions
	matchResourceOpsMap := make(map[string]interfaces.PermissionResourceOps)

	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batchIDs := ids[i:end]

		var batchMatchResources map[string]interfaces.PermissionResourceOps
		// Verify the operation permissions of the permission management
		batchMatchResources, err = rs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			batchIDs, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return []*interfaces.ResourceSummary{}, 0, err
		}

		// Merge results
		for _, resourceOps := range batchMatchResources {
			matchResourceOpsMap[resourceOps.ResourceID] = resourceOps
		}
	}

	// Extract the resource ids with permissions and keep them in the same order as IDS
	authorizedIDs := make([]string, 0, len(matchResourceOpsMap))
	for _, id := range ids {
		if _, exist := matchResourceOpsMap[id]; exist {
			authorizedIDs = append(authorizedIDs, id)
		}
	}
	total := int64(len(authorizedIDs))

	// If there is no authorized resource, return an empty result directly
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.ResourceSummary{}, total, nil
	}

	// Query the complete resource based on the array of authorized ids and apply pagination
	// If limit = -1, all will be returned
	if params.Limit != -1 {
		// Pagination process authorizedIDs
		// Check whether the starting position is out of bounds
		if params.Offset < 0 || params.Offset >= len(authorizedIDs) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.ResourceSummary{}, total, nil
		}
		// Calculate the end position
		end := params.Offset + params.Limit
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		// Only query the resource ID of the current page
		authorizedIDs = authorizedIDs[params.Offset:end]
	}

	// Query the complete resource based on the array of authorized ids
	// Process in batches, 10,000 ids per batch, to avoid the error of prepared statement contains too many placeholders
	summaries := make([]*interfaces.ResourceSummary, 0, len(authorizedIDs))
	queryBatchSize := 10000
	for i := 0; i < len(authorizedIDs); i += queryBatchSize {
		end := i + queryBatchSize
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		batchIDs := authorizedIDs[i:end]

		summariesByID, err := rs.ra.GetSummariesByIDs(ctx, batchIDs)
		if err != nil {
			span.SetStatus(codes.Error, "Get resources by IDs failed")
			return []*interfaces.ResourceSummary{}, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}

		for _, id := range batchIDs {
			if summary, exists := summariesByID[id]; exists {
				if summary.Category == interfaces.ResourceCategoryLogicalView {
					summary.LogicalType = summary.LogicType
				}
				summaries = append(summaries, summary)
			}
		}
	}

	// Set the operation permissions for resources
	for _, c := range summaries {
		if resrc, exist := matchResourceOpsMap[c.ID]; exist {
			c.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(summaries)*2)
	for _, c := range summaries {
		accountInfos = append(accountInfos, &c.Creator, &c.Updater)
	}

	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate resource account names: %v", err)
	}

	span.SetStatus(codes.Ok, "")
	return summaries, total, nil
}

func (rs *resourceService) InternalList(ctx context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.ResourceSummary, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalList")
	defer span.End()
	params.IncludeBuiltin = true
	summaries, _, err := rs.ra.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List resources failed")
		return nil, err
	}
	for _, summary := range summaries {
		if summary.Category == interfaces.ResourceCategoryLogicalView {
			summary.LogicalType = summary.LogicType
		}
	}
	span.SetStatus(codes.Ok, "")
	return summaries, nil
}

// Update updates a Resource.
func (rs *resourceService) Update(ctx context.Context, req *interfaces.ResourceRequest) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource")
	defer span.End()

	if err := rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		ID:   req.ID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return err
	}

	for _, property := range req.SchemaDefinition {
		if property == nil || property.Filterable == nil {
			return unsupportedResourceUpdateError(ctx, "schema_definition fields require a boolean filterable")
		}
	}

	resource, err := rs.ra.GetByID(ctx, nil, req.ID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	decodeResourceLogicFields(resource)
	completeResourceFilterability(resource)
	if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in resources are restricted to the built-in administrator")
	}
	if req.Builtin != nil && *req.Builtin != resource.Builtin {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("resource built_in is immutable")
	}
	if req.Enabled != resource.Enabled {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_EnabledFieldNotAllowed).
			WithErrorDetails("use POST /resources/{id}/enable or /disable to change enabled state")
	}

	// 重新保存 table/dataset 是升级 string/text 默认特征契约的边界。
	// 读取和构建历史 Schema 时不补齐，以便构建请求明确提示用户重新保存配置。
	if (resource.Category == interfaces.ResourceCategoryTable || resource.Category == interfaces.ResourceCategoryDataset) && req.SchemaDefinition != nil {
		indexConfig := req.IndexConfig
		if indexConfig == nil {
			indexConfig = resource.IndexConfig
		}
		AddDefaultStringAndTextFeatures(req.SchemaDefinition, indexConfig)
	}

	buildRelevantChanged, err := rs.validateResourceUpdateScope(ctx, resource, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid resource update scope")
		return err
	}
	if req.SchemaDefinition != nil && resource.Category != interfaces.ResourceCategoryLogicalView {
		filterabilityEnabled := false
		for _, property := range req.SchemaDefinition {
			if *property.Filterable {
				filterabilityEnabled = true
				break
			}
		}
		if filterabilityEnabled {
			var filterabilityErr error
			switch resource.Category {
			case interfaces.ResourceCategoryDataset:
				filterabilityErr = rs.lim.ValidateFilterability(req.SchemaDefinition)
			default:
				catalog, err := rs.cs.InternalGetByID(ctx, resource.CatalogID, false)
				if err != nil {
					return err
				}
				if catalog == nil {
					return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
				}
				candidate := *resource
				candidate.SchemaDefinition = req.SchemaDefinition
				filterabilityErr = rs.cf.ValidateFilterability(catalog.ConnectorType, &candidate)
			}
			if err := mapFilterabilityError(ctx, filterabilityErr); err != nil {
				return err
			}
		}
	}
	if resource.Category == interfaces.ResourceCategoryDataset && resource.LocalIndexName != "" {
		if field := changedDatasetFulltextAnalyzer(resource, req); field != "" {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("dataset fulltext analyzer for field %q cannot be changed on an existing index; rebuild the dataset instead", field))
		}
	}
	// Older Dataset rows can have a vector feature without its persisted
	// dimension. Completing that metadata must also pass through the index
	// mapping update; otherwise the Resource row and physical index contract
	// could diverge even though this is not a user-requested schema change.
	legacyDatasetVectorDimensions := resource.Category == interfaces.ResourceCategoryDataset &&
		hasMissingVectorFeatureDimensions(resource.SchemaDefinition)
	if buildRelevantChanged {
		if err := rs.rejectResourceOperationWhenActiveBuildTask(ctx, resource.ID, true); err != nil {
			span.SetStatus(codes.Error, "Resource has active build task")
			return err
		}
	}
	if resource.Category == interfaces.ResourceCategoryDataset && buildRelevantChanged {
		// Existing documents were materialized against the current index contract.
		// Do not allow a normal Resource update to leave them under a different
		// mapping or embedding configuration; that requires an explicit rebuild.
		result, err := rs.ds.ListDocuments(ctx, resource,
			&interfaces.ResourceDataQueryParams{
				Paging: interfaces.PagingRequest{Limit: 1},
			})
		if err != nil {
			return err
		}
		if len(result.Entries) > 0 {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails("dataset index structure cannot be changed while it contains documents; rebuild the dataset instead")
		}
	}
	previousFingerprint := ""
	keyFieldsChanged := req.IndexConfig != nil && indexConfigKeyFieldsChanged(resource.IndexConfig, req.IndexConfig)
	if buildRelevantChanged {
		previousFingerprint, err = resourceLocalIndexMappingFingerprint(resource)
		if err != nil {
			span.SetStatus(codes.Error, "Fingerprint current resource index config failed")
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("invalid current resource index configuration: %v", err))
		}
	}

	switch resource.Category {
	case interfaces.ResourceCategoryLogicalView:
		resource.SchemaDefinition = req.SchemaDefinition
		resource.LogicalType = req.LogicalType
		resource.LogicalViewDefinition = req.LogicalViewDefinition
		resource.SourceMetadata = req.SourceMetadata
	default:
		resource.SchemaDefinition = applyMutableSchemaFields(
			resource.SchemaDefinition,
			req.SchemaDefinition,
			resource.Category == interfaces.ResourceCategoryDataset,
		)
	}
	if req.IndexConfig != nil {
		resource.IndexConfig = req.IndexConfig
	}

	if err := validatePropertyFeatures(ctx, resource.SchemaDefinition); err != nil {
		return err
	}
	if resource.Category == interfaces.ResourceCategoryTable || resource.Category == interfaces.ResourceCategoryDataset {
		if err := validateKeywordConfig(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
			return err
		}
	}
	if resource.Category == interfaces.ResourceCategoryTable {
		if err := validateLocalIndexVectorOutputs(ctx, resource.SchemaDefinition); err != nil {
			return err
		}
	}
	if resource.Category == interfaces.ResourceCategoryDataset {
		if err := validateDatasetVectorOutputs(ctx, resource.SchemaDefinition); err != nil {
			return err
		}
	}
	if err := rs.validateIndexConfigModels(ctx, resource.SchemaDefinition, resource.IndexConfig, resource.Category); err != nil {
		return err
	}
	if err := rs.validateIndexConfigAnalyzers(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
		return err
	}
	currentFingerprint := ""
	if buildRelevantChanged {
		currentFingerprint, err = resourceLocalIndexMappingFingerprint(resource)
		if err != nil {
			span.SetStatus(codes.Error, "Fingerprint updated resource index config failed")
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("invalid updated resource index configuration: %v", err))
		}
	}

	// Check if the catalog exists
	exists, err := rs.cs.CheckExistByID(ctx, req.CatalogID)
	if err != nil {
		return err
	}
	if !exists {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	// Apply updates
	resource.Name = req.Name
	resource.Tags = req.Tags
	resource.Description = req.Description
	resource.Enabled = req.Enabled

	// Get account info
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	now := time.Now().UnixMilli()
	resource.Updater = accountInfo
	resource.UpdateTime = now

	completeResourceFilterability(resource)
	if err := encodeResourceLogicFields(resource); err != nil {
		span.SetStatus(codes.Error, "Encode resource definition failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to encode resource definition")
	}

	tx, err := rs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Update resource transaction failed")
		otellog.LogError(ctx, "Update resource transaction failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	defer func() { _ = tx.Rollback() }()

	rowsAffected, err := rs.ra.Update(ctx, tx, resource, req.ExpectedUpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Update resource failed")
		otellog.LogError(ctx, "Update resource failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	if rowsAffected == 0 {
		span.SetStatus(codes.Error, "Resource update conflict")
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	if resource.Category == interfaces.ResourceCategoryLogicalView && buildRelevantChanged {
		resource.RowCount = nil
		resource.RowCountTime = nil
		if _, err := rs.ra.UpdateRowCount(ctx, tx, resource, resource.UpdateTime); err != nil {
			otellog.LogError(ctx, "Clear changed view row count failed", err)
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update resource")
		}
	}
	if resource.Category == interfaces.ResourceCategoryDataset && (buildRelevantChanged || legacyDatasetVectorDimensions) {
		// Claim the resource version before changing OpenSearch. The transaction
		// keeps concurrent updates from publishing an unpersisted mapping while
		// an OpenSearch rejection still rolls the Resource update back.
		if err := rs.ds.Update(ctx, resource); err != nil {
			return err
		}
	}
	indexContractChanged := buildRelevantChanged && previousFingerprint != currentFingerprint &&
		resource.LocalIndexStatus == interfaces.ResourceLocalIndexStatusAvailable
	if indexContractChanged {
		updated, err := rs.ra.UpdateLocalIndexState(ctx, tx, resource.ID,
			interfaces.ResourceLocalIndexStatusStale, resource.LocalIndexName, "")
		if err != nil || !updated {
			span.SetStatus(codes.Error, "Mark resource local index stale failed")
			if err != nil {
				otellog.LogError(ctx, "Mark resource local index stale failed", err)
			}
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to mark resource local index stale")
		}
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusStale
		resource.SyncMark = ""
	} else if keyFieldsChanged {
		if resource.SyncMark != "" {
			updated, err := rs.ra.UpdateLocalIndexState(ctx, tx, resource.ID,
				resource.LocalIndexStatus, resource.LocalIndexName, "")
			if err != nil || !updated {
				return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
					WithErrorDetails("failed to clear resource incremental checkpoint")
			}
		}
		resource.SyncMark = ""
	}
	if err := tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit resource update transaction failed")
		otellog.LogError(ctx, "Commit resource update transaction failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	if resource.CatalogID != "" {
		parentCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceParentCleanupTimeout)
		err := rs.ps.UpsertResourceParents(parentCtx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []interfaces.PermissionResourceParent{{
				ResourceID: resource.ID, ParentID: resource.CatalogID,
			}})
		cancel()
		if err != nil {
			logger.Errorf("Upsert resource parent after resource update: resource %s: %v", resource.ID, err)
			span.SetStatus(codes.Error, "failed to register resource parent")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// jsonNumbersEqual compares JSON-backed values without treating float64 and
// json.Number representations of the same number as a schema change.
func jsonNumbersEqual(current, next any) bool {
	currentValue, err := comparableJSONValue(current)
	if err != nil {
		return false
	}
	nextValue, err := comparableJSONValue(next)
	return err == nil && reflect.DeepEqual(currentValue, nextValue)
}

func comparableJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := common.UnmarshalPreciseJSON(encoded, &decoded); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(decoded), nil
}

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if number, ok := new(big.Rat).SetString(string(v)); ok {
			return struct{ Number string }{number.RatString()}
		}
		return v
	case []any:
		for i := range v {
			v[i] = normalizeJSONNumbers(v[i])
		}
	case map[string]any:
		for key := range v {
			v[key] = normalizeJSONNumbers(v[key])
		}
	}
	return value
}

func hasMissingVectorFeatureDimensions(schema []*interfaces.Property) bool {
	for _, property := range schema {
		for _, feature := range property.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			if feature.Config == nil {
				return true
			}
			if _, exists := feature.Config["dimension"]; !exists {
				return true
			}
		}
	}
	return false
}

func indexConfigKeyFieldsChanged(current, requested *interfaces.ResourceIndexConfig) bool {
	if requested == nil {
		return false
	}
	if current == nil {
		return len(requested.PrimaryKeyFields) > 0 || len(requested.IncrementalFields) > 0
	}
	return !slices.Equal(current.PrimaryKeyFields, requested.PrimaryKeyFields) ||
		!slices.Equal(current.IncrementalFields, requested.IncrementalFields)
}

func resourceLocalIndexMappingFingerprint(resource *interfaces.Resource) (string, error) {
	resourceCopy := *resource
	if resource.IndexConfig != nil {
		indexConfigCopy := *resource.IndexConfig
		indexConfigCopy.PrimaryKeyFields = nil
		indexConfigCopy.IncrementalFields = nil
		resourceCopy.IndexConfig = &indexConfigCopy
	}
	return ResourceIndexConfigFingerprint(&resourceCopy)
}

// SetEnabled changes only a Resource's enabled state.
func (rs *resourceService) SetEnabled(ctx context.Context, id string, enabled bool) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Set resource enabled")
	defer span.End()

	if err := rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		ID:   id,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return nil, err
	}

	resource, err := rs.ra.GetByID(ctx, nil, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	decodeResourceLogicFields(resource)
	completeResourceFilterability(resource)
	if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in resources are restricted to the built-in administrator")
	}
	if resource.Enabled == enabled {
		span.SetStatus(codes.Ok, "")
		return resource, nil
	}

	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}
	if err := rs.ra.UpdateEnabled(ctx, resource.ID, enabled, time.Now().UnixMilli(), accountInfo); err != nil {
		span.SetStatus(codes.Error, "Set resource enabled failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// UpdateStatus updates a Resource's status.
func (rs *resourceService) UpdateStatus(ctx context.Context, id string, status string, statusMessage string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource status")
	defer span.End()

	if err := rs.ra.UpdateStatus(ctx, nil, id, status, statusMessage); err != nil {
		span.SetStatus(codes.Error, "Update resource status failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateDiscoverStatus updates a Resource's last discover status.
func (rs *resourceService) UpdateDiscoverStatus(ctx context.Context, id string, status string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource discover status")
	defer span.End()

	if err := rs.ra.UpdateDiscoverStatus(ctx, id, status); err != nil {
		span.SetStatus(codes.Error, "Update resource discover status failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteByIDs deletes Resources by IDs.
func (rs *resourceService) DeleteByIDs(ctx context.Context, ids []string, ignoreMissing, onlyIfStale bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete resources")
	defer span.End()

	if ignoreMissing && onlyIfStale {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails("only_if_stale cannot be combined with ignore_missing")
	}
	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	// Ask bkn-safe for explicit per-ID delete decisions before loading rows. With
	// ignoreMissing, denied IDs are resolved after loading and accepted only when
	// they are actually absent.
	requirements := make([]interfaces.PermissionRequirement, 0, len(ids))
	for _, id := range ids {
		requirements = append(requirements, interfaces.PermissionRequirement{
			Resource: interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
				ID:   id,
			},
			Operation: interfaces.OPERATION_TYPE_DELETE,
		})
	}
	decisions, err := rs.ps.CheckPermissions(ctx, requirements)
	if err != nil {
		span.SetStatus(codes.Error, "Check resource permissions error")
		return err
	}
	allowedByID := make(map[string]bool, len(decisions))
	for _, decision := range decisions {
		allowedByID[decision.ResourceID] = decision.Allowed
	}
	if !ignoreMissing {
		for _, id := range ids {
			if allowedByID[id] {
				continue
			}
			return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
				WithErrorDetails("Access denied: insufficient permissions for resource's delete operation.")
		}
	}

	// Load the requested Resources once so built-in visibility and the deletion
	// lifecycle use the same immutable rows.
	resourcesByID, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	existingIDs := make([]string, 0, len(resourcesByID))
	resources := make([]*interfaces.Resource, 0, len(resourcesByID))
	for _, id := range ids {
		if resource, exists := resourcesByID[id]; exists {
			existingIDs = append(existingIDs, id)
			resources = append(resources, resource)
		}
	}

	if ignoreMissing {
		for _, id := range existingIDs {
			if !allowedByID[id] {
				return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
					WithErrorDetails("Access denied: insufficient permissions for resource's delete operation.")
			}
		}
	}
	if !interfaces.IsBuiltinAdmin(ctx) {
		for _, resource := range resources {
			if resource.Builtin {
				return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
					WithErrorDetails("built-in resources are restricted to the built-in administrator")
			}
		}
	}

	if !ignoreMissing {
		for _, id := range ids {
			if _, exists := resourcesByID[id]; !exists {
				return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
					WithErrorDetails(fmt.Sprintf("id %s not found", id))
			}
		}
	}
	if len(existingIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if onlyIfStale {
		for _, resource := range resources {
			if resource.Status != interfaces.ResourceStatusStale || resource.LastDiscoverStatus != interfaces.DiscoverStatusMissing {
				return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_DeleteConflict)
			}
		}
	}

	for _, resource := range resources {
		if err := rs.rejectResourceOperationWhenActiveDiscoverTask(ctx, resource.ID); err != nil {
			span.SetStatus(codes.Error, "Active resource refresh prevents resource deletion")
			return err
		}
		if err := rs.rejectResourceOperationWhenActiveBuildTask(ctx, resource.ID, false); err != nil {
			span.SetStatus(codes.Error, "Active build task prevents resource deletion")
			return err
		}
	}

	tx, err := rs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Begin resource deletion transaction failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete resources")
	}
	defer func() { _ = tx.Rollback() }()

	err = rs.ra.DeleteByIDs(ctx, tx, existingIDs, onlyIfStale)
	if err == nil {
		err = rs.bsa.DeleteByResourceIDs(ctx, tx, existingIDs)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if errors.Is(err, interfaces.ErrResourceDeleteConditionNotMet) {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				span.SetStatus(codes.Error, "Rollback conditional resource deletion failed")
				return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
					WithErrorDetails("failed to delete resources")
			}
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_DeleteConflict)
		}
		span.SetStatus(codes.Error, "Delete resources failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete resources")
	}

	parentIDs := make([]string, 0, len(resources))
	for _, resource := range resources {
		parentIDs = append(parentIDs, resource.ID)
	}
	if len(parentIDs) > 0 {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceParentCleanupTimeout)
		if cleanupErr := rs.ps.DeleteResourceParents(cleanupCtx,
			interfaces.AUTH_RESOURCE_TYPE_RESOURCE, parentIDs); cleanupErr != nil {
			// The local rows are already gone. Keep deleting the remaining
			// external state; an orphan parent edge is safer than restoring an
			// edge for a Resource that no longer exists.
			logger.Errorf("Delete resource parents after resource deletion: %v", cleanupErr)
		}
		cancel()
	}

	for _, resource := range resources {
		if resource.Category == interfaces.ResourceCategoryDataset {
			if err := rs.ds.Delete(ctx, resource); err != nil {
				logger.Errorf("Delete dataset failed after resource deletion: %v", err)
			}
		}
	}

	permissionCleanupCtx, cancelPermissionCleanup := context.WithTimeout(
		context.WithoutCancel(ctx), resourcePermissionCleanupTimeout)
	permissionCleanupErr := rs.ps.DeleteResources(permissionCleanupCtx,
		interfaces.AUTH_RESOURCE_TYPE_RESOURCE, existingIDs)
	cancelPermissionCleanup()
	if permissionCleanupErr != nil {
		logger.Errorf("Delete resource permissions after resource deletion: %v", permissionCleanupErr)
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// checkExistByID checks if a resource exists by ID.
func (rs *resourceService) checkExistByID(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check resource exist by ID")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, nil, id)
	if err != nil {
		span.SetStatus(codes.Error, "GetByID failed")
		return false, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resource != nil, nil
}

func (rs *resourceService) InternalUpdateLocalIndexName(ctx context.Context, tx *sql.Tx, id, localIndexName string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateLocalIndexName")
	defer span.End()

	return rs.ra.UpdateLocalIndexName(ctx, tx, id, localIndexName)
}

func (rs *resourceService) InternalUpdateLocalIndexState(ctx context.Context, tx *sql.Tx, id string,
	localIndexStatus string, localIndexName string, syncMark string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateLocalIndexState")
	defer span.End()

	return rs.ra.UpdateLocalIndexState(ctx, tx, id, localIndexStatus, localIndexName, syncMark)
}

func (rs *resourceService) InternalUpdateSemanticMetadata(ctx context.Context,
	tx *sql.Tx, resource *interfaces.Resource, expectedUpdateTime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateSemanticMetadata")
	defer span.End()

	completeResourceFilterability(resource)
	if err := encodeResourceLogicFields(resource); err != nil {
		span.SetStatus(codes.Error, "Encode resource definition failed")
		return err
	}
	rowsAffected, err := rs.ra.UpdateSemanticMetadata(ctx, tx, resource, expectedUpdateTime)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	return nil
}

func (rs *resourceService) InternalUpdateDiscoveryMetadata(ctx context.Context, tx *sql.Tx, resource *interfaces.Resource,
	expectedUpdateTime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateDiscoveryMetadata")
	defer span.End()

	if resource == nil {
		span.SetStatus(codes.Error, "Resource is required")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	ownedTx := false
	if tx == nil {
		var err error
		tx, err = rs.db.BeginTx(ctx, nil)
		if err != nil {
			span.SetStatus(codes.Error, "Begin discovery metadata transaction failed")
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update discovery metadata")
		}
		ownedTx = true
		defer func() { _ = tx.Rollback() }()
	}

	current, err := rs.ra.GetByID(ctx, tx, resource.ID)
	if err != nil {
		span.SetStatus(codes.Error, "Read resource for discovery metadata update failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update discovery metadata")
	}
	if current == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	storedProperties := make(map[string]*interfaces.Property, len(current.SchemaDefinition))
	for _, property := range current.SchemaDefinition {
		storedProperties[property.Name] = property
	}
	for _, property := range resource.SchemaDefinition {
		if stored, exists := storedProperties[property.Name]; exists && stored.Filterable != nil {
			property.Filterable = stored.Filterable
		}
		property.SetDefaultFilterable()
	}
	// 扫描结果必须可形成有效契约；旧契约无法读取时允许修复，但不能继续信任已有索引。
	previousFingerprint, previousFingerprintErr := ResourceIndexConfigFingerprint(current)
	currentFingerprint, err := ResourceIndexConfigFingerprint(resource)
	if err != nil {
		span.SetStatus(codes.Error, "Fingerprint discovered resource failed")
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(fmt.Sprintf("invalid discovered resource index configuration: %v", err))
	}

	rowsAffected, err := rs.ra.UpdateDiscoveryMetadata(ctx, tx, resource, expectedUpdateTime)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	if (previousFingerprintErr != nil || previousFingerprint != currentFingerprint) && current.LocalIndexStatus == interfaces.ResourceLocalIndexStatusAvailable {
		updated, err := rs.ra.UpdateLocalIndexState(ctx, tx, current.ID,
			interfaces.ResourceLocalIndexStatusStale, current.LocalIndexName, "")
		if err != nil || !updated {
			span.SetStatus(codes.Error, "Mark discovered resource local index stale failed")
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to mark resource local index stale")
		}
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusStale
		resource.LocalIndexName = current.LocalIndexName
		resource.SyncMark = ""
	}
	if ownedTx {
		if err := tx.Commit(); err != nil {
			span.SetStatus(codes.Error, "Commit discovery metadata transaction failed")
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update discovery metadata")
		}
	}
	return nil
}

// InternalCreate 为已授权内部流程准备资源并管理完整创建事务；失败先回滚，再尽力清理外部副作用。
func (rs *resourceService) InternalCreate(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalCreate")
	defer span.End()

	resource, err := rs.prepareNewResource(ctx, req)
	if err != nil {
		return nil, err
	}

	if resource.Category == interfaces.ResourceCategoryDataset {
		if err := rs.ds.Create(ctx, resource); err != nil {
			return nil, err
		}
	}

	datasetSnapshot := *resource
	completed := false
	parentAttempted := false
	defer func() {
		if completed || !parentAttempted && resource.Category != interfaces.ResourceCategoryDataset {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceParentCleanupTimeout)
		defer cancel()

		if parentAttempted {
			if err := rs.ps.DeleteResourceParents(cleanupCtx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{resource.ID}); err != nil {
				logger.Errorf("Delete resource parent after resource creation failure: resource %s: %v", resource.ID, err)
			}
		}
		if resource.Category == interfaces.ResourceCategoryDataset {
			if err := rs.ds.Delete(cleanupCtx, &datasetSnapshot); err != nil {
				logger.Errorf("Delete dataset index after resource creation failure: resource %s: %v", resource.ID, err)
			}
		}
	}()

	tx, err := rs.db.BeginTx(ctx, nil)
	if err != nil {
		otellog.LogError(ctx, "Create resource transaction failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).WithErrorDetails("failed to create resource")
	}
	defer func() { _ = tx.Rollback() }()

	if err := rs.ra.Create(ctx, tx, resource); err != nil {
		otellog.LogError(ctx, "Create resource failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).WithErrorDetails("failed to create resource")
	}

	// 登记可能已部分生效的外部副作用，以便 Upsert 或后续 Commit 失败时清理。
	parentAttempted = true
	parentCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceParentCleanupTimeout)
	err = rs.ps.UpsertResourceParents(parentCtx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		[]interfaces.PermissionResourceParent{{ResourceID: resource.ID, ParentID: resource.CatalogID}})
	cancel()
	if err != nil {
		logger.Errorf("Upsert resource parent after resource creation: resource %s: %v", resource.ID, err)
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		otellog.LogError(ctx, "Commit resource creation transaction failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).WithErrorDetails("failed to create resource")
	}
	completed = true
	span.SetStatus(codes.Ok, "")
	return resource, nil
}

func (rs *resourceService) InternalUpdateStatus(ctx context.Context, tx *sql.Tx, id string, status string, statusMessage string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	return rs.ra.UpdateStatus(ctx, tx, id, status, statusMessage)
}

func (rs *resourceService) rejectResourceOperationWhenActiveBuildTask(ctx context.Context, resourceID string, includePending bool) error {
	statuses := []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}
	if includePending {
		statuses = append([]string{interfaces.BuildTaskStatusPending}, statuses...)
	}
	tasks, err := rs.bta.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 1},
		ResourceID:            resourceID,
		Statuses:              statuses,
	})
	if err != nil {
		otellog.LogError(ctx, "Check active build task failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 {
		if includePending && tasks[0].Status == interfaces.BuildTaskStatusPending {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_Exist).
				WithErrorDetails("resource has a pending build task; wait until it finishes")
		}
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
			WithErrorDetails("resource has a running build task; wait until it finishes")
	}
	return nil
}

func (rs *resourceService) rejectResourceOperationWhenActiveDiscoverTask(ctx context.Context, resourceID string) error {
	tasks, err := rs.dta.InternalList(ctx, interfaces.DiscoverTaskQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 1},
		ResourceID:            resourceID,
		Statuses: []string{
			interfaces.DiscoverTaskStatusPending,
			interfaces.DiscoverTaskStatusRunning,
		},
	})
	if err != nil {
		otellog.LogError(ctx, "Check active resource refresh task failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_DiscoverTask_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict,
			verrors.VegaBackend_DiscoverTask_ResourceRefreshInProgress).
			WithErrorDetails("resource has a pending or running metadata refresh task; wait until it finishes")
	}
	return nil
}

func (rs *resourceService) validateResourceUpdateScope(ctx context.Context,
	resource *interfaces.Resource, req *interfaces.ResourceRequest) (bool, error) {
	if req.CatalogID != resource.CatalogID {
		return false, unsupportedResourceUpdateError(ctx, "catalog_id cannot be updated")
	}
	if req.Category == "" {
		return false, unsupportedResourceUpdateError(ctx, "category is required")
	}
	if req.Category != resource.Category {
		return false, unsupportedResourceUpdateError(ctx, "category cannot be updated")
	}
	if resource.Category == interfaces.ResourceCategoryLogicalView {
		if req.LogicalType != resource.LogicalType {
			return false, unsupportedResourceUpdateError(ctx, "logical_type cannot be changed")
		}
		req.SourceMetadata = nil
		logicalType, viewFields, err := PrepareLogicalView(ctx, req)
		if err != nil {
			return false, err
		}
		if logicalType != resource.LogicalType {
			return false, unsupportedResourceUpdateError(ctx, "logical_type cannot be changed")
		}
		requestedFilterability := make(map[string]*bool, len(req.SchemaDefinition))
		for _, property := range req.SchemaDefinition {
			requestedFilterability[property.Name] = property.Filterable
		}
		for _, property := range viewFields {
			if value := requestedFilterability[property.Name]; value != nil {
				property.Filterable = value
			}
			property.SetDefaultFilterable()
		}
		req.SchemaDefinition = viewFields
		// Filterability restricts query use without changing materialized data.
		comparableSchemas := make([][]*interfaces.Property, 2)
		for i, schema := range [][]*interfaces.Property{resource.SchemaDefinition, viewFields} {
			comparableSchemas[i] = make([]*interfaces.Property, len(schema))
			for j, property := range schema {
				copy := *property
				copy.Filterable = nil
				comparableSchemas[i][j] = &copy
			}
		}
		return !jsonNumbersEqual(resource.LogicalViewDefinition, req.LogicalViewDefinition) ||
			!jsonNumbersEqual(comparableSchemas[0], comparableSchemas[1]), nil
	}
	indexConfigChanged := req.IndexConfig != nil && !reflect.DeepEqual(resource.IndexConfig, req.IndexConfig)
	if req.SchemaDefinition == nil {
		return indexConfigChanged, nil
	}
	// A stored legacy schema can contain a self-referencing ref_property written by the platform before
	// migration. The request was normalized at ingress; normalize both sides before comparison so an
	// ordinary edit without schema changes is not classified as a build-related change.
	NormalizeSelfReferencingFeatures(resource.SchemaDefinition)
	schemaChanged, err := validateMutableSchemaUpdate(
		ctx,
		resource.SchemaDefinition,
		req.SchemaDefinition,
		resource.Category == interfaces.ResourceCategoryDataset,
	)
	return schemaChanged || indexConfigChanged, err
}

func (rs *resourceService) validateIndexConfigModels(ctx context.Context, schema []*interfaces.Property,
	indexConfig *interfaces.ResourceIndexConfig, category string) error {
	if err := validateIndexConfigKeyFields(ctx, schema, indexConfig); err != nil {
		return err
	}
	defaultEmbeddingModelID := ""
	if indexConfig != nil {
		defaultEmbeddingModelID = strings.TrimSpace(indexConfig.DefaultEmbeddingModel)
	}
	models := map[string]*interfaces.SmallModel{}
	for _, prop := range schema {
		for i := range prop.Features {
			feature := &prop.Features[i]
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			if feature.RefProperty != "" && feature.RefProperty != prop.Name {
				if len(feature.Config) > 0 {
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
						WithErrorDetails(fmt.Sprintf("vector feature on field %q that references %q must not define config", prop.Name, feature.RefProperty))
				}
				continue
			}

			fieldName := prop.Name

			modelID := ""
			if feature.Config != nil {
				if value, ok := feature.Config["embedding_model"].(string); ok {
					modelID = strings.TrimSpace(value)
				}
			}
			if modelID == "" {
				modelID = defaultEmbeddingModelID
			}
			if modelID == "" {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("embedding model is required for vector field %q; set config.embedding_model or index_config.default_embedding_model", fieldName))
			}

			model, ok := models[modelID]
			if !ok {
				var err error
				model, err = rs.mfs.GetModelByID(ctx, modelID)
				if err != nil || model == nil || model.EmbeddingDim <= 0 {
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
						WithErrorDetails(fmt.Sprintf("embedding model ID %q for field %q not found", modelID, fieldName))
				}

				models[modelID] = model
			}
			if feature.Config == nil {
				feature.Config = map[string]any{}
			}
			feature.Config["dimension"] = model.EmbeddingDim
		}
	}
	// A derived view may expose only the field carrying a source Feature while
	// its referenced vector field remains private. EE preparation validates the
	// reference against the complete source schema before reaching this point.
	if category != interfaces.ResourceCategoryLogicalView {
		if err := ValidateVectorFeatureReferences(schema); err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(err.Error())
		}
	}
	return nil
}

func (rs *resourceService) validateIndexConfigAnalyzers(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	defaultAnalyzer := ""
	if indexConfig != nil {
		defaultAnalyzer = strings.TrimSpace(indexConfig.DefaultFulltextAnalyzer)
	}
	for _, prop := range schema {
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Fulltext {
				continue
			}
			analyzer := strings.TrimSpace(fulltextAnalyzerConfigValue(feature.Config))
			if analyzer == "" {
				analyzer = defaultAnalyzer
			}
			if analyzer == "" {
				continue
			}
			fieldName := prop.Name
			if feature.RefProperty != "" {
				fieldName = feature.RefProperty
			}
			available, err := rs.lim.ValidateAnalyzer(ctx, analyzer)
			if err != nil {
				var unavailableErr *interfaces.IndexCapabilitiesUnavailableError
				if errors.As(err, &unavailableErr) {
					return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, verrors.VegaBackend_IndexCapability_InternalError_Unavailable).
						WithErrorDetails(err.Error())
				}
				return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails(err.Error())
			}
			if !available {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_Analyzer).
					WithErrorDetails(fmt.Sprintf("analyzer %q for field %q is unavailable", analyzer, fieldName))
			}
		}
	}
	return nil
}

// fulltextAnalyzerConfigFor 返回 Dataset mapping 使用的字段级 analyzer 配置。
// 缺省配置与显式 standard 必须区分；Dataset mapping 不读取资源默认 analyzer。
func fulltextAnalyzerConfigFor(prop *interfaces.Property) (string, bool) {
	for _, feature := range prop.Features {
		if feature.FeatureType == interfaces.PropertyFeatureType_Fulltext {
			return strings.TrimSpace(fulltextAnalyzerConfigValue(feature.Config)), true
		}
	}
	return "", false
}

// changedDatasetFulltextAnalyzer 检查已有全文字段的 mapping 分词器配置是否改变。
// 空索引也不能通过更新 mapping 修改 analyzer。
func changedDatasetFulltextAnalyzer(resource *interfaces.Resource, req *interfaces.ResourceRequest) string {
	requestedSchema := req.SchemaDefinition
	if requestedSchema == nil {
		requestedSchema = resource.SchemaDefinition
	}
	current := make(map[string]string, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		if analyzer, exists := fulltextAnalyzerConfigFor(prop); exists {
			current[prop.Name] = analyzer
		}
	}
	for _, prop := range requestedSchema {
		analyzer, exists := fulltextAnalyzerConfigFor(prop)
		if !exists {
			continue
		}
		if previous, exists := current[prop.Name]; exists && previous != analyzer {
			return prop.Name
		}
	}
	return ""
}

func fulltextAnalyzerConfigValue(config map[string]any) string {
	if config == nil {
		return ""
	}
	value, _ := config["analyzer"].(string)
	return value
}

func validateIndexConfigKeyFields(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if indexConfig == nil {
		return nil
	}

	schemaFields := make(map[string]*interfaces.Property, len(schema))
	for _, prop := range schema {
		schemaFields[prop.Name] = prop
	}
	primaryKeys := make(map[string]struct{}, len(indexConfig.PrimaryKeyFields))
	for _, field := range indexConfig.PrimaryKeyFields {
		prop, exists := schemaFields[field]
		if !exists {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields field %q is not in the resource schema", field))
		}
		if _, duplicate := primaryKeys[field]; duplicate {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields contains duplicate field %q", field))
		}
		if !interfaces.IndexConfig_IsPrimaryKeyType(prop.Type) {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields field %q has unsupported type %q", field, prop.Type))
		}
		primaryKeys[field] = struct{}{}
	}

	incrementalKeys := make(map[string]struct{}, len(indexConfig.IncrementalFields))
	for _, field := range indexConfig.IncrementalFields {
		prop, exists := schemaFields[field]
		if !exists {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields field %q is not in the resource schema", field))
		}
		if _, duplicate := incrementalKeys[field]; duplicate {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields contains duplicate field %q", field))
		}
		if !interfaces.IndexConfig_IsIncrementalFieldType(prop.Type) {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields field %q has unsupported type %q", field, prop.Type))
		}
		incrementalKeys[field] = struct{}{}
	}
	return nil
}

func unsupportedResourceUpdateError(ctx context.Context, details string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
		WithErrorDetails(details)
}

func validatePropertyFeatures(ctx context.Context, schema []*interfaces.Property) error {
	for _, property := range schema {
		seenTypes := make(map[string]struct{}, len(property.Features))
		seenNames := make(map[string]struct{}, len(property.Features))
		for _, feature := range property.Features {
			if strings.HasPrefix(feature.FeatureName, property.Name+".") {
				return unsupportedResourceUpdateError(ctx, fmt.Sprintf(
					"feature name %q must be relative to property %q", feature.FeatureName, property.Name))
			}
			if feature.FeatureName != "" {
				if _, exists := seenNames[feature.FeatureName]; exists {
					return unsupportedResourceUpdateError(ctx, fmt.Sprintf("property %q has more than one feature named %q", property.Name, feature.FeatureName))
				}
				seenNames[feature.FeatureName] = struct{}{}
			}
			if feature.FeatureType == "" {
				continue
			}
			if _, exists := seenTypes[feature.FeatureType]; exists {
				return unsupportedResourceUpdateError(ctx, fmt.Sprintf("property %q has more than one %q feature", property.Name, feature.FeatureType))
			}
			seenTypes[feature.FeatureType] = struct{}{}
		}
	}
	return nil
}

func validateKeywordConfig(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if indexConfig != nil && indexConfig.DefaultKeywordIgnoreAbove != nil {
		value := *indexConfig.DefaultKeywordIgnoreAbove
		if value < 1 || value > interfaces.MaxKeywordIgnoreAbove {
			return unsupportedResourceUpdateError(ctx, fmt.Sprintf(
				"index_config.default_keyword_ignore_above must be an integer between 1 and %d",
				interfaces.MaxKeywordIgnoreAbove))
		}
	}
	for _, property := range schema {
		for _, feature := range property.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Keyword {
				continue
			}
			value, exists := feature.Config["ignore_above"]
			limit, valid := positiveIntegerConfigValue(value)
			if !exists || !valid || limit > interfaces.MaxKeywordIgnoreAbove {
				return unsupportedResourceUpdateError(ctx, fmt.Sprintf(
					"keyword feature on property %q must define config.ignore_above as an integer between 1 and %d",
					property.Name, interfaces.MaxKeywordIgnoreAbove))
			}
		}
	}
	return nil
}

// validateLocalIndexVectorOutputs reserves generated *_vector field names for
// string/text vector features that do not reuse an existing vector field.
func validateLocalIndexVectorOutputs(ctx context.Context, schema []*interfaces.Property) error {
	logicalFields := make(map[string]struct{}, len(schema))
	for _, property := range schema {
		logicalFields[property.Name] = struct{}{}
	}
	for _, property := range schema {
		if property.Type != interfaces.DataType_String && property.Type != interfaces.DataType_Text {
			continue
		}
		for _, feature := range property.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector || feature.RefProperty != "" {
				continue
			}
			generatedField := local_index.VectorFieldName(property.Name)
			if _, exists := logicalFields[generatedField]; exists {
				return unsupportedResourceUpdateError(ctx, fmt.Sprintf(
					"generated vector field %q conflicts with a logical property %q; set ref_property to reuse an existing vector field",
					generatedField, generatedField))
			}
		}
	}
	return nil
}

// validateDatasetVectorOutputs reserves generated *_vector field names for
// string/text vector features. Dataset does not support ref_property, so a
// feature always derives from the property it belongs to.
func validateDatasetVectorOutputs(ctx context.Context, schema []*interfaces.Property) error {
	if err := validateLocalIndexVectorOutputs(ctx, schema); err != nil {
		return err
	}
	for _, property := range schema {
		for _, feature := range property.Features {
			// HTTP validation rejects this already. Keep the Dataset business
			// invariant here as well because internal callers can bypass the
			// HTTP adapter and call the service directly.
			if feature.RefProperty != "" {
				return unsupportedResourceUpdateError(ctx, "dataset does not support ref_property")
			}
		}
	}
	return nil
}

func validateMutableSchemaUpdate(ctx context.Context, current []*interfaces.Property, requested []*interfaces.Property, allowPropertyAdditions bool) (bool, error) {
	if !allowPropertyAdditions && len(current) != len(requested) {
		return false, unsupportedResourceUpdateError(ctx, "schema_definition can only update field display_name, description, filterable, and features")
	}
	if len(requested) < len(current) {
		return false, unsupportedResourceUpdateError(ctx, "schema_definition cannot remove or rename fields")
	}

	currentByName := make(map[string]*interfaces.Property, len(current))
	for _, prop := range current {
		if prop.Name == "" {
			return false, unsupportedResourceUpdateError(ctx, "current schema_definition contains an invalid field")
		}
		currentByName[prop.Name] = prop
	}

	schemaChanged := false
	seen := make(map[string]struct{}, len(requested))
	for _, requestedProp := range requested {
		if requestedProp.Name == "" {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition contains an invalid field")
		}
		if _, dup := seen[requestedProp.Name]; dup {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition contains duplicate fields")
		}
		seen[requestedProp.Name] = struct{}{}
		currentProp, ok := currentByName[requestedProp.Name]
		if !ok {
			if !allowPropertyAdditions {
				return false, unsupportedResourceUpdateError(ctx, "schema_definition cannot add, remove, or rename fields")
			}
			schemaChanged = true
			continue
		}

		currentComparable := *currentProp
		requestedComparable := *requestedProp
		currentComparable.DisplayName = ""
		currentComparable.Description = ""
		currentComparable.Features = nil
		currentComparable.Filterable = nil
		requestedComparable.DisplayName = ""
		requestedComparable.Description = ""
		requestedComparable.Features = nil
		requestedComparable.Filterable = nil
		if !jsonNumbersEqual(currentComparable, requestedComparable) {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition can only update field display_name, description, filterable, and features")
		}
		if !mutableFeaturesEqual(currentProp.Features, requestedProp.Features) {
			schemaChanged = true
		}
	}
	for name := range currentByName {
		if _, ok := seen[name]; !ok {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition cannot remove or rename fields")
		}
	}
	return schemaChanged, nil
}

// mutableFeaturesEqual compares the index-relevant semantics of features for
// every Resource category that supports mutable feature configuration. Display
// metadata, persisted service flags, and order do not change the index contract.
// A missing vector dimension is treated as an omitted server-maintained value;
// an explicitly supplied dimension remains part of the comparison.
func mutableFeaturesEqual(current, requested []interfaces.PropertyFeature) bool {
	if len(current) != len(requested) {
		return false
	}
	currentCopy := append([]interfaces.PropertyFeature(nil), current...)
	requestedCopy := append([]interfaces.PropertyFeature(nil), requested...)
	normalize := func(features []interfaces.PropertyFeature) {
		for i := range features {
			features[i].DisplayName = ""
			features[i].Description = ""
			features[i].IsDefault = false
			features[i].IsNative = false
			if len(features[i].Config) == 0 {
				features[i].Config = nil
			}
		}
		slices.SortFunc(features, func(left, right interfaces.PropertyFeature) int {
			if result := strings.Compare(left.FeatureType, right.FeatureType); result != 0 {
				return result
			}
			if result := strings.Compare(left.FeatureName, right.FeatureName); result != 0 {
				return result
			}
			return strings.Compare(left.RefProperty, right.RefProperty)
		})
	}
	normalize(currentCopy)
	normalize(requestedCopy)
	for i := range currentCopy {
		if currentCopy[i].FeatureType != interfaces.PropertyFeatureType_Vector ||
			requestedCopy[i].FeatureType != interfaces.PropertyFeatureType_Vector {
			continue
		}
		if _, supplied := requestedCopy[i].Config["dimension"]; supplied {
			continue
		}
		config := make(map[string]any, len(currentCopy[i].Config))
		for key, value := range currentCopy[i].Config {
			if key != "dimension" {
				config[key] = value
			}
		}
		if len(config) == 0 && len(requestedCopy[i].Config) == 0 {
			// A legacy nil config and an omitted request config are equivalent once
			// the server-maintained dimension is excluded from this comparison.
			currentCopy[i].Config = nil
			requestedCopy[i].Config = nil
		} else {
			currentCopy[i].Config = config
		}
	}
	return jsonNumbersEqual(currentCopy, requestedCopy)
}

// InitializeJSONIndexAttributes 为 JSON 字段记录托管索引仅保存原值的统一策略。
// 已有 Dataset 字段不调用此函数，避免改变不可变的 enabled 参数。
func InitializeJSONIndexAttributes(schema []*interfaces.Property) {
	for _, prop := range schema {
		if prop.Type != interfaces.DataType_Json {
			continue
		}
		attributes := make(map[string]any, len(prop.Attributes)+1)
		for key, value := range prop.Attributes {
			attributes[key] = value
		}
		attributes["enabled"] = false
		prop.Attributes = attributes
	}
}

func applyMutableSchemaFields(current []*interfaces.Property, requested []*interfaces.Property, allowPropertyAdditions bool) []*interfaces.Property {
	if requested == nil {
		return current
	}
	currentByName := make(map[string]*interfaces.Property, len(current))
	for _, prop := range current {
		currentByName[prop.Name] = prop
	}
	for _, requestedProp := range requested {
		if currentProp, ok := currentByName[requestedProp.Name]; ok {
			currentProp.DisplayName = requestedProp.DisplayName
			currentProp.Description = requestedProp.Description
			currentProp.Filterable = requestedProp.Filterable
			currentProp.Features = requestedProp.Features
			continue
		}
		if allowPropertyAdditions {
			InitializeJSONIndexAttributes([]*interfaces.Property{requestedProp})
			current = append(current, requestedProp)
		}
	}
	return current
}

// ListAuthResourceEntries lists resource authorization entries with filters.
func (rs *resourceService) ListAuthResourceEntries(ctx context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListAuthResourceEntries")
	defer span.End()

	params.IncludeBuiltin = interfaces.IsBuiltinAdmin(ctx)
	entries, total, err := rs.ra.ListAuthResourceEntries(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResourceEntries failed")
		return []*interfaces.AuthResourceEntry{}, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(entries) == 0 {
		return []*interfaces.AuthResourceEntry{}, total, nil
	}

	span.SetStatus(codes.Ok, "")
	return entries, total, nil
}

// CheckExistByCategories checks if Resources exists by catalog ID and categories.
func (rs *resourceService) CheckExistByCategories(ctx context.Context, catalogID string, categories []string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckExistByCategories")
	defer span.End()

	return rs.ra.CheckExistByCategories(ctx, catalogID, categories)
}

func (rs *resourceService) InternalUpdateRowCount(ctx context.Context, tx *sql.Tx, source *interfaces.Resource, count, countTime int64) error {
	if source == nil || count < 0 || countTime <= 0 {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody)
	}
	source.RowCount = &count
	source.RowCountTime = &countTime
	previous := source.UpdateTime
	// 数量采集不改变资源定义版本，避免使数据游标和用户编辑失效。

	updated, err := rs.ra.UpdateRowCount(ctx, tx, source, previous)
	if err != nil {
		return err
	}
	if updated != 1 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	return nil
}
