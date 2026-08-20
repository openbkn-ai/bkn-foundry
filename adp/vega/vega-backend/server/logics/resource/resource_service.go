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
	"vega-backend/drivenadapters/entityextension"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	dataset "vega-backend/logics/dataset"
	"vega-backend/logics/extensions"
	"vega-backend/logics/local_index"
	model_factory "vega-backend/logics/model_factory"
	"vega-backend/logics/permission"
	"vega-backend/logics/user_mgmt"
)

var (
	rServiceOnce sync.Once
	rService     interfaces.ResourceService
)

const resourceAuthResourcePermissionBatchSize = 10000

var activeResourceBuildTaskStatuses = []string{
	interfaces.BuildTaskStatusPending,
	interfaces.BuildTaskStatusRunning,
	interfaces.BuildTaskStatusStopping,
}

type resourceService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	cs         interfaces.CatalogService
	ds         interfaces.DatasetService
	ps         interfaces.PermissionService
	ra         interfaces.ResourceAccess
	ums        interfaces.UserMgmtService
	bta        interfaces.BuildTaskAccess
	lim        interfaces.LocalIndexManager
	mfs        interfaces.ModelFactoryService
}

// NewResourceService creates a new ResourceService.
func NewResourceService(appSetting *common.AppSetting) interfaces.ResourceService {
	rServiceOnce.Do(func() {
		rService = &resourceService{
			appSetting: appSetting,
			db:         logics.DB,
			cs:         catalog.NewCatalogService(appSetting),
			ds:         dataset.NewDatasetService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			ra:         logics.RA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			bta:        logics.BTA,
			lim:        local_index.NewLocalIndexManager(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
		}
	})
	return rService
}

// resourceAuthResourceType returns the resource type of the data resource in the permission service:
// Resources in the internal system directory are registered as internal_resource. The resource of the business role :* wildcard authorization cannot match, only visible to the super administrator
func resourceAuthResourceType(internal bool) string {
	if internal {
		return interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE
	}
	return interfaces.AUTH_RESOURCE_TYPE_RESOURCE
}

// resourceOpOnCatalog is a translation table from "operations on resources" to "operations to be asked about on the corresponding directory".
//
// List by item without copying by the same name: The "modify" semantic on a directory is "modify the directory itself", and copying by the same name is equivalent to "modifying"
// Those who "can rename directories" upgrade to "can modify every table under the directory", which is an overstepping of authority rather than convenience.
//
// Intentional absence of authorize: The person holding the authorization right of the directory should not be granted the right to delegate each table under the directory as a result
// Ability. Operations without entries stop here and do not ask upwards.
// resourceOwnOperations is what the permission service still declares on the
// resource type. The management verbs were withdrawn when they converged onto
// the catalog, so a p-line that still answers one can only be residue from
// before the convergence: the grant console no longer offers those verbs, which
// means it can neither hand them out nor take them back. Asking the resource
// about a withdrawn verb would let that residue keep deciding, invisibly and
// irrevocably — so those questions go straight to the catalog.
var resourceOwnOperations = map[string]bool{
	interfaces.OPERATION_TYPE_VIEW_DETAIL: true,
	interfaces.OPERATION_TYPE_QUERY_DATA:  true,
}

var resourceOpOnCatalog = map[string]string{
	interfaces.OPERATION_TYPE_VIEW_DETAIL: interfaces.OPERATION_TYPE_VIEW_DETAIL,
	interfaces.OPERATION_TYPE_QUERY_DATA:  interfaces.OPERATION_TYPE_QUERY_DATA,
	interfaces.OPERATION_TYPE_MODIFY:      interfaces.OPERATION_TYPE_RESOURCE_MANAGE,
	interfaces.OPERATION_TYPE_DELETE:      interfaces.OPERATION_TYPE_RESOURCE_MANAGE,
	interfaces.OPERATION_TYPE_TASK_MANAGE: interfaces.OPERATION_TYPE_TASK_MANAGE,
}

// catalogAuthResourceType returns the resource type of the data directory in the permission service, and
// resourceAuthResourceType is symmetrical.
func catalogAuthResourceType(internal bool) string {
	if internal {
		return interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG
	}
	return interfaces.AUTH_RESOURCE_TYPE_CATALOG
}

// checkResourceOrCatalog determines an operation on a resource: First, ask the resource itself; if rejected, then ask the directory to which it belongs
// (The operation should be translated according to the above table.)
//
// This is pure relaxation: the first question that can be asked today will pass, and under normal circumstances, only one authentication request will still be sent. Only the first question was rejected
// Only then will there be a second question. When both questions are rejected, the error of the first question is returned, and the error code and prompt seen by the caller remain unchanged.
//
// The attribution relationship does not require any synchronization :catalog_id is in the row of resource records that vega is judging.
func (rs *resourceService) checkResourceOrCatalog(ctx context.Context,
	resourceID, catalogID string, parentInternal bool, op string) error {

	// err stays nil when the resource is never asked, which is how the code below
	// tells "the resource refused" from "the resource was not entitled to answer".
	var err error
	if resourceOwnOperations[op] {
		err = rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: resourceAuthResourceType(parentInternal),
			ID:   resourceID,
		}, []string{op})
		if err == nil {
			return nil
		}
	}
	catalogOp, ok := resourceOpOnCatalog[op]
	if !ok || catalogID == "" {
		if err != nil {
			return err
		}
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
	}
	if err2 := rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: catalogAuthResourceType(parentInternal),
		ID:   catalogID,
	}, []string{catalogOp}); err2 != nil {
		if err != nil {
			return err // Return the error of the old caliber and keep the existing error message semantics unchanged
		}
		return err2
	}
	return nil
}

// mergeCatalogPermissions adds the part of "given by the affiliated directory" to the operations that were not approved on the resource side.
//
// Only send requests for the difference: In the normal situation where the resource side has been fully approved, no additional requests will be sent in the next time. This is also why it is supplemented
// After filtering, not before.
func (rs *resourceService) mergeCatalogPermissions(ctx context.Context, ids []string,
	ops []string, result map[string]interfaces.PermissionResourceOps) error {

	// Only those on the resource side that have not been approved at all. The criterion is "whether it is in the result" rather than "whether the operations are complete".
	// Because the caller reads the former: once it appears in the map, it is regarded as visible, and Operations are only used to render buttons.
	// Pressing the latter trigger will cause an additional round of requests when the resource side has already approved, which is a waste of money.
	pending := make([]string, 0)
	seenPending := map[string]bool{}
	for _, id := range ids {
		if _, allowed := result[id]; allowed || seenPending[id] {
			continue
		}
		seenPending[id] = true
		pending = append(pending, id)
	}
	if len(pending) == 0 {
		return nil
	}

	catalogOps := make([]string, 0, len(ops))
	seenOp := map[string]bool{}
	for _, op := range ops {
		catalogOp, ok := resourceOpOnCatalog[op]
		if !ok || seenOp[catalogOp] {
			continue
		}
		seenOp[catalogOp] = true
		catalogOps = append(catalogOps, catalogOp)
	}
	if len(catalogOps) == 0 {
		return nil // The requested operations do not ask upward (such as authorize)
	}

	resources, err := rs.ra.GetByIDsBasic(ctx, pending)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	catalogOf := make(map[string]string, len(resources))
	catalogIDs := make([]string, 0, len(resources))
	seenCatalog := map[string]bool{}
	for _, r := range resources {
		if r == nil || r.CatalogID == "" {
			continue
		}
		catalogOf[r.ID] = r.CatalogID
		if !seenCatalog[r.CatalogID] {
			seenCatalog[r.CatalogID] = true
			catalogIDs = append(catalogIDs, r.CatalogID)
		}
	}
	if len(catalogIDs) == 0 {
		return nil
	}

	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		return err
	}
	normalCatalogs, internalIDs := make([]string, 0, len(catalogIDs)), make([]string, 0)
	for _, id := range catalogIDs {
		if _, ok := internalCatalogs[id]; ok {
			internalIDs = append(internalIDs, id)
		} else {
			normalCatalogs = append(normalCatalogs, id)
		}
	}

	// The collection of operations approved for each directory. Ask once according to the table of contents, and multiple tables in the same directory on the page share one answer.
	granted := make(map[string]map[string]bool, len(catalogIDs))
	for _, group := range []struct {
		authType string
		ids      []string
	}{
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, normalCatalogs},
		{interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG, internalIDs},
	} {
		if len(group.ids) == 0 {
			continue
		}
		for _, catalogOp := range catalogOps {
			matched, err := rs.ps.FilterResources(ctx, group.authType, group.ids,
				[]string{catalogOp}, true, interfaces.COMMON_OPERATIONS)
			if err != nil {
				return err
			}
			for catalogID := range matched {
				if granted[catalogID] == nil {
					granted[catalogID] = map[string]bool{}
				}
				granted[catalogID][catalogOp] = true
			}
		}
	}

	for _, id := range pending {
		catalogID, ok := catalogOf[id]
		if !ok || len(granted[catalogID]) == 0 {
			continue
		}
		entry, exists := result[id]
		if !exists {
			entry = interfaces.PermissionResourceOps{ResourceID: id}
		}
		held := map[string]bool{}
		for _, op := range entry.Operations {
			held[op] = true
		}
		for _, op := range ops {
			catalogOp, mapped := resourceOpOnCatalog[op]
			if !mapped || held[op] || !granted[catalogID][catalogOp] {
				continue
			}
			held[op] = true
			entry.Operations = append(entry.Operations, op)
		}
		if len(entry.Operations) > 0 {
			result[id] = entry
		}
	}
	return nil
}

// internalCatalogIDSet queries the collection of all internal directory ids of the system
func (rs *resourceService) internalCatalogIDSet(ctx context.Context) (map[string]struct{}, error) {
	ids, err := rs.cs.ListInternalIDs(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// The internalResourceIDSet queries the collection of resource ids in all internal system directories
func (rs *resourceService) internalResourceIDSet(ctx context.Context) (map[string]struct{}, error) {
	catalogIDs, err := rs.cs.ListInternalIDs(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, catalogID := range catalogIDs {
		ids, err := rs.ra.ListIDs(ctx, interfaces.ResourcesQueryParams{CatalogID: catalogID})
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
		}
		for _, id := range ids {
			set[id] = struct{}{}
		}
	}
	return set, nil
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

// Permissions filterResourcePermissions grouped by internal/common resources do filtering: according to the internal directory of resources
// The internal_resource type is verified, and the rest are verified by the resource type. The results are merged and returned
func (rs *resourceService) filterResourcePermissions(ctx context.Context, ids []string,
	internalSet map[string]struct{}, ops []string, allowOperation bool) (map[string]interfaces.PermissionResourceOps, error) {

	normalIDs, internalIDs := partitionResourceIDs(ids, internalSet)

	result := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for _, group := range []struct {
		authType string
		ids      []string
	}{
		{interfaces.AUTH_RESOURCE_TYPE_RESOURCE, normalIDs},
		{interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE, internalIDs},
	} {
		if len(group.ids) == 0 {
			continue
		}
		matched, err := rs.ps.FilterResources(ctx, group.authType, group.ids, ops,
			allowOperation, interfaces.COMMON_OPERATIONS)
		if err != nil {
			return nil, err
		}
		for _, resourceOps := range matched {
			result[resourceOps.ResourceID] = resourceOps
		}
	}
	// For those that haven't been approved on the resource side, check if they belong to the directory (#817). No request will be sent when the difference is empty.
	if err := rs.mergeCatalogPermissions(ctx, ids, ops, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Create creates a new Resource.
func (rs *resourceService) Create(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create resource")
	defer span.End()

	// Resources in the internal directory are verified/registered according to the internal_resource type. By default, only the super administrator/system S2S identity can create them
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	_, parentInternal := internalCatalogs[req.CatalogID]
	authType := resourceAuthResourceType(parentInternal)

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
		Type: catalogAuthResourceType(parentInternal),
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

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		id = xid.New().String()
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

	if err := validateSchemaDefinition(ctx, req.SchemaDefinition); err != nil {
		return nil, err
	}
	if err := extensions.ValidateSchemaPropertiesExtensions(ctx, req.SchemaDefinition); err != nil {
		return nil, err
	}
	if err := rs.validateIndexConfigModels(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
		return nil, err
	}
	if err := rs.validateIndexConfigAnalyzers(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
		return nil, err
	}
	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return nil, err
		}
	}

	resource := &interfaces.Resource{
		ID:               id,
		CatalogID:        req.CatalogID,
		Name:             req.Name,
		Tags:             req.Tags,
		Description:      req.Description,
		Category:         req.Category,
		Status:           req.Status,
		Schema:           req.Schema,
		SourceIdentifier: req.SourceIdentifier,
		SourceMetadata:   req.SourceMetadata,
		SchemaDefinition: req.SchemaDefinition,
		IndexConfig:      req.IndexConfig,
		LogicType:        logicType,
		LogicDefinition:  req.LogicDefinition,
		Creator:          accountInfo,
		CreateTime:       now,
		Updater:          accountInfo,
		UpdateTime:       now,
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
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("failed to create resource")
	}

	if req.Extensions != nil {
		if err := entityextension.NewStore(rs.appSetting).Replace(ctx, tx, entityextension.KindResource, resource.ID, *req.Extensions); err != nil {
			span.SetStatus(codes.Error, "Replace resource extensions failed")
			otellog.LogError(ctx, "Replace resource extensions failed", err)
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
				WithErrorDetails("failed to create resource")
		}
	}
	if err := tx.Commit(); err != nil {
		otellog.LogError(ctx, "Commit resource creation transaction failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("failed to create resource")
	}

	switch resource.Category {
	case interfaces.ResourceCategoryDataset:
		// create dataset
		if err := rs.ds.Create(ctx, resource); err != nil {
			logger.Errorf("Create dataset failed: %v", err)
			// The failure of dataset creation does not affect resource creation; it only records errors
		}
	}

	// Register resources.
	//
	// The creator gets view_detail alone (#801). Management — modify, delete,
	// task_manage — is decided on the owning catalog, so a second object-level
	// management grant would only give the two sides different answers.
	// query_data is withheld for the same reason the split was made: handing it
	// to the creator would erase the line between "may manage" and "may see the
	// contents" exactly where it was drawn. Read access is granted explicitly,
	// on the catalog or on this table.
	err = rs.ps.CreateResources(ctx, []interfaces.PermissionResource{{
		ID:   resource.ID,
		Type: authType,
		Name: resource.Name,
	}}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		logger.Errorf("CreateResources error: %s", err.Error())
		span.SetStatus(codes.Error, "failed to create resource")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateResourcesFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// Get retrieves a Resource by ID.
func (rs *resourceService) GetByID(ctx context.Context, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resource")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}

	// Filter objects with viewing permissions based on permissions. The total length of the filtered array is the total number, and there is no need to request the total number again.
	// Resources in the internal directory are verified by the internal_resource type
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	_, parentInternal := internalCatalogs[resource.CatalogID]
	if parentInternal && interfaces.IsS2SInternalAccess(ctx) {
		// Internal directory resources are accessed via S2S within the cluster (/in/ internal network endpoints) : The internal infrastructure of the system is allowed by default.
		// Do not perform per-account view_detail verification - such resources are never authorized to business users.
		// When internal services access on behalf of users, checking per account will only result in false rejection. The external network endpoint will not carry this tag.
		resource.Operations = interfaces.COMMON_OPERATIONS
	} else {
		matchResoucesMap, err := rs.ps.FilterResources(ctx, resourceAuthResourceType(parentInternal), []string{resource.ID},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return nil, err
		}
		// The resource side has not approved it. Check the affiliated directory (#817) again.
		if err := rs.mergeCatalogPermissions(ctx, []string{resource.ID},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, matchResoucesMap); err != nil {
			span.SetStatus(codes.Error, "Merge catalog permissions error")
			return nil, err
		}

		if resrc, exist := matchResoucesMap[resource.ID]; exist {
			resource.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
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

// AuthorizedResourceIDs answers "which resources may I act on", for listings of
// things that hang off a resource rather than of resources themselves — build
// tasks above all.
//
// The type-wide grant is probed first and reported as unrestricted. That is not
// only an optimisation: most accounts hold resource:*, and resolving a concrete
// id set for them would mean listing every resource on every page request.
//
// Known narrowing gap: only the non-internal wildcard is probed, so a holder of
// resource:* with nothing on internal_resource is still reported unrestricted and
// sees tasks belonging to internal-catalog resources. List() does partition those.
// It is strictly narrower than the previous behaviour — the listing had no
// authorization at all — and closing it needs an exclusion set carried into the
// SQL rather than a third boolean.
//
// Otherwise the visible set is resolved in one batch. It is deliberately the
// whole set rather than the page's ids: the caller filters the SQL with it, so
// the count and the page agree. Filtering a page after the fact would leave
// total_count too high and pages unevenly sized.
func (rs *resourceService) AuthorizedResourceIDs(ctx context.Context, op string) ([]string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.AuthorizedResourceIDs")
	defer span.End()

	if err := rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{op}); err == nil {
		return nil, true, nil
	}

	ids, err := rs.ra.ListIDs(ctx, interfaces.ResourcesQueryParams{})
	if err != nil {
		span.SetStatus(codes.Error, "List resource ids failed")
		return nil, false, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(ids) == 0 {
		return nil, false, nil
	}
	internalResources, err := rs.internalResourceIDSet(ctx)
	if err != nil {
		return nil, false, err
	}
	allowed, err := rs.filterResourcePermissions(ctx, ids, internalResources, []string{op}, true)
	if err != nil {
		return nil, false, err
	}
	out := make([]string, 0, len(allowed))
	for _, id := range ids { // 保持 ListIDs 的顺序，便于比对
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out, false, nil
}

// CheckResourcePermission authorizes an operation on one resource for callers
// that hold only its id — the task services, whose objects all hang off a
// resource. It resolves the owning catalog itself, so a caller never has to know
// that the fallback exists.
//
// A missing resource is reported as forbidden rather than as "not found": the
// caller has not proven it may see the resource, and saying which ids exist is
// itself a disclosure.
func (rs *resourceService) CheckResourcePermission(ctx context.Context, resourceID string, op string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.CheckResourcePermission")
	defer span.End()

	if resourceID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("resource_id is required")
	}
	resource, err := rs.ra.GetByID(ctx, resourceID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resource failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if resource == nil {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
	}

	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		return err
	}
	_, parentInternal := internalCatalogs[resource.CatalogID]
	if parentInternal && interfaces.IsS2SInternalAccess(ctx) {
		// Same exemption GetByID makes: an internal-catalog resource reached over
		// the in-cluster S2S face is infrastructure, never granted to business
		// roles, so a per-account check there can only refuse a caller that is
		// acting for the platform itself. Without this the guard would break the
		// Context Loader's reads of internal datasets.
		return nil
	}
	return rs.checkResourceOrCatalog(ctx, resource.ID, resource.CatalogID, parentInternal, op)
}

func (rs *resourceService) InternalGetByID(ctx context.Context, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByID")
	defer span.End()

	return rs.ra.GetByID(ctx, id)
}

// InternalGetByIDs is used by the server to batch read the basic information of resources internally without performing permission filtering or loading extended fields.
func (rs *resourceService) InternalGetByIDs(ctx context.Context, ids []string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByIDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Resource{}, nil
	}
	resources, err := rs.ra.GetByIDsBasic(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return resources, nil
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
	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetByIDs retrieves Resources by IDs.
func (rs *resourceService) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resources by IDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Resource{}, nil
	}

	resources, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	if err := rs.ra.AttachListExtensions(ctx, interfaces.ResourcesQueryParams{IncludeExtensions: true}, resources); err != nil {
		span.SetStatus(codes.Error, "Load resource extensions failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	// Filter objects with viewing permissions based on permissions. The total length of the filtered array is the total number, and there is no need to request the total number again.
	// Resources in the internal directory are verified by the internal_resource type
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	internalResources := make(map[string]struct{})
	for _, resource := range resources {
		if _, ok := internalCatalogs[resource.CatalogID]; ok {
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

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetByCatalogID retrieves all Resources under a Catalog.
func (rs *resourceService) GetByCatalogID(ctx context.Context, catalogID string) ([]*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resources by catalog ID")
	defer span.End()

	resources, err := rs.ra.GetByCatalogID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resources, nil
}

// GetByName retrieves a Resource by catalog and name.
func (rs *resourceService) GetByName(ctx context.Context, catalogID string, name string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get resource by name")
	defer span.End()

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

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

// List lists Resources with filters.
func (rs *resourceService) List(ctx context.Context, params interfaces.ResourcesQueryParams) ([]*interfaces.Resource, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List resources")
	defer span.End()

	// Query the ids of all resources
	ids, err := rs.ra.ListIDs(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List resource IDs failed")
		return []*interfaces.Resource{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Resource{}, 0, nil
	}

	// The collection of resource ids in the internal directory is grouped by the internal_resource type during permission verification
	internalResources, err := rs.internalResourceIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal resource IDs failed")
		return []*interfaces.Resource{}, 0, err
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
		batchMatchResources, err = rs.filterResourcePermissions(ctx, batchIDs, internalResources,
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return []*interfaces.Resource{}, 0, err
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
		return []*interfaces.Resource{}, total, nil
	}

	// Query the complete resource based on the array of authorized ids and apply pagination
	// If limit = -1, all will be returned
	if params.Limit != -1 {
		// Pagination process authorizedIDs
		// Check whether the starting position is out of bounds
		if params.Offset < 0 || params.Offset >= len(authorizedIDs) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.Resource{}, total, nil
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
	resources := make([]*interfaces.Resource, 0, len(authorizedIDs))
	queryBatchSize := 10000
	for i := 0; i < len(authorizedIDs); i += queryBatchSize {
		end := i + queryBatchSize
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		batchIDs := authorizedIDs[i:end]

		batchResources, err := rs.ra.GetByIDsBasic(ctx, batchIDs)
		if err != nil {
			span.SetStatus(codes.Error, "Get resources by IDs failed")
			return []*interfaces.Resource{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
				WithErrorDetails(err.Error())
		}

		resources = append(resources, batchResources...)
	}

	if err := rs.ra.AttachListExtensions(ctx, params, resources); err != nil {
		span.SetStatus(codes.Error, "Attach resource extensions failed")
		return []*interfaces.Resource{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	// Set the operation permissions for resources
	for _, c := range resources {
		if resrc, exist := matchResourceOpsMap[c.ID]; exist {
			c.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(resources)*2)
	for _, c := range resources {
		accountInfos = append(accountInfos, &c.Creator, &c.Updater)
	}

	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate resource account names: %v", err)
	}

	span.SetStatus(codes.Ok, "")
	return resources, total, nil
}

// Update updates a Resource.
func (rs *resourceService) Update(ctx context.Context, resource *interfaces.Resource, req *interfaces.ResourceRequest) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource")
	defer span.End()

	if resource == nil {
		span.SetStatus(codes.Error, "Resource not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	// Determine whether the userid has the permission to be modified; Resources in the internal directory are verified by the internal_resource type
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return err
	}
	_, parentInternal := internalCatalogs[resource.CatalogID]
	if err = rs.checkResourceOrCatalog(ctx, resource.ID, resource.CatalogID,
		parentInternal, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
	}

	buildRelevantChanged, err := rs.validateResourceUpdateScope(ctx, resource, req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid resource update scope")
		return err
	}
	if buildRelevantChanged {
		if err := rs.rejectBuildRelevantUpdateWhenActiveBuildTask(ctx, resource); err != nil {
			span.SetStatus(codes.Error, "Resource has active build task")
			return err
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
	if err := extensions.ValidateSchemaPropertiesExtensions(ctx, resource.SchemaDefinition); err != nil {
		return err
	}
	if err := rs.validateIndexConfigModels(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
		return err
	}
	if err := rs.validateIndexConfigAnalyzers(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
		return err
	}
	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return err
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
	if buildRelevantChanged {
		resource.LocalIndexName = ""
	}

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
	if buildRelevantChanged {
		if err := rs.ra.UpdateLocalIndexName(ctx, tx, resource.ID, ""); err != nil {
			span.SetStatus(codes.Error, "Clear resource local index name failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to clear resource local index name")
		}
	}

	if req.Extensions != nil {
		if err := entityextension.NewStore(rs.appSetting).Replace(ctx, tx, entityextension.KindResource, resource.ID, *req.Extensions); err != nil {
			span.SetStatus(codes.Error, "Replace resource extensions failed")
			otellog.LogError(ctx, "Replace resource extensions failed", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails("failed to update resource")
		}
	}
	if err := tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit resource update transaction failed")
		otellog.LogError(ctx, "Commit resource update transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails("failed to update resource")
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

	// Determine whether the userid has the deletion permission; Resources in the internal directory are verified by the internal_resource type
	internalResources, err := rs.internalResourceIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal resource IDs failed")
		return err
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

	// First, obtain the information of the resource to be deleted so that different resources can be processed differently
	resources, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	for _, resource := range resources {
		switch resource.Category {
		case interfaces.ResourceCategoryTable:
			// Cascade clear all build tasks of this resource + corresponding OpenSearch index (including historical orphans).
			// Now, when resources are deleted, tasks/indexes will also be deleted (dangerous operations will be confirmed and checked by the front end for the second time).
			// Tasks that are running or stopped will be rejected by cascade (HasRunningExecution), and users need to stop them first before deleting them.
			if err := logics.CascadeDeleteBuildTasks(ctx, rs.bta, rs.lim,
				interfaces.BuildTasksQueryParams{ResourceID: resource.ID}); err != nil {
				span.SetStatus(codes.Error, "Cascade delete build tasks failed")
				return err
			}
		case interfaces.ResourceCategoryDataset:
			// Delete dataset
			if err := rs.ds.Delete(ctx, resource.ID); err != nil {
				logger.Errorf("Delete dataset failed: %v", err)
				// The failure to delete the dataset does not affect the deletion of resources; it only records errors
			}
		}
	}

	if err := rs.ra.DeleteByIDs(ctx, ids); err != nil {
		span.SetStatus(codes.Error, "Delete resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	//  Clear resource policies and delete the corresponding type of policies by internal/ordinary resource groups
	normalIDs, internalIDs := partitionResourceIDs(ids, internalResources)
	if len(normalIDs) > 0 {
		if err = rs.ps.DeleteResources(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, normalIDs); err != nil {
			return err
		}
	}
	if len(internalIDs) > 0 {
		if err = rs.ps.DeleteResources(ctx, interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE, internalIDs); err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExistByID checks if a resource exists by ID.
func (rs *resourceService) CheckExistByID(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check resource exist by ID")
	defer span.End()

	resource, err := rs.ra.GetByID(ctx, id)
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

	rowsAffected, err := rs.ra.UpdateDiscoveryMetadata(ctx, tx, resource, expectedUpdateTime)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_UpdateConflict)
	}
	return nil
}

func (rs *resourceService) InternalCreate(ctx context.Context, tx *sql.Tx, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	if tx == nil {
		return nil, fmt.Errorf("transaction is required")
	}

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		id = xid.New().String()
	}

	var logicType string
	var err error
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

	accountInfo, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	resource := &interfaces.Resource{
		ID:               id,
		CatalogID:        req.CatalogID,
		Name:             req.Name,
		Tags:             req.Tags,
		Description:      req.Description,
		Category:         req.Category,
		Status:           req.Status,
		Schema:           req.Schema,
		SourceIdentifier: req.SourceIdentifier,
		SourceMetadata:   req.SourceMetadata,
		SchemaDefinition: req.SchemaDefinition,
		IndexConfig:      req.IndexConfig,
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

func (rs *resourceService) rejectBuildRelevantUpdateWhenActiveBuildTask(ctx context.Context, resource *interfaces.Resource) error {
	tasks, err := rs.bta.InternalList(ctx, interfaces.BuildTasksQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: 1},
		ResourceID:            resource.ID,
		Statuses:              activeResourceBuildTaskStatuses,
	})
	if err != nil {
		otellog.LogError(ctx, "Check active build task failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_BuildTask_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(tasks) > 0 {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_BuildTask_Exist).
			WithErrorDetails("resource has an active build task; update build-relevant fields after it finishes")
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
	if err := validateIndexConfigBuildKeyFields(ctx, schema, indexConfig); err != nil {
		return err
	}
	defaultEmbeddingModelID := ""
	if indexConfig != nil {
		defaultEmbeddingModelID = strings.TrimSpace(indexConfig.DefaultEmbeddingModel)
	}
	checkedModelIDs := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}

			fieldName := prop.Name
			if feature.RefProperty != "" {
				fieldName = feature.RefProperty
			}

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

			if _, ok := checkedModelIDs[modelID]; !ok {
				if _, err := rs.mfs.GetModelByID(ctx, modelID); err != nil {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
						WithErrorDetails(fmt.Sprintf("embedding model ID %q for field %q not found", modelID, fieldName))
				}

				checkedModelIDs[modelID] = struct{}{}
			}
		}
	}
	return nil
}

func (rs *resourceService) validateIndexConfigAnalyzers(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if rs.lim == nil {
		return nil
	}
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

func validateIndexConfigBuildKeyFields(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if indexConfig == nil || len(indexConfig.BuildKeyFields) == 0 {
		return nil
	}

	schemaFields := make(map[string]*interfaces.Property, len(schema))
	for _, prop := range schema {
		if prop != nil {
			schemaFields[prop.Name] = prop
		}
	}
	seen := make(map[string]struct{}, len(indexConfig.BuildKeyFields))
	for _, field := range indexConfig.BuildKeyFields {
		prop, exists := schemaFields[field]
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields field %q is not in the resource schema", field))
		}
		if _, duplicate := seen[field]; duplicate {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields contains duplicate field %q", field))
		}
		if !interfaces.DataType_IsBuildKey(prop.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_BuildKeyFields).
				WithErrorDetails(fmt.Sprintf("build_key_fields field %q has unsupported type %q", field, prop.Type))
		}
		seen[field] = struct{}{}
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
		seen := make(map[string]struct{}, len(property.Features))
		for _, feature := range property.Features {
			if feature.FeatureType == "" {
				continue
			}
			if _, exists := seen[feature.FeatureType]; exists {
				return unsupportedResourceUpdateError(ctx, fmt.Sprintf("property %q has more than one %q feature", property.Name, feature.FeatureType))
			}
			seen[feature.FeatureType] = struct{}{}
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
		if !reflect.DeepEqual(currentProp.Features, requestedProp.Features) {
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

	entries, err := rs.ra.ListAuthResources(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResources failed")
		return []*interfaces.AuthResourceEntry{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}
	if len(entries) == 0 {
		return []*interfaces.AuthResourceEntry{}, 0, nil
	}

	authorizedEntries, err := rs.filterAuthorizedResourceAuthResources(ctx, entries)
	if err != nil {
		return []*interfaces.AuthResourceEntry{}, 0, err
	}
	total := int64(len(authorizedEntries))
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.AuthResourceEntry{}, total, nil
	}

	span.SetStatus(codes.Ok, "")
	return paginateResourceAuthResources(authorizedEntries, params.Offset, params.Limit), total, nil
}

func (rs *resourceService) filterAuthorizedResourceAuthResources(ctx context.Context, entries []*interfaces.AuthResourceEntry) ([]*interfaces.AuthResourceEntry, error) {
	// Resources in the internal directory of the system are authorized by type internal_resource and do not enter the list of authorized resources of type resource
	internalResources, err := rs.internalResourceIDSet(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if _, ok := internalResources[entry.ID]; ok {
			continue
		}
		ids = append(ids, entry.ID)
	}

	authorizedIDs := make(map[string]struct{}, len(ids))
	for i := 0; i < len(ids); i += resourceAuthResourcePermissionBatchSize {
		end := i + resourceAuthResourcePermissionBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batchMatchResources, err := rs.ps.FilterResources(ctx, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ids[i:end],
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, false, interfaces.COMMON_OPERATIONS)
		if err != nil {
			return nil, err
		}
		for _, resourceOps := range batchMatchResources {
			authorizedIDs[resourceOps.ResourceID] = struct{}{}
		}
	}

	results := make([]*interfaces.AuthResourceEntry, 0, len(authorizedIDs))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if _, exist := authorizedIDs[entry.ID]; exist {
			results = append(results, entry)
		}
	}

	return results, nil
}

func paginateResourceAuthResources(entries []*interfaces.AuthResourceEntry, offset, limit int) []*interfaces.AuthResourceEntry {
	if limit == -1 {
		return entries
	}
	if offset < 0 || offset >= len(entries) {
		return []*interfaces.AuthResourceEntry{}
	}

	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	return entries[offset:end]
}

// CheckExistByCategories checks if Resources exists by catalog ID and categories.
func (rs *resourceService) CheckExistByCategories(ctx context.Context, catalogID string, categories []string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckExistByCategories")
	defer span.End()

	return rs.ra.CheckExistByCategories(ctx, catalogID, categories)
}
