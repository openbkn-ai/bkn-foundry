// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package catalog provides Catalog management business logic.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	kwcrypto "github.com/openbkn-ai/bkn-foundry/comm-go/crypto"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/drivenadapters/entityextension"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog_health_check_schedule"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/extensions"
	"vega-backend/logics/permission"
	"vega-backend/logics/user_mgmt"
)

const (
	// EncryptedPrefix is the prefix for encrypted values.
	EncryptedPrefix = "ENC:"

	catalogAuthResourcePermissionBatchSize = 10000
	defaultConnectionTestTimeout           = 30 * time.Second
	connectorInitializationFailedResult    = "Connector initialization failed."
	connectionTestFailedResult             = "Connection test failed."
	maximumConnectionTestResultLength      = 2048
	catalogDeletedTaskMessage              = "catalog deleted"
)

var (
	cServiceOnce sync.Once
	cService     interfaces.CatalogService
)

type catalogService struct {
	appSetting *common.AppSetting
	cipher     kwcrypto.Cipher
	db         *sql.DB

	ca   interfaces.CatalogAccess
	cf   interfaces.ConnectorFactory
	ra   interfaces.ResourceAccess
	ps   interfaces.PermissionService
	ums  interfaces.UserMgmtService
	bta  interfaces.BuildTaskAccess
	dsa  interfaces.DiscoverScheduleAccess
	dta  interfaces.DiscoverTaskAccess
	hcss interfaces.CatalogHealthCheckScheduleService
	suta interfaces.SemanticUnderstandingTaskAccess
}

// NewCatalogService creates a new CatalogService.
func NewCatalogService(appSetting *common.AppSetting) interfaces.CatalogService {
	cServiceOnce.Do(func() {
		var cipher kwcrypto.Cipher
		if appSetting.CryptoSetting.Enabled {
			var err error
			cipher, err = kwcrypto.NewRSACipher(appSetting.CryptoSetting.PrivateKey, appSetting.CryptoSetting.PublicKey)
			if err != nil {
				logger.Fatalf("Failed to create RSA cipher: %v", err)
			}
		}

		cf := factory.GetFactory(appSetting)
		hcss := catalog_health_check_schedule.NewCatalogHealthCheckScheduleService(appSetting)
		ps := permission.NewPermissionService(appSetting)
		ums := user_mgmt.NewUserMgmtService(appSetting)
		cService = &catalogService{
			appSetting: appSetting,
			cipher:     cipher,
			db:         logics.DB,

			bta:  logics.BTA,
			ca:   logics.CA,
			cf:   cf,
			dsa:  logics.DSA,
			dta:  logics.DTA,
			hcss: hcss,
			ps:   ps,
			ra:   logics.RA,
			suta: logics.SUTA,
			ums:  ums,
		}
	})
	return cService
}

// catalogAuthResourceType returns the resource type of catalog in the permission service:
// The internal system directory is registered as internal_catalog. The catalog of the business role :* Generic authorization cannot match, only visible to the super administrator
func catalogAuthResourceType(internal bool) string {
	if internal {
		return interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG
	}
	return interfaces.AUTH_RESOURCE_TYPE_CATALOG
}

// partitionCatalogIDs groups directory ids according to whether they are internal system directories
func partitionCatalogIDs(ids []string, internalSet map[string]struct{}) (normalIDs, internalIDs []string) {
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

// internalCatalogIDSet queries the collection of all internal directory ids of the system
func (cs *catalogService) internalCatalogIDSet(ctx context.Context) (map[string]struct{}, error) {
	ids, err := cs.ca.ListInternalIDs(ctx)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// filterCatalogResources performs permission filtering by internal/regular directory groups: internal directories are filtered by internal_catalog
// Type verification: For regular directories, verify according to the catalog type. Merge the results and return them
func (cs *catalogService) filterCatalogResources(ctx context.Context, ids []string,
	internalSet map[string]struct{}, ops []string, allowOperation bool) (map[string]interfaces.PermissionResourceOps, error) {

	normalIDs, internalIDs := partitionCatalogIDs(ids, internalSet)

	result := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for _, group := range []struct {
		authType string
		ids      []string
	}{
		{interfaces.AUTH_RESOURCE_TYPE_CATALOG, normalIDs},
		{interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG, internalIDs},
	} {
		if len(group.ids) == 0 {
			continue
		}
		matched, err := cs.ps.FilterResources(ctx, group.authType, group.ids, ops,
			allowOperation, interfaces.COMMON_OPERATIONS)
		if err != nil {
			return nil, err
		}
		for _, resourceOps := range matched {
			result[resourceOps.ResourceID] = resourceOps
		}
	}
	return result, nil
}

// Create creates a new Catalog.
func (cs *catalogService) Create(ctx context.Context, req *interfaces.CatalogRequest, allowUnhealthy bool) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create catalog")
	defer span.End()

	if req.Internal && req.ConnectorType != "" {
		span.SetStatus(codes.Error, "Physical catalog cannot be internal")
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails("internal catalogs must be logical")
	}

	// Determine whether the userid has the permission to create a business knowledge network (policy decision);
	// The internal directory is verified by the internal_catalog type. By default, it can only be created by the super administrator/system S2S identity
	authType := catalogAuthResourceType(req.Internal)
	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: authType,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE})
	if err != nil {
		return "", err
	}

	// Get account info from context
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	catalogType := interfaces.CatalogTypePhysical
	healthStatus := interfaces.CatalogHealthStatusUnchecked
	healthResult := ""
	if req.ConnectorType == "" {
		catalogType = interfaces.CatalogTypeLogical
		if req.HealthCheckSchedule != nil {
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).
				WithErrorDetails("health check schedules are only supported for physical catalogs")
		}
	} else {
		span.SetAttributes(attr.Key("connector_type").String(req.ConnectorType))

		// Verify whether the sensitive field is a legitimate RSA ciphertext and obtain the plaintext for connection testing
		sensitiveFields := cs.cf.GetSensitiveFields(req.ConnectorType)
		decryptedConfig, err := cs.validateAndDecryptSensitiveFields(sensitiveFields, req.ConnectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to validate sensitive fields", err)
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
		}

		// Create a connector with the decrypted plaintext config and test the connection
		connectorCfg := interfaces.ConnectorConfig(decryptedConfig)
		connector, err := cs.cf.CreateConnectorInstance(ctx, req.ConnectorType, connectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to create connector", err)
			return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InternalError_CreateFailed).WithErrorDetails(connectorInitializationFailedResult)
		}

		if err := cs.testConnectorConnection(ctx, connector); err != nil {
			otellog.LogError(ctx, "Failed to test connection to data source", err)
			_ = connector.Close(ctx)
			if !allowUnhealthy {
				return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
					verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed).
					WithErrorDetails(connectionTestFailedResult)
			}
			healthStatus = interfaces.CatalogHealthStatusUnhealthy
			healthResult = connectionTestFailedResult
		} else {
			defer func() { _ = connector.Close(ctx) }()
			healthStatus = interfaces.CatalogHealthStatusHealthy
			healthResult = "Connection test succeeded."
		}
	}

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		id = xid.New().String()
	}
	catalog := &interfaces.Catalog{
		ID:            id,
		Name:          req.Name,
		Tags:          req.Tags,
		Description:   req.Description,
		Type:          catalogType,
		Enabled:       req.Enabled,
		Internal:      req.Internal,
		ConnectorType: req.ConnectorType,
		ConnectorCfg:  req.ConnectorCfg,
		CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: healthStatus,
			LastCheckTime:     now,
			HealthCheckResult: healthResult,
		},
		Creator:    accountInfo,
		CreateTime: now,
		Updater:    accountInfo,
		UpdateTime: now,
	}

	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return "", err
		}
	}

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		otellog.LogError(ctx, "Create catalog transaction failed", err)
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateFailed).
			WithErrorDetails("failed to create catalog")
	}
	defer func() { _ = tx.Rollback() }()

	err = cs.ca.Create(ctx, tx, catalog)
	if err == nil {
		err = cs.createHealthCheckSchedule(ctx, tx, catalog, req.HealthCheckSchedule)
	}
	if err == nil && req.Extensions != nil {
		err = entityextension.NewStore(cs.appSetting).Replace(
			ctx, tx, entityextension.KindCatalog, catalog.ID, *req.Extensions)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		otellog.LogError(ctx, "Create catalog transaction failed", err)
		if httpErr, ok := err.(*rest.HTTPError); ok {
			return "", httpErr
		}
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateFailed).
			WithErrorDetails("failed to create catalog")
	}

	// Register resources.
	//
	// The creator also gets resource_manage and query_data (#801): both are now
	// judged on the catalog, so without them they could not add a table to the
	// catalog they just created — and the connection config and credentials are
	// theirs to begin with. Both are in COMMON_OPERATIONS, so the whole set is
	// what the creator receives.
	err = cs.ps.CreateResources(ctx, []interfaces.PermissionResource{{
		ID:   catalog.ID,
		Type: authType,
		Name: catalog.Name,
	}}, interfaces.COMMON_OPERATIONS)
	if err != nil {
		logger.Errorf("CreateResources error: %s", err.Error())
		span.SetStatus(codes.Error, "failed to create catalog resource")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateResourcesFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return catalog.ID, nil
}

func (cs *catalogService) createHealthCheckSchedule(ctx context.Context, tx *sql.Tx, catalog *interfaces.Catalog,
	req *interfaces.CatalogHealthCheckScheduleRequest) error {
	if catalog.Type != interfaces.CatalogTypePhysical {
		return nil
	}
	_, err := cs.hcss.Create(ctx, tx, catalog, req)
	return err
}

// Get retrieves a Catalog by ID.
// FilterAuthorizedCatalogs keeps the ids the caller may perform op on. Symmetric
// with ResourceService.FilterAuthorizedResources: the ids come from a page the
// caller already fetched, so the question stays bounded by the page rather than
// by the size of the grant.
func (cs *catalogService) FilterAuthorizedCatalogs(ctx context.Context, ids []string,
	op string) (map[string]bool, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.FilterAuthorizedCatalogs")
	defer span.End()

	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return map[string]bool{}, nil
	}

	internalSet, err := cs.internalCatalogIDSet(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := cs.filterCatalogResources(ctx, unique, internalSet, []string{op}, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(allowed))
	for id := range allowed {
		out[id] = true
	}
	return out, nil
}

// CheckTaskPermission authorizes an operation on something that hangs off a
// catalog — a build, discover or semantic task — and is the only check those
// three should use.
//
// Deleting a catalog does not delete its tasks; they are marked cancelled and
// the rows stay. Judging those on a catalog that is no longer there answers 403
// forever, including for a super administrator, because the lookup fails before
// casbin is ever consulted. The tasks would then be invisible to every listing
// and deletable by nobody — dead rows that still count towards total.
//
// So a missing catalog steps up to the type-wide grant instead. Holding
// catalog:* already means seeing every catalog, and a task whose parent is gone
// discloses nothing further; without this it is simply stranded.
func (cs *catalogService) CheckTaskPermission(ctx context.Context, catalogID string, op string) error {
	if catalogID != "" {
		// InternalGetByID answers a 404 error rather than (nil, nil) for a catalog
		// that is gone, so any failure here is read as "gone" — otherwise deleting
		// a catalog would make its tasks unreachable through this path too.
		if catalog, err := cs.InternalGetByID(ctx, catalogID, false); err == nil && catalog != nil {
			return cs.CheckCatalogPermission(ctx, catalogID, op)
		}
	}
	typeWide, err := cs.HasTypeWideGrant(ctx, op)
	if err != nil {
		return err
	}
	if typeWide {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
		WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
}

// HasTypeWideGrant reports a grant written against the catalog type itself.
// Only one caller needs it: a task whose parent catalog has been deleted has no
// object left to judge, and leaving those unreachable would strand them forever.
func (cs *catalogService) HasTypeWideGrant(ctx context.Context, op string) (bool, error) {
	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{op})
	if err == nil {
		return true, nil
	}
	// A refusal answers "no", but anything else is the authorization service
	// failing to answer at all. Reading that as "no" would turn an outage into a
	// silent permission decision, so it goes back up.
	if interfaces.IsPermissionRefusal(err) {
		return false, nil
	}
	return false, err
}

// CheckCatalogPermission authorizes an operation on one catalog for callers that
// hold only its id. A missing catalog is reported as forbidden rather than as
// "not found": the caller has not proven it may see the catalog, and saying
// which ids exist is itself a disclosure.
func (cs *catalogService) CheckCatalogPermission(ctx context.Context, catalogID string, op string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.CheckCatalogPermission")
	defer span.End()

	if catalogID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("catalog_id is required")
	}
	catalog, err := cs.ca.GetByID(ctx, catalogID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", op))
	}
	if catalog.Internal && interfaces.IsS2SInternalAccess(ctx) {
		// Mirrors GetByID: internal catalogs reached over the S2S face belong to
		// the platform, not to any account.
		return nil
	}
	return cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: catalogAuthResourceType(catalog.Internal),
		ID:   catalog.ID,
	}, []string{op})
}

func (cs *catalogService) GetByID(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get catalog")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	// Filter objects with viewing permissions based on permissions. The total length of the filtered array is the total number, and there is no need to request the total number again.
	// The internal directory is validated by the internal_catalog type
	if catalog.Internal && interfaces.IsS2SInternalAccess(ctx) {
		// Internal directories are accessed via S2S within the cluster (/in/ internal network endpoints) : The internal infrastructure of the system is allowed by default.
		// Do not perform per-account view_detail verification. The same exemption package as the resource service
		// Override the secondary authentication of the internal catalog to which the internal dataset belongs when querying data. The external network endpoint will not carry this tag.
		catalog.Operations = interfaces.COMMON_OPERATIONS
	} else {
		matchResoucesMap, err := cs.ps.FilterResources(ctx, catalogAuthResourceType(catalog.Internal), []string{catalog.ID},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return nil, err
		}

		if resrc, exist := matchResoucesMap[catalog.ID]; exist {
			catalog.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
	}

	accountInfos := []*interfaces.AccountInfo{&catalog.Creator, &catalog.Updater}
	err = cs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate catalog account names: %v", err)
	}

	if !withSensitiveFields {
		// Remove sensitive fields and do not return to the front end
		cs.removeSensitiveFields(catalog)
	} else {
		// Verify whether the sensitive field is a legitimate RSA ciphertext and obtain the plaintext for connection testing
		sensitiveFields := cs.cf.GetSensitiveFields(catalog.ConnectorType)
		decryptedConfig, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to validate sensitive fields", err)
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
		}
		catalog.ConnectorCfg = decryptedConfig
	}

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

func (cs *catalogService) InternalGetByID(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.InternalGetByID")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	if !withSensitiveFields {
		cs.removeSensitiveFields(catalog)
		span.SetStatus(codes.Ok, "")
		return catalog, nil
	}

	sensitiveFields := cs.cf.GetSensitiveFields(catalog.ConnectorType)
	decryptedConfig, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Failed to validate sensitive fields", err)
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
	}
	catalog.ConnectorCfg = decryptedConfig

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

// InternalGetByIDs is used for the server to batch read directory information internally without performing permission filtering or loading extended fields.
func (cs *catalogService) InternalGetByIDs(ctx context.Context, ids []string) ([]*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.InternalGetByIDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Catalog{}, nil
	}
	catalogs, err := cs.ca.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalogs failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return catalogs, nil
}

// GetByIDs retrieves a Catalog by IDs.
func (cs *catalogService) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get catalogs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Catalog{}, nil
	}

	catalogs, err := cs.ca.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	if err := cs.ca.AttachListExtensions(ctx, interfaces.CatalogsQueryParams{IncludeExtensions: true}, catalogs); err != nil {
		span.SetStatus(codes.Error, "Load catalog extensions failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	// Remove sensitive fields and do not return to the front end
	for _, c := range catalogs {
		cs.removeSensitiveFields(c)
	}

	// Filter objects with viewing permissions based on permissions. The total length of the filtered array is the total number, and there is no need to request the total number again.
	// The internal directory is validated by the internal_catalog type
	internalSet := make(map[string]struct{})
	for _, c := range catalogs {
		if c.Internal {
			internalSet[c.ID] = struct{}{}
		}
	}
	matchResoucesMap, err := cs.filterCatalogResources(ctx, ids, internalSet,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	accountInfos := make([]*interfaces.AccountInfo, 0)
	for _, c := range catalogs {
		if resrc, exist := matchResoucesMap[c.ID]; exist {
			c.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
		accountInfos = append(accountInfos, &c.Creator, &c.Updater)
	}

	err = cs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate catalog account names: %v", err)
	}

	span.SetStatus(codes.Ok, "")
	return catalogs, nil
}

// List lists Catalogs with filters.
func (cs *catalogService) List(ctx context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.Catalog, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List catalogs")
	defer span.End()

	// Query the ids of all catalogs
	ids, err := cs.ca.ListIDs(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "List catalog IDs failed")
		return []*interfaces.Catalog{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Catalog{}, 0, nil
	}

	// Internal directory ID collection, grouped by internal_catalog type during permission verification
	internalSet, err := cs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return []*interfaces.Catalog{}, 0, err
	}

	// Filter permissions using batch processing, with 10,000 ids processed in each batch
	batchSize := 10000
	// All authorized catalogs and their operation permissions
	matchResourceOpsMap := make(map[string]interfaces.PermissionResourceOps)

	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batchIDs := ids[i:end]

		var batchMatchResources map[string]interfaces.PermissionResourceOps
		// Verify the operation permissions of the permission management
		batchMatchResources, err = cs.filterCatalogResources(ctx, batchIDs, internalSet,
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return []*interfaces.Catalog{}, 0, err
		}

		// Merge results
		for _, resourceOps := range batchMatchResources {
			matchResourceOpsMap[resourceOps.ResourceID] = resourceOps
		}
	}

	// Extract the catalog ID with permission and keep it in the same order as the ids
	authorizedIDs := make([]string, 0, len(matchResourceOpsMap))
	for _, id := range ids {
		if _, exist := matchResourceOpsMap[id]; exist {
			authorizedIDs = append(authorizedIDs, id)
		}
	}
	total := int64(len(authorizedIDs))

	// If there is no authorized catalog, return an empty result directly
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Catalog{}, total, nil
	}

	// Apply pagination based on the array of authorized ids
	if params.Limit != -1 {
		// Pagination process authorizedIDs
		// Check whether the starting position is out of bounds
		if params.Offset < 0 || params.Offset >= len(authorizedIDs) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.Catalog{}, total, nil
		}
		// Calculate the end position
		end := params.Offset + params.Limit
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		// Only query the catalog ID of the current page
		authorizedIDs = authorizedIDs[params.Offset:end]
	}

	// Query the complete catalog based on the array of authorized ids
	// Process in batches, 10,000 ids per batch, to avoid the error of prepared statement contains too many placeholders
	catalogs := make([]*interfaces.Catalog, 0, len(authorizedIDs))
	queryBatchSize := 10000
	for i := 0; i < len(authorizedIDs); i += queryBatchSize {
		end := i + queryBatchSize
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		batchIDs := authorizedIDs[i:end]

		batchCatalogs, err := cs.ca.GetByIDs(ctx, batchIDs)
		if err != nil {
			span.SetStatus(codes.Error, "Get catalogs by IDs failed")
			return []*interfaces.Catalog{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
		}

		catalogs = append(catalogs, batchCatalogs...)
	}

	if err := cs.ca.AttachListExtensions(ctx, params, catalogs); err != nil {
		span.SetStatus(codes.Error, "Attach catalog extensions failed")
		return []*interfaces.Catalog{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	// Set the operation permissions for the catalog
	for _, c := range catalogs {
		if resrc, exist := matchResourceOpsMap[c.ID]; exist {
			c.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(catalogs)*2)
	for _, c := range catalogs {
		accountInfos = append(accountInfos, &c.Creator, &c.Updater)
	}

	err = cs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate catalog account names: %v", err)
	}

	// Remove sensitive fields and do not return to the front end
	for _, c := range catalogs {
		cs.removeSensitiveFields(c)
	}

	span.SetStatus(codes.Ok, "")
	return catalogs, total, nil
}

// Update updates a Catalog.
func (cs *catalogService) Update(ctx context.Context, catalog *interfaces.Catalog, req *interfaces.CatalogRequest, allowUnhealthy bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update catalog")
	defer span.End()

	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	// Determine whether the userid has the permission to be modified; The internal directory is validated by the internal_catalog type
	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: catalogAuthResourceType(catalog.Internal),
		ID:   catalog.ID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	nameModified := req.Name != catalog.Name

	// Apply updates
	catalog.Name = req.Name
	catalog.Tags = req.Tags
	catalog.Description = req.Description

	if req.ConnectorType != "" {
		span.SetAttributes(attr.Key("connector_type").String(req.ConnectorType))

		// Note: The immutability of connector_type is underpinned by the PUT handler (catalog_handler.go).
		// Here, it will not be repeated. Just follow the req.ConnectorType to go through the decryption + trial connection + persistence process.

		// Verify whether the sensitive field is a legitimate RSA ciphertext and obtain the plaintext for connection testing
		sensitiveFields := cs.cf.GetSensitiveFields(req.ConnectorType)
		decryptedConfig, err := cs.validateAndDecryptSensitiveFields(sensitiveFields, req.ConnectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to validate sensitive fields", err)
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
		}

		// Create a connector with the decrypted plaintext config and test the connection
		connectorCfg := interfaces.ConnectorConfig(decryptedConfig)
		connector, err := cs.cf.CreateConnectorInstance(ctx, req.ConnectorType, connectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to create connector", err)
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InternalError_CreateFailed).WithErrorDetails(connectorInitializationFailedResult)
		}

		if err := cs.testConnectorConnection(ctx, connector); err != nil {
			otellog.LogError(ctx, "Failed to test connection to data source", err)
			_ = connector.Close(ctx)
			if !allowUnhealthy {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed).
					WithErrorDetails(connectionTestFailedResult)
			}
			catalog.CatalogHealthCheckStatus = interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
				LastCheckTime:     time.Now().UnixMilli(),
				HealthCheckResult: connectionTestFailedResult,
			}
		} else {
			defer func() { _ = connector.Close(ctx) }()

			catalog.CatalogHealthCheckStatus = interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
				LastCheckTime:     time.Now().UnixMilli(),
				HealthCheckResult: "Connection test succeeded.",
			}
		}

		// The req. ConnectorConfig has set up a file in the validateAndDecryptSensitiveFields plus ENC: prefix
		catalog.ConnectorCfg = req.ConnectorCfg
	}

	// Get account info
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	now := time.Now().UnixMilli()
	catalog.Updater = accountInfo
	catalog.UpdateTime = now

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Update catalog transaction failed")
		otellog.LogError(ctx, "Update catalog transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}
	defer func() { _ = tx.Rollback() }()

	rowsAffected, err := cs.ca.Update(ctx, tx, catalog, req.ExpectedUpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Update catalog failed")
		otellog.LogError(ctx, "Update catalog failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}
	if rowsAffected == 0 {
		span.SetStatus(codes.Error, "Catalog update conflict")
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_UpdateConflict)
	}

	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return err
		}
		if err := entityextension.NewStore(cs.appSetting).Replace(ctx, tx, entityextension.KindCatalog, catalog.ID, *req.Extensions); err != nil {
			span.SetStatus(codes.Error, "Replace catalog extensions failed")
			otellog.LogError(ctx, "Replace catalog extensions failed", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
				WithErrorDetails("failed to update catalog")
		}
	}
	if err := tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit catalog update transaction failed")
		otellog.LogError(ctx, "Commit catalog update transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}

	// Request the interface to update the resource name, update the resource name
	if nameModified {
		err = cs.ps.UpdateResource(ctx, interfaces.PermissionResource{
			ID:   catalog.ID,
			Type: catalogAuthResourceType(catalog.Internal),
			Name: catalog.Name,
		})
		if err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cs *catalogService) SetEnabled(ctx context.Context, catalog *interfaces.Catalog, enabled bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Set catalog enabled")
	defer span.End()

	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: catalogAuthResourceType(catalog.Internal),
		ID:   catalog.ID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	status := catalog.CatalogHealthCheckStatus
	if status.HealthCheckStatus == "" {
		status.HealthCheckStatus = interfaces.CatalogHealthStatusUnchecked
	}
	now := time.Now().UnixMilli()
	if enabled && !catalog.Enabled {
		status = interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: interfaces.CatalogHealthStatusUnchecked,
			LastCheckTime:     now,
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	if err := cs.ca.UpdateEnabled(ctx, catalog.ID, enabled, status, now, accountInfo); err != nil {
		span.SetStatus(codes.Error, "Set catalog enabled failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cs *catalogService) authorizeDelete(ctx context.Context, id string) (map[string]struct{}, error) {
	internalSet, err := cs.internalCatalogIDSet(ctx)
	if err != nil {
		return nil, err
	}
	matched, err := cs.filterCatalogResources(ctx, []string{id}, internalSet,
		[]string{interfaces.OPERATION_TYPE_DELETE}, true)
	if err != nil {
		return nil, err
	}
	if _, exists := matched[id]; !exists {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("Access denied: insufficient permissions for catalog's delete operation.")
	}
	return internalSet, nil
}

// GetDeletionImpact returns the dependency counts used by catalog deletion.
func (cs *catalogService) GetDeletionImpact(ctx context.Context, id string) (*interfaces.CatalogDeletionImpact, error) {
	if _, err := cs.authorizeDelete(ctx, id); err != nil {
		return nil, err
	}
	impact, err := cs.getDeletionImpact(ctx, id)
	if err != nil {
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) {
			return nil, httpErr
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to inspect catalog dependencies")
	}
	return impact, nil
}

// getDeletionImpact uses access ports so the catalog service does not depend on
// discover services, which already depend on catalog service.
func (cs *catalogService) getDeletionImpact(ctx context.Context, id string) (*interfaces.CatalogDeletionImpact, error) {
	page := interfaces.PaginationQueryParams{Limit: 1}
	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if catalog == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound).
			WithErrorDetails(fmt.Sprintf("id %s not found", id))
	}
	resources, err := cs.ra.GetByCatalogID(ctx, id)
	if err != nil {
		return nil, err
	}
	var pendingBuild, buildExecuting, scheduleTotal, pendingDiscover, runningDiscover int64
	healthCheckScheduleTotal := int64(0)
	if catalog.Type == interfaces.CatalogTypePhysical {
		_, pendingBuild, err = cs.bta.List(ctx, interfaces.BuildTasksQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
			Statuses:              []string{interfaces.BuildTaskStatusPending},
		})
		if err != nil {
			return nil, err
		}
		_, buildExecuting, err = cs.bta.List(ctx, interfaces.BuildTasksQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
			Statuses: []string{
				interfaces.BuildTaskStatusRunning,
				interfaces.BuildTaskStatusStopping,
			},
		})
		if err != nil {
			return nil, err
		}
		_, scheduleTotal, err = cs.dsa.List(ctx, interfaces.DiscoverScheduleQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
		})
		if err != nil {
			return nil, err
		}
		_, pendingDiscover, err = cs.dta.List(ctx, interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
			Statuses:              []string{interfaces.DiscoverTaskStatusPending},
		})
		if err != nil {
			return nil, err
		}
		_, runningDiscover, err = cs.dta.List(ctx, interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
			Statuses:              []string{interfaces.DiscoverTaskStatusRunning},
		})
		if err != nil {
			return nil, err
		}

		healthCheckSchedule, getErr := cs.hcss.GetByCatalogID(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		if healthCheckSchedule != nil {
			healthCheckScheduleTotal = 1
		}
	}
	_, pendingSemantic, err := cs.suta.List(ctx, interfaces.SemanticUnderstandingTaskQueryParams{
		PaginationQueryParams: page,
		CatalogID:             id,
		Statuses:              []string{interfaces.SemanticUnderstandingTaskStatusPending},
	})
	if err != nil {
		return nil, err
	}
	_, semanticRunning, err := cs.suta.List(ctx, interfaces.SemanticUnderstandingTaskQueryParams{
		PaginationQueryParams: page,
		CatalogID:             id,
		Statuses:              []string{interfaces.SemanticUnderstandingTaskStatusRunning},
	})
	if err != nil {
		return nil, err
	}

	protectedResources := 0
	resourceIDs := make([]string, 0, len(resources))
	for _, resource := range resources {
		resourceIDs = append(resourceIDs, resource.ID)
		if resource.Category == interfaces.ResourceCategoryDataset || resource.Category == interfaces.ResourceCategoryLogicView {
			protectedResources++
		}
	}

	blockers := make([]string, 0, 4)
	if protectedResources > 0 {
		blockers = append(blockers, interfaces.CatalogDeletionBlockerProtectedResources)
	}
	if buildExecuting > 0 {
		blockers = append(blockers, interfaces.CatalogDeletionBlockerBuildTasksRunningOrStopping)
	}
	if runningDiscover > 0 {
		blockers = append(blockers, interfaces.CatalogDeletionBlockerDiscoverTasksRunning)
	}
	if semanticRunning > 0 {
		blockers = append(blockers, interfaces.CatalogDeletionBlockerSemanticUnderstandingTasksRunning)
	}

	return &interfaces.CatalogDeletionImpact{
		CatalogID: id,
		CanDelete: len(blockers) == 0,
		Blockers:  blockers,
		BuildTasks: interfaces.CatalogDeletionTaskImpact{
			WillCancel: pendingBuild,
			Blocking:   buildExecuting,
		},
		DiscoverTasks: interfaces.CatalogDeletionTaskImpact{
			WillCancel: pendingDiscover,
			Blocking:   runningDiscover,
		},
		SemanticUnderstandingTasks: interfaces.CatalogDeletionTaskImpact{
			WillCancel: pendingSemantic,
			Blocking:   semanticRunning,
		},
		DiscoverSchedules:           scheduleTotal,
		CatalogHealthCheckSchedules: healthCheckScheduleTotal,
		Resources:                   len(resources),
		ProtectedResources:          protectedResources,
		ResourceIDs:                 resourceIDs,
	}, nil
}

// DeleteByID deletes a Catalog by ID.
func (cs *catalogService) DeleteByID(ctx context.Context, id string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete catalog")
	defer span.End()

	internalSet, err := cs.authorizeDelete(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Authorize catalog deletion failed")
		return err
	}

	impact, err := cs.getDeletionImpact(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog deletion impact failed")
		var httpErr *rest.HTTPError
		if errors.As(err, &httpErr) {
			return httpErr
		}
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to inspect catalog dependencies")
	}
	if !impact.CanDelete {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails(impact)
	}

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Delete catalog transaction failed")
		otellog.LogError(ctx, "Delete catalog transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete catalog")
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	err = cs.bta.MarkCancelledByCatalogID(ctx, tx, id, catalogDeletedTaskMessage, now)
	if err == nil {
		err = cs.dta.MarkCancelledByCatalogID(ctx, tx, id, catalogDeletedTaskMessage, now)
	}
	if err == nil {
		err = cs.suta.MarkCancelledByCatalogID(ctx, tx, id, catalogDeletedTaskMessage, now)
	}
	if err == nil {
		err = cs.dsa.DeleteByCatalogID(ctx, tx, id)
	}
	if err == nil {
		err = cs.hcss.DeleteByCatalogID(ctx, tx, id)
	}
	if err == nil {
		err = cs.ra.DeleteByCatalogID(ctx, tx, id)
	}
	if err == nil {
		err = cs.ca.DeleteByID(ctx, tx, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		span.SetStatus(codes.Error, "Delete catalog transaction failed")
		otellog.LogError(ctx, "Delete catalog transaction failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete catalog")
	}

	// The database is the source of truth. Permission cleanup is best-effort
	// after commit and must not turn a completed deletion into an API error.
	catalogPermissionType := interfaces.AUTH_RESOURCE_TYPE_CATALOG
	resourcePermissionType := interfaces.AUTH_RESOURCE_TYPE_RESOURCE
	if _, internal := internalSet[id]; internal {
		catalogPermissionType = interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG
		resourcePermissionType = interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE
	}
	if len(impact.ResourceIDs) > 0 {
		if cleanupErr := cs.ps.DeleteResources(ctx, resourcePermissionType, impact.ResourceIDs); cleanupErr != nil {
			logger.Errorf("delete catalog %s: delete resource permissions failed: %v", id, cleanupErr)
		}
	}
	if cleanupErr := cs.ps.DeleteResources(ctx, catalogPermissionType, []string{id}); cleanupErr != nil {
		logger.Errorf("delete catalog %s: delete catalog permission failed: %v", id, cleanupErr)
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// ListInternalIDs lists the ids of all internal system directories.
func (cs *catalogService) ListInternalIDs(ctx context.Context) ([]string, error) {
	ids, err := cs.ca.ListInternalIDs(ctx)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	return ids, nil
}

// CheckExistByID checks if a Catalog exists by ID.
func (cs *catalogService) CheckExistByID(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check catalog exist by ID")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "GetByID failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return catalog != nil, nil
}

// CheckExistByName checks if a Catalog exists by name.
func (cs *catalogService) CheckExistByName(ctx context.Context, name string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check catalog exist by name")
	defer span.End()

	catalog, err := cs.ca.GetByName(ctx, name)
	if err != nil {
		span.SetStatus(codes.Error, "GetByName failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return catalog != nil, nil
}

// TestConnection tests catalog connection.
func (cs *catalogService) TestConnection(ctx context.Context, catalogID string) (*interfaces.CatalogHealthCheckStatus, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Test catalog connection")
	defer span.End()

	if err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   catalogID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		span.SetStatus(codes.Error, "Check catalog modify permission failed")
		return nil, err
	}

	catalog, err := cs.ca.GetByID(ctx, catalogID)
	if err != nil {
		otellog.LogError(ctx, "Get catalog for connection test failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails("failed to get catalog for connection test")
	}
	if catalog == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	result, err := cs.testCatalogConnection(ctx, catalog)
	if err != nil {
		span.SetStatus(codes.Error, "Test catalog connection failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// InternalTestConnection tests catalog connection for internal workers.
func (cs *catalogService) InternalTestConnection(ctx context.Context, catalogID string) (*interfaces.CatalogHealthCheckStatus, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Test catalog connection internally")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	if catalog == nil {
		return nil, fmt.Errorf("catalog not found: %s", catalogID)
	}

	result, err := cs.testCatalogConnection(ctx, catalog)
	if err != nil {
		span.SetStatus(codes.Error, "Test catalog connection failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (cs *catalogService) testCatalogConnection(
	ctx context.Context, catalog *interfaces.Catalog,
) (*interfaces.CatalogHealthCheckStatus, error) {
	if catalog == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	if catalog.ConnectorType == "" {
		result := interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
			LastCheckTime:     time.Now().UnixMilli(),
			HealthCheckResult: "Logical catalogs do not support connection tests.",
		}
		return &result, nil
	}

	sensitiveFields := cs.cf.GetSensitiveFields(catalog.ConnectorType)
	config, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
	}
	result, err := cs.probeConnection(ctx, catalog.ConnectorType, interfaces.ConnectorConfig(config), sensitiveFields)
	if err != nil {
		return nil, err
	}
	if err := cs.ca.UpdateHealthCheckStatus(ctx, catalog.ID, *result); err != nil {
		otellog.LogError(ctx, "Update catalog health check status failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog health check status")
	}
	return result, nil
}

// TestConnectionConfig tests an unpersisted physical catalog configuration without creating a Catalog.
func (cs *catalogService) TestConnectionConfig(ctx context.Context,
	req *interfaces.CatalogConnectionTestRequest) (*interfaces.CatalogHealthCheckStatus, error) {
	if req == nil || req.ConnectorType == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter)
	}

	if err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE}); err != nil {
		return nil, err
	}

	sensitiveFields := cs.cf.GetSensitiveFields(req.ConnectorType)
	decryptedConfig, err := cs.validateAndDecryptSensitiveFields(sensitiveFields, req.ConnectorCfg)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
	}
	return cs.probeConnection(ctx, req.ConnectorType, interfaces.ConnectorConfig(decryptedConfig), sensitiveFields)
}

func (cs *catalogService) probeConnection(ctx context.Context, connectorType string,
	config interfaces.ConnectorConfig, sensitiveFields []string) (*interfaces.CatalogHealthCheckStatus, error) {
	connector, err := cs.cf.CreateConnectorInstance(ctx, connectorType, config)
	if err != nil {
		otellog.LogError(ctx, "Failed to create connector", err)
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InternalError_CreateFailed).WithErrorDetails(connectorInitializationFailedResult)
	}
	defer func() { _ = connector.Close(ctx) }()

	result := &interfaces.CatalogHealthCheckStatus{
		LastCheckTime: time.Now().UnixMilli(),
	}
	if err := cs.testConnectorConnection(ctx, connector); err != nil {
		otellog.LogError(ctx, "Failed to test connection to data source", err)
		result.HealthCheckStatus = interfaces.CatalogHealthStatusUnhealthy
		result.HealthCheckResult = sanitizeConnectionError(err, config, sensitiveFields)
		return result, nil
	}
	result.HealthCheckStatus = interfaces.CatalogHealthStatusHealthy
	result.HealthCheckResult = "Connection test succeeded."
	return result, nil
}

func sanitizeConnectionError(err error, config interfaces.ConnectorConfig, sensitiveFields []string) string {
	if err == nil {
		return ""
	}

	result := err.Error()
	for _, field := range sensitiveFields {
		value, ok := config[field].(string)
		if !ok || value == "" {
			continue
		}
		for _, variant := range []string{
			value,
			url.QueryEscape(value),
			strings.ReplaceAll(url.QueryEscape(value), "+", "%20"),
			url.PathEscape(value),
		} {
			if variant != "" {
				result = strings.ReplaceAll(result, variant, "******")
			}
		}
	}

	result = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(result))
	if result == "" {
		return connectionTestFailedResult
	}
	runes := []rune(result)
	if len(runes) > maximumConnectionTestResultLength {
		result = string(runes[:maximumConnectionTestResultLength-3]) + "..."
	}
	return result
}

func (cs *catalogService) testConnectorConnection(ctx context.Context, connector interfaces.Connector) error {
	timeout := defaultConnectionTestTimeout
	if cs.appSetting.CatalogHealthCheck.Timeout > 0 {
		timeout = cs.appSetting.CatalogHealthCheck.Timeout
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return connector.TestConnection(testCtx)
}

// ValidateAndDecryptSensitiveFields verify whether sensitive fields as legal RSA cipher,
// Return the decrypted plaintext config (for connection testing), and at the same time add the ENC: prefix to the original config (for storage).
// If the cipher is nil (encryption is not enabled), directly return a copy of the original config as decryptedConfig without verification.
func (cs *catalogService) validateAndDecryptSensitiveFields(sensitiveFields []string,
	config map[string]any) (decryptedConfig map[string]any, err error) {
	// Copy config as decryptedConfig
	decryptedConfig = make(map[string]any, len(config))
	for k, v := range config {
		decryptedConfig[k] = v
	}

	if cs.cipher == nil {
		return decryptedConfig, nil
	}

	for _, field := range sensitiveFields {
		val, ok := config[field].(string)
		if !ok || val == "" {
			continue
		}
		// Try to decrypt it with the private key to verify whether it is a legitimate ciphertext
		decrypted, decryptErr := cs.cipher.Decrypt(val)
		if decryptErr != nil {
			return nil, fmt.Errorf("field %s: %w", field, decryptErr)
		}
		// Decryption successful: The plaintext is placed in decryptedConfig, and the original config is prefixed with "ENC:"
		decryptedConfig[field] = decrypted
		config[field] = EncryptedPrefix + val
	}
	return decryptedConfig, nil
}

// removeSensitiveFields removes sensitive fields from ConnectorConfig for GET/List returns
func (cs *catalogService) removeSensitiveFields(catalog *interfaces.Catalog) {
	if catalog == nil || catalog.ConnectorType == "" {
		return
	}
	sensitiveFields := cs.cf.GetSensitiveFields(catalog.ConnectorType)
	for _, field := range sensitiveFields {
		delete(catalog.ConnectorCfg, field)
	}
}

// decryptSensitiveFields verifies whether the sensitive field is a legitimate RSA ciphertext
// Return the decrypted plaintext config (for connection). The data is obtained from the database and the ENC prefix needs to be removed first before decryption
// If the cipher is nil (encryption is not enabled), directly return a copy of the original config as decryptedConfig without verification.
func (cs *catalogService) decryptSensitiveFields(sensitiveFields []string,
	config map[string]any) (decryptedConfig map[string]any, err error) {

	// Copy config as decryptedConfig
	decryptedConfig = make(map[string]any, len(config))
	for k, v := range config {
		decryptedConfig[k] = v
	}

	if cs.cipher == nil {
		return decryptedConfig, nil
	}

	for _, field := range sensitiveFields {
		val, ok := config[field].(string)
		if !ok || val == "" {
			continue
		}
		// Try to decrypt it with the private key to verify whether it is a legitimate ciphertext
		if !strings.HasPrefix(val, EncryptedPrefix) {
			return nil, fmt.Errorf("field %s: %w", field, errors.New("not encrypted"))
		} else {
			val = val[len(EncryptedPrefix):]
		}
		decrypted, decryptErr := cs.cipher.Decrypt(val)
		if decryptErr != nil {
			return nil, fmt.Errorf("field %s: %w", field, decryptErr)
		}
		// Decryption successful: The plaintext is placed in decryptedConfig, and the original config is prefixed with "ENC:"
		decryptedConfig[field] = decrypted
		config[field] = EncryptedPrefix + val
	}
	return decryptedConfig, nil
}

func (cs *catalogService) UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateMetadata")
	defer span.End()

	err := cs.ca.UpdateMetadata(ctx, id, metadata)
	if err != nil {
		otellog.LogError(ctx, "Update metadata failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	return nil
}

// ListAuthResources lists catalog auth resources with filters.
func (cs *catalogService) ListAuthResources(ctx context.Context,
	params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListAuthResources")
	defer span.End()

	entries, err := cs.ca.ListAuthResources(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResources failed")
		return []*interfaces.AuthResourceEntry{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(entries) == 0 {
		return []*interfaces.AuthResourceEntry{}, 0, nil
	}

	authorizedEntries, err := cs.filterAuthorizedCatalogAuthResources(ctx, entries)
	if err != nil {
		return []*interfaces.AuthResourceEntry{}, 0, err
	}
	total := int64(len(authorizedEntries))
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.AuthResourceEntry{}, total, nil
	}

	span.SetStatus(codes.Ok, "")
	return paginateCatalogAuthResources(authorizedEntries, params.Offset, params.Limit), total, nil
}

func (cs *catalogService) filterAuthorizedCatalogAuthResources(ctx context.Context, entries []*interfaces.AuthResourceEntry) ([]*interfaces.AuthResourceEntry, error) {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		ids = append(ids, entry.ID)
	}

	authorizedIDs := make(map[string]struct{}, len(ids))
	for i := 0; i < len(ids); i += catalogAuthResourcePermissionBatchSize {
		end := i + catalogAuthResourcePermissionBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batchMatchResources, err := cs.ps.FilterResources(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG, ids[i:end],
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

func paginateCatalogAuthResources(entries []*interfaces.AuthResourceEntry, offset, limit int) []*interfaces.AuthResourceEntry {
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
