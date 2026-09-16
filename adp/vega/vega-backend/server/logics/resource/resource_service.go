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
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/permission"
	"vega-backend/logics/user_mgmt"
)

var (
	rServiceOnce sync.Once
	rService     interfaces.ResourceService
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
	dta        interfaces.DiscoverTaskAccess
	lim        interfaces.LocalIndexManager
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
			dta:        logics.DTA,
			lim:        local_index.NewLocalIndexManager(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
		}
	})
	return rService
}

// checkResourcePermission asks bkn-safe for the effective decision on the
// resource itself. Resource inheritance and operation requirements belong to
// bkn-safe; Vega does not translate or supplement that decision.
func (rs *resourceService) checkResourcePermission(ctx context.Context,
	resourceID string, internal bool, op string) error {
	if internal && !interfaces.IsBuiltinAdmin(ctx) {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("internal resources are restricted to the built-in administrator")
	}
	return rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: resourceID,
	}, []string{op})
}

// partitionResourceIDs groups resource ids based on whether they belong to an internal system directory
func partitionResourceIDs(ids []string, internalSet map[string]struct{}) (normalIDs, internalIDs []string) {
	normalIDs = make([]string, 0, len(ids))
	internalIDs = make([]string, 0)
	for _, id := range ids {
		if _, ok := internalSet[id]; ok {
			internalIDs = append(internalIDs, id)
		} else {
			normalIDs = append(normalIDs, id)
		}
	}
	return normalIDs, internalIDs
}

// filterResourcePermissions excludes internal resources for non-admin callers,
// then evaluates the remaining resources in bkn-safe's resource hierarchy.
func (rs *resourceService) filterResourcePermissions(ctx context.Context, ids []string,
	internalSet map[string]struct{}, ops []string, allowOperation bool) (map[string]interfaces.PermissionResourceOps, error) {
	if !interfaces.IsBuiltinAdmin(ctx) {
		visibleIDs, _ := partitionResourceIDs(ids, internalSet)
		if len(visibleIDs) == 0 {
			return map[string]interfaces.PermissionResourceOps{}, nil
		}
		ids = visibleIDs
	}

	return rs.ps.FilterResources(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ids,
		ops, allowOperation, interfaces.COMMON_OPERATIONS)
}

// Create creates a new Resource.
func (rs *resourceService) Create(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create resource")
	defer span.End()

	// Only the built-in admin may create a resource under an internal catalog.
	internalCatalogs, err := rs.cs.InternalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	_, parentInternal := internalCatalogs[req.CatalogID]
	if parentInternal && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("internal resources are restricted to the built-in administrator")
	}

	// Creating a table is authorised by the target catalog's resource_manage
	// (#801). A table is always created INSIDE a catalog, so "may create a table"
	// and "may act on this catalog" are the same question — and the old check
	// could not answer it: it asked resource:* + create, and a wildcard object
	// does not say which catalog the table lands in, so whoever held it could
	// create a table anywhere.
	//
	// The legacy verb is deliberately NOT asked as a second chance. A custom role
	// still carrying resource:*/create loses table creation on upgrade, and that
	// is the intended outcome: it is the grant that could not name a catalog.
	// Re-grant those roles resource_manage on the catalogs they should manage.
	if err = rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   req.CatalogID,
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}); err != nil {
		return nil, err
	}

	// Get account info from context
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	// Check if the catalog exists
	exists, err := rs.cs.CheckExistByID(ctx, req.CatalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Check catalog exist failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if !exists {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	resourceInternal := req.Internal != nil && *req.Internal
	if resourceInternal != parentInternal {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("resource internal must match its catalog internal value")
	}

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate resource UUIDv7: %w", err)
		}
		id = generatedID.String()
	}

	var logicType string
	switch req.Category {
	case interfaces.ResourceCategoryLogicView:
		logicType, err = rs.validateLogicDefinition(ctx, req)
		if err != nil {
			return nil, err
		}
		viewFields, err := rs.parseLogicDefinition(ctx, req.LogicDefinition)
		if err != nil {
			return nil, err
		}
		req.SchemaDefinition = viewFields
		if req.SourceIdentifier == "" {
			req.SourceIdentifier = fmt.Sprintf("%s.%s", req.CatalogID, id)
		}
	}
	if (req.Category == interfaces.ResourceCategoryTable || req.Category == interfaces.ResourceCategoryDataset) && req.SchemaDefinition != nil {
		AddDefaultStringAndTextFeatures(req.SchemaDefinition, req.IndexConfig)
	}

	if err := validateSchemaDefinition(ctx, req.SchemaDefinition); err != nil {
		return nil, err
	}
	if req.Category == interfaces.ResourceCategoryTable || req.Category == interfaces.ResourceCategoryDataset {
		if err := validateKeywordConfig(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
			return nil, err
		}
	}
	if req.Category == interfaces.ResourceCategoryTable {
		if err := validateLocalIndexVectorOutputs(ctx, req.SchemaDefinition); err != nil {
			return nil, err
		}
	}
	if req.Category == interfaces.ResourceCategoryDataset {
		if err := validateDatasetVectorOutputs(ctx, req.SchemaDefinition); err != nil {
			return nil, err
		}
	}
	if err := rs.validateIndexConfigModels(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
		return nil, err
	}
	if err := rs.validateIndexConfigAnalyzers(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
		return nil, err
	}
	resource := &interfaces.Resource{
		ID:               id,
		CatalogID:        req.CatalogID,
		Name:             req.Name,
		Tags:             req.Tags,
		Description:      req.Description,
		Category:         req.Category,
		Internal:         resourceInternal,
		Enabled:          true,
		Status:           req.Status,
		Schema:           req.Schema,
		SourceIdentifier: req.SourceIdentifier,
		SourceMetadata:   req.SourceMetadata,
		SchemaDefinition: req.SchemaDefinition,
		IndexConfig:      req.IndexConfig,
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusUnavailable,
		LogicType:        logicType,
		LogicDefinition:  req.LogicDefinition,
		Creator:          accountInfo,
		CreateTime:       now,
		Updater:          accountInfo,
		UpdateTime:       now,
	}

	if resource.Category == interfaces.ResourceCategoryDataset {
		if err := rs.ds.Create(ctx, resource); err != nil {
			return nil, err
		}
	}

	tx, err := rs.db.BeginTx(ctx, nil)
	if err != nil {
		otellog.LogError(ctx, "Create resource transaction failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("failed to create resource")
	}
	defer func() { _ = tx.Rollback() }()

	err = rs.ra.Create(ctx, tx, resource)
	if err != nil {
		otellog.LogError(ctx, "Create resource failed", err)
		if resource.Category == interfaces.ResourceCategoryDataset {
			if deleteErr := rs.ds.Delete(ctx, resource); deleteErr != nil {
				logger.Errorf("Delete dataset index after resource creation failed: resource %s: %v", resource.ID, deleteErr)
			}
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("failed to create resource")
	}

	if err := tx.Commit(); err != nil {
		otellog.LogError(ctx, "Commit resource creation transaction failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("failed to create resource")
	}

	// Register resources.
	if resource.CatalogID != "" {
		err = rs.ps.UpsertResourceParents(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []interfaces.PermissionResourceParent{{
				ResourceID: resource.ID, ParentID: resource.CatalogID,
			}})
		if err != nil {
			logger.Errorf("UpsertResourceParents error: %s", err.Error())
			span.SetStatus(codes.Error, "failed to register resource parent")
			return nil, err
		}
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// Get retrieves a Resource by ID.
func (rs *resourceService) GetByID(ctx context.Context, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resource")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, nil, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	populateResourceColumnCount(resource)

	// Apply the fixed internal guard before checking resource permissions.
	if resource.Internal && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("internal resources are restricted to the built-in administrator")
	}

	internalResources := map[string]struct{}{}
	if resource.Internal {
		internalResources[resource.ID] = struct{}{}
	}
	matchResoucesMap, err := rs.filterResourcePermissions(ctx, []string{resource.ID}, internalResources,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	if resrc, exist := matchResoucesMap[resource.ID]; exist {
		resource.Operations = resrc.Operations // The operations that the user is currently permitted to perform
	} else {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("resource_id is required")
	}
	resource, err := rs.ra.GetByID(ctx, nil, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if resource == nil {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
	}

	return rs.checkResourcePermission(ctx, resource.ID, resource.Internal, op)
}

func (rs *resourceService) InternalGetByID(ctx context.Context, tx *sql.Tx, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByID")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
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
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	resources := make([]*interfaces.Resource, 0, len(resourcesByID))
	for _, id := range ids {
		if resource, exists := resourcesByID[id]; exists {
			resources = append(resources, resource)
			delete(resourcesByID, id)
		}
	}
	for _, resource := range resources {
		populateResourceColumnCount(resource)
	}

	// The shared permission filter enforces the internal visibility boundary.
	internalResources := make(map[string]struct{})
	for _, resource := range resources {
		if resource.Internal {
			internalResources[resource.ID] = struct{}{}
		}
	}
	matchResoucesMap, err := rs.filterResourcePermissions(ctx, ids, internalResources,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	accountInfos := make([]*interfaces.AccountInfo, 0)
	for _, resource := range resources {
		if resrc, exist := matchResoucesMap[resource.ID]; exist {
			resource.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
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
		}
		return
	}
	for _, resource := range resources {
		if resource.Category == interfaces.ResourceCategoryDataset {
			count, err := rs.ds.CountDocuments(ctx, resource)
			if err != nil {
				logger.Warnf("Failed to populate dataset row count for resource %s: %v", resource.ID, err)
				resource.RowCount = nil
				continue
			}
			resource.RowCount = &count
			continue
		}
		count, ok := sourceMetadataRowCount(resource.SourceMetadata)
		if !ok {
			resource.RowCount = nil
			continue
		}
		resource.RowCount = &count
	}
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

func sourceMetadataRowCount(sourceMetadata map[string]any) (int64, bool) {
	if sourceMetadata == nil {
		return 0, false
	}
	properties, ok := sourceMetadata["properties"].(map[string]any)
	if !ok {
		return 0, false
	}
	value, ok := properties["row_count"]
	if !ok {
		return 0, false
	}
	count, ok := common.NumberAsInt64(value)
	if !ok || count < 0 {
		return 0, false
	}
	return count, true
}

// GetByCatalogID retrieves all Resources under a Catalog.
func (rs *resourceService) GetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resources by catalog ID")
	defer span.End()
	internalCatalogs, err := rs.cs.InternalCatalogIDSet(ctx)
	if err != nil {
		return nil, err
	}
	if _, internal := internalCatalogs[catalogID]; internal && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("internal resources are restricted to the built-in administrator")
	}

	resources, err := rs.ra.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	for _, resource := range resources {
		populateResourceColumnCount(resource)
	}

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetByName retrieves a Resource by catalog and name.
func (rs *resourceService) GetByName(ctx context.Context, catalogID string, name string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resource by name")
	defer span.End()
	internalCatalogs, err := rs.cs.InternalCatalogIDSet(ctx)
	if err != nil {
		return nil, err
	}
	if _, internal := internalCatalogs[catalogID]; internal && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("internal resources are restricted to the built-in administrator")
	}

	resource, err := rs.ra.GetByName(ctx, catalogID, name)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	populateResourceColumnCount(resource)

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// List lists Resources with filters.
func (rs *resourceService) List(ctx context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.ResourceSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List resources")
	defer span.End()

	params.IncludeInternal = interfaces.IsBuiltinAdmin(ctx)
	// Query the ids of all resources
	refs, err := rs.ra.ListPermissionRefs(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List resource IDs failed")
		return []*interfaces.ResourceSummary{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
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
		batchMatchResources, err = rs.filterResourcePermissions(ctx, batchIDs, nil,
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
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
			return []*interfaces.ResourceSummary{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}

		for _, id := range batchIDs {
			if summary, exists := summariesByID[id]; exists {
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
	params.IncludeInternal = true
	summaries, _, err := rs.ra.List(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List resources failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return summaries, nil
}

// Update updates a Resource.
func (rs *resourceService) Update(ctx context.Context, resource *interfaces.Resource, req *interfaces.ResourceRequest) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource")
	defer span.End()

	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	if req.Internal != nil && *req.Internal != resource.Internal {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("resource internal is immutable")
	}
	if err := rs.checkResourcePermission(ctx, resource.ID, resource.Internal,
		interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
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
		documents, _, err := rs.ds.ListDocuments(ctx, resource,
			&interfaces.ResourceDataQueryParams{
				Paging: interfaces.PagingRequest{Limit: 1},
			})
		if err != nil {
			return err
		}
		if len(documents) > 0 {
			return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails("dataset index structure cannot be changed while it contains documents; rebuild the dataset instead")
		}
	}
	previousFingerprint := ""
	keyFieldsChanged := req.IndexConfig != nil && indexConfigKeyFieldsChanged(resource.IndexConfig, req.IndexConfig)
	if buildRelevantChanged {
		previousFingerprint, err = resourceLocalIndexMappingFingerprint(resource)
		if err != nil {
			span.SetStatus(codes.Error, "Fingerprint current resource index config failed")
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("invalid current resource index configuration: %v", err))
		}
	}

	switch resource.Category {
	case interfaces.ResourceCategoryLogicView:
		logicType, err := rs.validateLogicDefinition(ctx, req)
		if err != nil {
			return err
		}
		viewFields, err := rs.parseLogicDefinition(ctx, req.LogicDefinition)
		if err != nil {
			return err
		}
		resource.SchemaDefinition = viewFields
		resource.LogicType = logicType
		resource.LogicDefinition = req.LogicDefinition
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

	if err := validateSchemaDefinition(ctx, resource.SchemaDefinition); err != nil {
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
	if err := rs.validateIndexConfigModels(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
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
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("invalid updated resource index configuration: %v", err))
		}
	}

	// Check if the catalog exists
	exists, err := rs.cs.CheckExistByID(ctx, req.CatalogID)
	if err != nil {
		return err
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
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

	tx, err := rs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Update resource transaction failed")
		otellog.LogError(ctx, "Update resource transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	defer func() { _ = tx.Rollback() }()

	rowsAffected, err := rs.ra.Update(ctx, tx, resource, req.ExpectedUpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Update resource failed")
		otellog.LogError(ctx, "Update resource failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	if rowsAffected == 0 {
		span.SetStatus(codes.Error, "Resource update conflict")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
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
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to mark resource local index stale")
		}
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusStale
		resource.SyncMark = ""
	} else if keyFieldsChanged {
		if resource.SyncMark != "" {
			updated, err := rs.ra.UpdateLocalIndexState(ctx, tx, resource.ID,
				resource.LocalIndexStatus, resource.LocalIndexName, "")
			if err != nil || !updated {
				return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
					WithErrorDetails("failed to clear resource incremental checkpoint")
			}
		}
		resource.SyncMark = ""
	}
	if err := tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit resource update transaction failed")
		otellog.LogError(ctx, "Commit resource update transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
	}
	if err := rs.ps.UpsertResourceParents(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		interfaces.AUTH_RESOURCE_TYPE_CATALOG, []interfaces.PermissionResourceParent{{
			ResourceID: resource.ID, ParentID: resource.CatalogID,
		}}); err != nil {
		span.SetStatus(codes.Error, "failed to register resource parent")
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func hasMissingVectorFeatureDimensions(schema []*interfaces.Property) bool {
	for _, property := range schema {
		if property == nil {
			continue
		}
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
func (rs *resourceService) SetEnabled(ctx context.Context, resource *interfaces.Resource, enabled bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Set resource enabled")
	defer span.End()

	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	if err := rs.checkResourcePermission(ctx, resource.ID, resource.Internal,
		interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
	}

	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}
	if err := rs.ra.UpdateEnabled(ctx, resource.ID, enabled, time.Now().UnixMilli(), accountInfo); err != nil {
		span.SetStatus(codes.Error, "Set resource enabled failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateStatus updates a Resource's status.
func (rs *resourceService) UpdateStatus(ctx context.Context, id string, status string, statusMessage string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource status")
	defer span.End()

	if err := rs.ra.UpdateStatus(ctx, nil, id, status, statusMessage); err != nil {
		span.SetStatus(codes.Error, "Update resource status failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
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
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteByIDs deletes Resources by IDs.
func (rs *resourceService) DeleteByIDs(ctx context.Context, ids []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete resources")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	// Load the requested Resources once so internal visibility and the deletion
	// lifecycle use the same immutable rows.
	resourcesByID, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	internalResources := make(map[string]struct{})
	for _, resource := range resourcesByID {
		if resource.Internal {
			internalResources[resource.ID] = struct{}{}
		}
	}
	matchResoucesMap, err := rs.filterResourcePermissions(ctx, ids, internalResources,
		[]string{interfaces.OPERATION_TYPE_DELETE}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return err
	}

	// Check if there is permission to delete
	if len(matchResoucesMap) != len(ids) {
		// The requested resource id can be repeated without deduplication. However, the resource ids filtered out have been de-duplicated. Therefore, simply judging the quantity is inaccurate
		for _, id := range ids {
			if _, exist := matchResoucesMap[id]; !exist {
				return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
					WithErrorDetails("Access denied: insufficient permissions for resource's delete operation.")
			}
		}
	}

	for _, resource := range resourcesByID {
		if err := rs.rejectResourceOperationWhenActiveDiscoverTask(ctx, resource.ID); err != nil {
			span.SetStatus(codes.Error, "Active resource refresh prevents resource deletion")
			return err
		}
		if err := rs.rejectResourceOperationWhenActiveBuildTask(ctx, resource.ID, false); err != nil {
			span.SetStatus(codes.Error, "Active build task prevents resource deletion")
			return err
		}
	}

	parentItems := make([]interfaces.PermissionResourceParent, 0, len(resourcesByID))
	for _, resource := range resourcesByID {
		parentItems = append(parentItems, interfaces.PermissionResourceParent{
			ResourceID: resource.ID, ParentID: resource.CatalogID,
		})
	}
	if len(parentItems) > 0 {
		parentIDs := make([]string, 0, len(parentItems))
		for _, item := range parentItems {
			parentIDs = append(parentIDs, item.ResourceID)
		}
		if err := rs.ps.DeleteResourceParents(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, parentIDs); err != nil {
			span.SetStatus(codes.Error, "Delete resource parents failed")
			return err
		}
	}

	if err := rs.ra.DeleteByIDs(ctx, ids); err != nil {
		if len(parentItems) > 0 {
			if restoreErr := rs.ps.UpsertResourceParents(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
				interfaces.AUTH_RESOURCE_TYPE_CATALOG, parentItems); restoreErr != nil {
				logger.Errorf("Restore resource parents after deletion failure: %v", restoreErr)
			}
		}
		span.SetStatus(codes.Error, "Delete resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	for _, resource := range resourcesByID {
		if resource.Category == interfaces.ResourceCategoryDataset {
			if err := rs.ds.Delete(ctx, resource); err != nil {
				logger.Errorf("Delete dataset failed after resource deletion: %v", err)
			}
		}
	}

	if err = rs.ps.DeleteResources(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ids); err != nil {
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExistByID checks if a resource exists by ID.
func (rs *resourceService) CheckExistByID(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check resource exist by ID")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, nil, id)
	if err != nil {
		span.SetStatus(codes.Error, "GetByID failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resource != nil, nil
}

// CheckExistByName checks if a Resource exists by name.
func (rs *resourceService) CheckExistByName(ctx context.Context, catalogID string, name string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check resource exist by name")
	defer span.End()

	resource, err := rs.ra.GetByName(ctx, catalogID, name)
	if err != nil {
		span.SetStatus(codes.Error, "GetByName failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
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

func (rs *resourceService) InternalUpdateLocalIndexState(
	ctx context.Context,
	tx *sql.Tx,
	id string,
	localIndexStatus string,
	localIndexName string,
	syncMark string,
) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateLocalIndexState")
	defer span.End()

	return rs.ra.UpdateLocalIndexState(ctx, tx, id, localIndexStatus, localIndexName, syncMark)
}

func (rs *resourceService) InternalUpdateSemanticMetadata(ctx context.Context,
	tx *sql.Tx, resource *interfaces.Resource, expectedUpdateTime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateSemanticMetadata")
	defer span.End()

	rowsAffected, err := rs.ra.UpdateSemanticMetadata(ctx, tx, resource, expectedUpdateTime)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	return nil
}

func (rs *resourceService) InternalUpdateDiscoveryMetadata(ctx context.Context, tx *sql.Tx, resource *interfaces.Resource,
	expectedUpdateTime int64) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdateDiscoveryMetadata")
	defer span.End()

	if resource == nil {
		span.SetStatus(codes.Error, "Resource is required")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	ownedTx := false
	if tx == nil {
		var err error
		tx, err = rs.db.BeginTx(ctx, nil)
		if err != nil {
			span.SetStatus(codes.Error, "Begin discovery metadata transaction failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update discovery metadata")
		}
		ownedTx = true
		defer func() { _ = tx.Rollback() }()
	}

	current, err := rs.ra.GetByID(ctx, tx, resource.ID)
	if err != nil {
		span.SetStatus(codes.Error, "Read resource for discovery metadata update failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update discovery metadata")
	}
	if current == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	previousFingerprint, err := ResourceIndexConfigFingerprint(current)
	if err != nil {
		span.SetStatus(codes.Error, "Fingerprint current discovered resource failed")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(fmt.Sprintf("invalid current resource index configuration: %v", err))
	}
	currentFingerprint, err := ResourceIndexConfigFingerprint(resource)
	if err != nil {
		span.SetStatus(codes.Error, "Fingerprint discovered resource failed")
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(fmt.Sprintf("invalid discovered resource index configuration: %v", err))
	}

	rowsAffected, err := rs.ra.UpdateDiscoveryMetadata(ctx, tx, resource, expectedUpdateTime)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	if previousFingerprint != currentFingerprint && current.LocalIndexStatus == interfaces.ResourceLocalIndexStatusAvailable {
		updated, err := rs.ra.UpdateLocalIndexState(ctx, tx, current.ID,
			interfaces.ResourceLocalIndexStatusStale, current.LocalIndexName, "")
		if err != nil || !updated {
			span.SetStatus(codes.Error, "Mark discovered resource local index stale failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
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
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update discovery metadata")
		}
	}
	return nil
}

func (rs *resourceService) InternalCreate(ctx context.Context, tx *sql.Tx, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	if tx == nil {
		return nil, fmt.Errorf("transaction is required")
	}
	internalCatalogs, err := rs.cs.InternalCatalogIDSet(ctx)
	if err != nil {
		return nil, err
	}
	_, parentInternal := internalCatalogs[req.CatalogID]
	resourceInternal := req.Internal != nil && *req.Internal
	if resourceInternal != parentInternal {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails("resource internal must match its catalog internal value")
	}

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate resource UUIDv7: %w", err)
		}
		id = generatedID.String()
	}

	var logicType string
	if req.Category == interfaces.ResourceCategoryLogicView {
		logicType, err = rs.validateLogicDefinition(ctx, req)
		if err != nil {
			return nil, err
		}
		req.SchemaDefinition, err = rs.parseLogicDefinition(ctx, req.LogicDefinition)
		if err != nil {
			return nil, err
		}
	}
	if (req.Category == interfaces.ResourceCategoryTable || req.Category == interfaces.ResourceCategoryDataset) && req.SchemaDefinition != nil {
		AddDefaultStringAndTextFeatures(req.SchemaDefinition, req.IndexConfig)
		if err := validateKeywordConfig(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
			return nil, err
		}
	}

	accountInfo, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	resource := &interfaces.Resource{
		ID:               id,
		CatalogID:        req.CatalogID,
		Name:             req.Name,
		Tags:             req.Tags,
		Description:      req.Description,
		Category:         req.Category,
		Enabled:          true,
		Internal:         resourceInternal,
		Status:           req.Status,
		Schema:           req.Schema,
		SourceIdentifier: req.SourceIdentifier,
		SourceMetadata:   req.SourceMetadata,
		SchemaDefinition: req.SchemaDefinition,
		IndexConfig:      req.IndexConfig,
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusUnavailable,
		LogicType:        logicType,
		LogicDefinition:  req.LogicDefinition,
		Creator:          accountInfo,
		CreateTime:       now,
		Updater:          accountInfo,
		UpdateTime:       now,
	}
	if err := rs.ra.Create(ctx, tx, resource); err != nil {
		return nil, err
	}
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
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 {
		if includePending && tasks[0].Status == interfaces.BuildTaskStatusPending {
			return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_Exist).
				WithErrorDetails("resource has a pending build task; wait until it finishes")
		}
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_HasRunningExecution).
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
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_DiscoverTask_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict,
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
	if resource.Category == interfaces.ResourceCategoryLogicView {
		return req.LogicDefinition != nil && !reflect.DeepEqual(resource.LogicDefinition, req.LogicDefinition), nil
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

func (rs *resourceService) validateIndexConfigModels(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if err := validateIndexConfigKeyFields(ctx, schema, indexConfig); err != nil {
		return err
	}
	defaultEmbeddingModelID := ""
	if indexConfig != nil {
		defaultEmbeddingModelID = strings.TrimSpace(indexConfig.DefaultEmbeddingModel)
	}
	models := map[string]*interfaces.SmallModel{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for i := range prop.Features {
			feature := &prop.Features[i]
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			if feature.RefProperty != "" && feature.RefProperty != prop.Name {
				if len(feature.Config) > 0 {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
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
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("embedding model is required for vector field %q; set config.embedding_model or index_config.default_embedding_model", fieldName))
			}

			model, ok := models[modelID]
			if !ok {
				var err error
				model, err = rs.mfs.GetModelByID(ctx, modelID)
				if err != nil || model == nil || model.EmbeddingDim <= 0 {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
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
	if err := ValidateVectorFeatureReferences(schema); err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
			WithErrorDetails(err.Error())
	}
	return nil
}

func (rs *resourceService) validateIndexConfigAnalyzers(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	defaultAnalyzer := ""
	if indexConfig != nil {
		defaultAnalyzer = strings.TrimSpace(indexConfig.DefaultFulltextAnalyzer)
	}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
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
					return rest.NewHTTPError(ctx, http.StatusServiceUnavailable, verrors.VegaBackend_IndexCapability_InternalError_Unavailable).
						WithErrorDetails(err.Error())
				}
				return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails(err.Error())
			}
			if !available {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_Analyzer).
					WithErrorDetails(fmt.Sprintf("analyzer %q for field %q is unavailable", analyzer, fieldName))
			}
		}
	}
	return nil
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
		if prop != nil {
			schemaFields[prop.Name] = prop
		}
	}
	primaryKeys := make(map[string]struct{}, len(indexConfig.PrimaryKeyFields))
	for _, field := range indexConfig.PrimaryKeyFields {
		prop, exists := schemaFields[field]
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields field %q is not in the resource schema", field))
		}
		if _, duplicate := primaryKeys[field]; duplicate {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields contains duplicate field %q", field))
		}
		if !interfaces.IndexConfig_IsPrimaryKeyType(prop.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).WithErrorDetails(fmt.Sprintf("primary_key_fields field %q has unsupported type %q", field, prop.Type))
		}
		primaryKeys[field] = struct{}{}
	}

	incrementalKeys := make(map[string]struct{}, len(indexConfig.IncrementalFields))
	for _, field := range indexConfig.IncrementalFields {
		prop, exists := schemaFields[field]
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields field %q is not in the resource schema", field))
		}
		if _, duplicate := incrementalKeys[field]; duplicate {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields contains duplicate field %q", field))
		}
		if !interfaces.IndexConfig_IsIncrementalFieldType(prop.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).WithErrorDetails(fmt.Sprintf("incremental_fields field %q has unsupported type %q", field, prop.Type))
		}
		incrementalKeys[field] = struct{}{}
	}
	return nil
}

func unsupportedResourceUpdateError(ctx context.Context, details string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
		WithErrorDetails(details)
}

func validateSchemaDefinition(ctx context.Context, schema []*interfaces.Property) error {
	for _, property := range schema {
		if property == nil {
			return unsupportedResourceUpdateError(ctx, "schema_definition cannot contain null fields")
		}
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
		if property != nil {
			logicalFields[property.Name] = struct{}{}
		}
	}
	for _, property := range schema {
		if property == nil || (property.Type != interfaces.DataType_String && property.Type != interfaces.DataType_Text) {
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
		if property == nil {
			continue
		}
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
		return false, unsupportedResourceUpdateError(ctx, "schema_definition can only update field display_name, description, and features")
	}
	if len(requested) < len(current) {
		return false, unsupportedResourceUpdateError(ctx, "schema_definition cannot remove or rename fields")
	}

	currentByName := make(map[string]*interfaces.Property, len(current))
	for _, prop := range current {
		if prop == nil || prop.Name == "" {
			return false, unsupportedResourceUpdateError(ctx, "current schema_definition contains an invalid field")
		}
		currentByName[prop.Name] = prop
	}

	schemaChanged := false
	seen := make(map[string]struct{}, len(requested))
	for _, requestedProp := range requested {
		if requestedProp == nil || requestedProp.Name == "" {
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
		requestedComparable.DisplayName = ""
		requestedComparable.Description = ""
		requestedComparable.Features = nil
		if !reflect.DeepEqual(currentComparable, requestedComparable) {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition can only update field display_name, description, and features")
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
	return reflect.DeepEqual(currentCopy, requestedCopy)
}

func applyMutableSchemaFields(current []*interfaces.Property, requested []*interfaces.Property, allowPropertyAdditions bool) []*interfaces.Property {
	if requested == nil {
		return current
	}
	currentByName := make(map[string]*interfaces.Property, len(current))
	for _, prop := range current {
		if prop != nil {
			currentByName[prop.Name] = prop
		}
	}
	for _, requestedProp := range requested {
		if requestedProp == nil {
			continue
		}
		if currentProp, ok := currentByName[requestedProp.Name]; ok {
			currentProp.DisplayName = requestedProp.DisplayName
			currentProp.Description = requestedProp.Description
			currentProp.Features = requestedProp.Features
			continue
		}
		if allowPropertyAdditions {
			current = append(current, requestedProp)
		}
	}
	return current
}

// ListAuthResources lists resource auth resources with filters.
func (rs *resourceService) ListAuthResources(ctx context.Context, params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListAuthResources")
	defer span.End()

	entries, total, err := rs.ra.ListAuthResources(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResources failed")
		return []*interfaces.AuthResourceEntry{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
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
