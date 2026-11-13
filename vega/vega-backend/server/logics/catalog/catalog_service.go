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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mohae/deepcopy"
	bkncrypto "github.com/openbkn-ai/bkn-foundry/comm-go/crypto"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog_health_check_schedule"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/user_mgmt"
)

const (
	// EncryptedPrefix is the prefix for encrypted values.
	EncryptedPrefix = "ENC:"

	catalogAuthResourcePermissionBatchSize = 10000
	defaultConnectionTestTimeout           = 30 * time.Second
	catalogPermissionCleanupTimeout        = 10 * time.Second
	catalogResourceCleanupTimeout          = 30 * time.Second
	connectorInitializationFailedResult    = "Connector initialization failed."
	connectionTestFailedResult             = "Connection test failed."
	connectionTestSucceededResult          = "Connection test succeeded."
	maximumConnectionTestResultLength      = 2048
)

func connectorInitializationError(ctx context.Context, err error) error {
	if errors.Is(err, factory.ErrConnectorEntitlementDenied) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Connector_EntitlementDenied).WithErrorDetails(err.Error())
	}
	if errors.Is(err, factory.ErrConnectorDisabled) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Connector_Disabled).WithErrorDetails(err.Error())
	}
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
		verrors.VegaBackend_Catalog_InternalError_CreateFailed).WithErrorDetails(connectorInitializationFailedResult)
}

var (
	errSensitivePlaintextPrefix = errors.New("sensitive plaintext must not start with ENC: regardless of letter case")

	cServiceOnce sync.Once
	cService     interfaces.CatalogService
)

type catalogService struct {
	appSetting *common.AppSetting
	cipher     bkncrypto.Cipher
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
		var cipher bkncrypto.Cipher
		if appSetting.CryptoSetting.Enabled || len(appSetting.CryptoSetting.AESKey) > 0 {
			var err error
			cipher, err = bkncrypto.NewAESGCMCipher(appSetting.CryptoSetting.AESKey)
			if err != nil {
				logger.Fatalf("Failed to create credential cipher: %v", err)
			}
		}

		cf := factory.NewConnectorFactory(appSetting)
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

// filterCatalogPermissionsInBatches filters catalog permissions without exceeding the permission-service request size.
func (cs *catalogService) filterCatalogPermissionsInBatches(ctx context.Context, ids []string,
	visibilityOperations []string, visibilityMatch string,
	includeOperations bool) (map[string]interfaces.PermissionResourceOps, error) {

	result := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for start := 0; start < len(ids); start += catalogAuthResourcePermissionBatchSize {
		end := start + catalogAuthResourcePermissionBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		var matched map[string]interfaces.PermissionResourceOps
		var err error
		if includeOperations {
			matched, err = cs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG,
				ids[start:end], visibilityOperations, visibilityMatch)
		} else {
			matched, err = cs.ps.FilterVisibleResources(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG,
				ids[start:end], visibilityOperations, visibilityMatch)
		}
		if err != nil {
			return nil, err
		}
		for id, resourceOps := range matched {
			result[id] = resourceOps
		}
	}
	return result, nil
}

// Create creates a new Catalog.
func (cs *catalogService) Create(ctx context.Context, req *interfaces.CatalogRequest, allowUnhealthy bool) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create catalog")
	defer span.End()

	if req.Builtin && req.ConnectorType != "" {
		span.SetStatus(codes.Error, "Physical catalog cannot be built-in")
		return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails("built-in catalogs must be logical")
	}
	if req.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return "", httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
	}

	// bkn-safe decides the type-wide catalog create permission after the local
	// built-in catalog guard above.
	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
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

	storedConfig := req.ConnectorCfg
	catalogType := interfaces.CatalogTypePhysical
	category := ""
	healthStatus := interfaces.CatalogHealthStatusUnchecked
	healthResult := ""
	if req.ConnectorType == "" {
		catalogType = interfaces.CatalogTypeLogical
		if req.HealthCheckSchedule != nil {
			return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				verrors.VegaBackend_Catalog_InvalidParameter).
				WithErrorDetails("health check schedules are only supported for physical catalogs")
		}
	} else {
		span.SetAttributes(attr.Key("connector_type").String(req.ConnectorType))

		// Use an independent plaintext copy for configuration and connection validation.
		sensitiveFields, err := cs.cf.GetSensitiveFields(req.ConnectorType, req.ConnectorCfg)
		if err != nil {
			return "", connectorInitializationError(ctx, err)
		}
		connectorCfg := deepcopy.Copy(req.ConnectorCfg).(interfaces.ConnectorConfig)
		connector, err := cs.cf.CreateConnectorInstance(ctx, req.ConnectorType, connectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to create connector", err)
			return "", connectorInitializationError(ctx, err)
		}
		defer func() { _ = connector.Close(ctx) }()

		category = connector.GetCategory()

		if err := cs.testConnectorConnection(ctx, connector); err != nil {
			otellog.LogError(ctx, "Failed to test connection to data source", err)
			if !allowUnhealthy {
				return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed).
					WithErrorDetails(connectionTestFailedResult)
			}
			healthStatus = interfaces.CatalogHealthStatusUnhealthy
			healthResult = connectionTestFailedResult
		} else {
			healthStatus = interfaces.CatalogHealthStatusHealthy
			healthResult = connectionTestSucceededResult
		}
		encryptedConfig, err := cs.encryptSensitiveFields(sensitiveFields, req.ConnectorCfg)
		if err != nil {
			if errors.Is(err, errSensitivePlaintextPrefix) {
				return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(err.Error())
			}
			otellog.LogError(ctx, "Failed to encrypt catalog credentials", err)
			return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_CreateFailed).WithErrorDetails(err.Error())
		}
		storedConfig = encryptedConfig
	}

	// Perform uniqueness checks only after authorization so unauthorised callers
	// cannot probe catalog names or IDs through create conflicts.
	exists, err := cs.checkExistByName(ctx, req.Name)
	if err != nil {
		return "", err
	}
	if exists {
		return "", httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_NameExists)
	}
	if req.ID != "" {
		exists, err = cs.CheckExistByID(ctx, req.ID)
		if err != nil {
			return "", err
		}
		if exists {
			return "", httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IDExists).
				WithErrorDetails(fmt.Sprintf("id %s already exists", req.ID))
		}
	}

	now := time.Now().UnixMilli()
	id := req.ID
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return "", fmt.Errorf("generate catalog UUIDv7: %w", err)
		}
		id = generatedID.String()
	}
	catalog := &interfaces.Catalog{
		ID:            id,
		Name:          req.Name,
		Tags:          req.Tags,
		Description:   req.Description,
		Type:          catalogType,
		Enabled:       req.Enabled,
		Builtin:       req.Builtin,
		ConnectorType: req.ConnectorType,
		Category:      category,
		ConnectorCfg:  storedConfig,
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

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		otellog.LogError(ctx, "Create catalog transaction failed", err)
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateFailed).
			WithErrorDetails("failed to create catalog")
	}
	defer func() { _ = tx.Rollback() }()

	err = cs.ca.Create(ctx, tx, catalog)
	if err == nil {
		err = cs.createHealthCheckSchedule(ctx, tx, catalog, req.HealthCheckSchedule)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		otellog.LogError(ctx, "Create catalog transaction failed", err)
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			return "", httpErr
		}
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_CreateFailed).
			WithErrorDetails("failed to create catalog")
	}

	// Register resources.
	//
	// A catalog creator receives bkn-safe's canonical instance-scoped
	// root bundle. resource_manage and query_data are judged on this catalog, so
	// omitting them would prevent the creator from managing its own tables and
	// data. Create itself stays a type-wide capability and is intentionally
	// absent.
	err = cs.ps.CreateResources(ctx, []interfaces.PermissionResource{{
		ID:   catalog.ID,
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		Name: catalog.Name,
	}}, interfaces.CATALOG_CREATOR_OPERATIONS)
	if err != nil {
		logger.Errorf("CreateResources error: %s", err.Error())
		span.SetStatus(codes.Error, "failed to create catalog resource")
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
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

// ListPermittedCatalogIDs returns IDs matching visibilityMatch across the
// requested operations, preserving the catalog query's order.
func (cs *catalogService) ListPermittedCatalogIDs(ctx context.Context, visibilityOperations []string,
	visibilityMatch string, params interfaces.CatalogsQueryParams) ([]string, error) {

	ids, _, err := cs.listPermittedCatalogIDs(ctx, visibilityOperations, visibilityMatch, false, params)
	return ids, err
}

func (cs *catalogService) ListPermittedCatalogIDsWithOperations(ctx context.Context, visibilityOperations []string,
	visibilityMatch string, params interfaces.CatalogsQueryParams) ([]string, map[string]interfaces.PermissionResourceOps, error) {

	return cs.listPermittedCatalogIDs(ctx, visibilityOperations, visibilityMatch, true, params)
}

func (cs *catalogService) listPermittedCatalogIDs(ctx context.Context, visibilityOperations []string,
	visibilityMatch string, includeOperations bool,
	params interfaces.CatalogsQueryParams) ([]string, map[string]interfaces.PermissionResourceOps, error) {

	params.IncludeBuiltin = interfaces.IsBuiltinAdmin(ctx)
	refs, err := cs.ca.ListPermissionRefs(ctx, params)
	if err != nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(refs) == 0 {
		return nil, map[string]interfaces.PermissionResourceOps{}, nil
	}
	all := make([]string, 0, len(refs))
	for _, ref := range refs {
		all = append(all, ref.CatalogID)
	}
	allowed, err := cs.filterCatalogPermissionsInBatches(ctx, all, visibilityOperations, visibilityMatch, includeOperations)
	if err != nil {
		return nil, nil, err
	}
	out := make([]string, 0, len(allowed))
	for _, id := range all {
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out, allowed, nil
}

// CheckCatalogPermission checks bkn-safe permission for one catalog ID. When
// getCatalog is true, it also checks existence and built-in visibility and
// returns the non-sensitive catalog.
func (cs *catalogService) CheckCatalogPermission(ctx context.Context, catalogID string,
	ops []string, getCatalog bool) (bool, *interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.CheckCatalogPermission")
	defer span.End()

	if catalogID == "" {
		return false, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_ID).
			WithErrorDetails("catalog_id is required")
	}
	err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   catalogID,
	}, ops)
	if err == nil {
		if !getCatalog {
			return true, nil, nil
		}
		catalog, catalogErr := cs.InternalGetByID(ctx, catalogID, false)
		if catalogErr != nil {
			return false, nil, catalogErr
		}
		if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
			return false, nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
		}
		return true, catalog, nil
	}
	if interfaces.IsPermissionRefusal(err) {
		return false, nil, nil
	}
	return false, nil, err
}

func (cs *catalogService) GetByID(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get catalog")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	// Apply the fixed internal guard before bkn-safe's normal catalog check.
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
	}

	matchResoucesMap, err := cs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		[]string{catalog.ID}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	if resrc, exist := matchResoucesMap[catalog.ID]; exist {
		catalog.Operations = resrc.Operations // The operations that the user is currently permitted to perform
	} else {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
	}

	accountInfos := []*interfaces.AccountInfo{&catalog.Creator, &catalog.Updater}
	err = cs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.RecordError(err)
		logger.Warnf("Failed to populate catalog account names: %v", err)
	}

	if !withSensitiveFields {
		// Remove sensitive fields and do not return to the front end
		if err := cs.removeSensitiveFields(catalog); err != nil {
			return nil, connectorInitializationError(ctx, err)
		}
	} else if catalog.ConnectorType != "" {
		// Decrypt stored sensitive fields in an independent configuration copy.
		sensitiveFields, err := cs.cf.GetSensitiveFields(catalog.ConnectorType, catalog.ConnectorCfg)
		if err != nil {
			return nil, connectorInitializationError(ctx, err)
		}
		decryptedConfig, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to validate sensitive fields", err)
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	if !withSensitiveFields || catalog.ConnectorType == "" {
		if err := cs.removeSensitiveFields(catalog); err != nil {
			return nil, connectorInitializationError(ctx, err)
		}
		span.SetStatus(codes.Ok, "")
		return catalog, nil
	}

	sensitiveFields, metadataErr := cs.cf.GetSensitiveFields(catalog.ConnectorType, catalog.ConnectorCfg)
	if metadataErr != nil {
		return nil, connectorInitializationError(ctx, metadataErr)
	}
	decryptedConfig, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Failed to validate sensitive fields", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
	}
	catalog.ConnectorCfg = decryptedConfig

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

// InternalGetByIDs is used for the server to batch read directory information internally without performing permission filtering or loading extended fields.
func (cs *catalogService) InternalGetByIDs(ctx context.Context, ids []string) (map[string]*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CatalogService.InternalGetByIDs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return map[string]*interfaces.Catalog{}, nil
	}
	catalogsByID, err := cs.ca.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalogs failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return catalogsByID, nil
}

// GetByIDs retrieves a Catalog by IDs.
func (cs *catalogService) GetByIDs(ctx context.Context, ids []string) ([]*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get catalogs")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Catalog{}, nil
	}

	catalogsByID, err := cs.ca.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	catalogs := make([]*interfaces.Catalog, 0, len(catalogsByID))
	for _, id := range ids {
		if catalog, exists := catalogsByID[id]; exists {
			catalogs = append(catalogs, catalog)
		}
	}

	// Remove sensitive fields and do not return to the front end
	for _, c := range catalogs {
		if err := cs.removeSensitiveFields(c); err != nil {
			return nil, connectorInitializationError(ctx, err)
		}
	}

	// Internal catalog details are restricted before bkn-safe authorization.
	if !interfaces.IsBuiltinAdmin(ctx) {
		for _, catalog := range catalogs {
			if catalog.Builtin {
				return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
					WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
			}
		}
	}
	matchResoucesMap, err := cs.ps.FilterVisibleResourcesWithOperations(ctx, interfaces.AUTH_RESOURCE_TYPE_CATALOG, ids,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return nil, err
	}

	accountInfos := make([]*interfaces.AccountInfo, 0)
	for _, c := range catalogs {
		if resrc, exist := matchResoucesMap[c.ID]; exist {
			c.Operations = resrc.Operations // The operations that the user is currently permitted to perform
		} else {
			return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
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

// List lists catalog summaries with filters.
func (cs *catalogService) List(ctx context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogSummary, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List catalogs")
	defer span.End()

	ids, matchResourceOpsMap, err := cs.ListPermittedCatalogIDsWithOperations(ctx,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY},
		interfaces.VISIBILITY_MATCH_ANY, params)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return []*interfaces.CatalogSummary{}, 0, err
	}
	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.CatalogSummary{}, 0, nil
	}

	// Extract the catalog ID with permission and keep it in the same order as the ids
	authorizedIDs := make([]string, 0, len(matchResourceOpsMap))
	authorizedIDs = append(authorizedIDs, ids...)
	total := int64(len(authorizedIDs))

	// If there is no authorized catalog, return an empty result directly
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.CatalogSummary{}, total, nil
	}

	// Apply pagination based on the array of authorized ids
	if params.Limit != -1 {
		// Pagination process authorizedIDs
		// Check whether the starting position is out of bounds
		if params.Offset < 0 || params.Offset >= len(authorizedIDs) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.CatalogSummary{}, total, nil
		}
		// Calculate the end position
		end := params.Offset + params.Limit
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		// Only query the catalog ID of the current page
		authorizedIDs = authorizedIDs[params.Offset:end]
	}

	// Query catalog summaries based on the array of authorized ids.
	// Process in batches, 10,000 ids per batch, to avoid the error of prepared statement contains too many placeholders
	catalogs := make([]*interfaces.CatalogSummary, 0, len(authorizedIDs))
	queryBatchSize := 10000
	for i := 0; i < len(authorizedIDs); i += queryBatchSize {
		end := i + queryBatchSize
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		batchIDs := authorizedIDs[i:end]

		catalogsByID, err := cs.ca.GetSummariesByIDs(ctx, batchIDs)
		if err != nil {
			span.SetStatus(codes.Error, "Get catalogs by IDs failed")
			return []*interfaces.CatalogSummary{}, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
		}

		for _, id := range batchIDs {
			if catalog, exists := catalogsByID[id]; exists {
				catalogs = append(catalogs, catalog)
			}
		}
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

	span.SetStatus(codes.Ok, "")
	return catalogs, total, nil
}

// ListConnectorTypeStats returns one count per catalog and connector type after applying catalog view permissions.
func (cs *catalogService) ListConnectorTypeStats(ctx context.Context, params interfaces.CatalogsQueryParams) ([]*interfaces.CatalogConnectorTypeStat, error) {
	params.IncludeBuiltin = interfaces.IsBuiltinAdmin(ctx)
	refs, err := cs.ca.ListConnectorTypePermissionRefs(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return []*interfaces.CatalogConnectorTypeStat{}, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.CatalogID)
	}
	allowed, err := cs.filterCatalogPermissionsInBatches(ctx, ids,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY},
		interfaces.VISIBILITY_MATCH_ANY, false)
	if err != nil {
		return nil, err
	}

	type statKey struct {
		catalogType   string
		connectorType string
	}
	counts := make(map[statKey]int64)
	for _, ref := range refs {
		if _, ok := allowed[ref.CatalogID]; ok {
			counts[statKey{catalogType: ref.CatalogType, connectorType: ref.ConnectorType}]++
		}
	}
	stats := make([]*interfaces.CatalogConnectorTypeStat, 0, len(counts))
	for key, catalogCount := range counts {
		stats = append(stats, &interfaces.CatalogConnectorTypeStat{
			CatalogType:   key.catalogType,
			ConnectorType: key.connectorType,
			CatalogCount:  catalogCount,
		})
	}
	sort.Slice(stats, func(left, right int) bool {
		if stats[left].CatalogType != stats[right].CatalogType {
			return stats[left].CatalogType < stats[right].CatalogType
		}
		return stats[left].ConnectorType < stats[right].ConnectorType
	})
	return stats, nil
}

// Update updates a Catalog.
func (cs *catalogService) Update(ctx context.Context, req *interfaces.CatalogRequest, allowUnhealthy bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update catalog")
	defer span.End()

	if err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   req.ID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return err
	}

	catalog, err := cs.ca.GetByID(ctx, req.ID)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
	}

	if req.ConnectorType != catalog.ConnectorType {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter_ConnectorType).
			WithErrorDetails("connector_type cannot be modified")
	}
	if req.Enabled != catalog.Enabled {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_EnabledFieldNotAllowed).
			WithErrorDetails("use POST /catalogs/{id}/enable or /disable to change enabled state")
	}

	nameModified := req.Name != catalog.Name
	if nameModified {
		exists, err := cs.checkExistByName(ctx, req.Name)
		if err != nil {
			return err
		}
		if exists {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_NameExists)
		}
	}

	// Apply updates
	catalog.Name = req.Name
	catalog.Tags = req.Tags
	catalog.Description = req.Description

	// 未提交配置时保留原配置；API 类别仍须本地校验实例可用性和已存配置。
	updateConnection := req.ConnectorType != "" && req.ConnectorCfg != nil
	if updateConnection || catalog.Category == interfaces.ConnectorCategoryAPI {
		span.SetAttributes(attr.Key("connector_type").String(req.ConnectorType))

		// 显式配置完整替换并测试连接；省略 API 配置时只校验实例，保留健康结果。

		config := req.ConnectorCfg
		if !updateConnection {
			config = catalog.ConnectorCfg
		}
		sensitiveFields, err := cs.cf.GetSensitiveFields(req.ConnectorType, config)
		if err != nil {
			return connectorInitializationError(ctx, err)
		}
		var connectorCfg interfaces.ConnectorConfig
		if updateConnection {
			connectorCfg = deepcopy.Copy(config).(interfaces.ConnectorConfig)
		} else {
			decryptedConfig, err := cs.decryptSensitiveFields(sensitiveFields, config)
			if err != nil {
				otellog.LogError(ctx, "Failed to decrypt catalog credentials", err)
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
			}
			connectorCfg = decryptedConfig
		}
		connector, err := cs.cf.CreateConnectorInstance(ctx, req.ConnectorType, connectorCfg)
		if err != nil {
			otellog.LogError(ctx, "Failed to create connector", err)
			return connectorInitializationError(ctx, err)
		}
		defer func() { _ = connector.Close(ctx) }()

		if updateConnection {
			if err := cs.testConnectorConnection(ctx, connector); err != nil {
				otellog.LogError(ctx, "Failed to test connection to data source", err)
				if !allowUnhealthy {
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
						verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed).
						WithErrorDetails(connectionTestFailedResult)
				}
				catalog.CatalogHealthCheckStatus = interfaces.CatalogHealthCheckStatus{
					HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
					LastCheckTime:     time.Now().UnixMilli(),
					HealthCheckResult: connectionTestFailedResult,
				}
			} else {
				catalog.CatalogHealthCheckStatus = interfaces.CatalogHealthCheckStatus{
					HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
					LastCheckTime:     time.Now().UnixMilli(),
					HealthCheckResult: connectionTestSucceededResult,
				}
			}
		}

		if updateConnection {
			encryptedConfig, err := cs.encryptSensitiveFields(sensitiveFields, req.ConnectorCfg)
			if err != nil {
				if errors.Is(err, errSensitivePlaintextPrefix) {
					return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
						verrors.VegaBackend_Catalog_InvalidParameter).WithErrorDetails(err.Error())
				}
				otellog.LogError(ctx, "Failed to encrypt catalog credentials", err)
				return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
					verrors.VegaBackend_Catalog_InternalError_UpdateFailed).WithErrorDetails(err.Error())
			}
			catalog.ConnectorCfg = encryptedConfig
		}
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
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}
	defer func() { _ = tx.Rollback() }()

	// 元数据更新不能写回读取时的连接快照，避免覆盖并发健康检查的新结果。
	rowsAffected, err := cs.ca.Update(ctx, tx, catalog, req.ExpectedUpdateTime, updateConnection)
	if err != nil {
		span.SetStatus(codes.Error, "Update catalog failed")
		otellog.LogError(ctx, "Update catalog failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}
	if rowsAffected == 0 {
		span.SetStatus(codes.Error, "Catalog update conflict")
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_UpdateConflict)
	}

	if err := tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit catalog update transaction failed")
		otellog.LogError(ctx, "Commit catalog update transaction failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog")
	}

	// Request the interface to update the resource name, update the resource name
	if nameModified {
		err = cs.ps.UpdateResource(ctx, interfaces.PermissionResource{
			ID:   catalog.ID,
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			Name: catalog.Name,
		})
		if err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cs *catalogService) SetEnabled(ctx context.Context, id string, enabled bool) (*interfaces.Catalog, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Set catalog enabled")
	defer span.End()

	if err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   id,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return nil, err
	}

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog failed")
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if catalog == nil {
		span.SetStatus(codes.Error, "Catalog not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
	}
	if catalog.Enabled == enabled {
		span.SetStatus(codes.Ok, "")
		return catalog, nil
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return catalog, nil
}

// GetDeletionImpact returns the dependency counts used by catalog deletion.
func (cs *catalogService) GetDeletionImpact(ctx context.Context, id string) (*interfaces.CatalogDeletionImpact, error) {
	allowed, catalog, err := cs.CheckCatalogPermission(ctx, id,
		[]string{interfaces.OPERATION_TYPE_DELETE}, true)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.ErrorCode == verrors.VegaBackend_Catalog_InternalError_GetFailed {
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
				WithErrorDetails("failed to inspect catalog dependencies")
		}
		return nil, err
	}
	if !allowed {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}

	impact, err := cs.getDeletionImpact(ctx, catalog)
	if err != nil {
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			return nil, httpErr
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to inspect catalog dependencies")
	}
	return impact, nil
}

// getDeletionImpact uses access ports so the catalog service does not depend on
// discover services, which already depend on catalog service.
func (cs *catalogService) getDeletionImpact(ctx context.Context, catalog *interfaces.Catalog) (*interfaces.CatalogDeletionImpact, error) {
	page := interfaces.PaginationQueryParams{Limit: 1}
	id := catalog.ID
	resources, err := cs.ra.GetByCatalogID(ctx, id)
	if err != nil {
		return nil, err
	}
	var buildTotal, buildExecuting, scheduleTotal, discoverTotal, runningDiscover int64
	healthCheckScheduleTotal := int64(0)
	if catalog.Type == interfaces.CatalogTypePhysical {
		_, buildTotal, err = cs.bta.List(ctx, interfaces.BuildTasksQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
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
		_, discoverTotal, err = cs.dta.List(ctx, interfaces.DiscoverTaskQueryParams{
			PaginationQueryParams: page,
			CatalogID:             id,
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
	_, semanticTotal, err := cs.suta.List(ctx, interfaces.SemanticUnderstandingTaskQueryParams{
		PaginationQueryParams: page,
		CatalogID:             id,
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
		if resource.Category == interfaces.ResourceCategoryDataset || resource.Category == interfaces.ResourceCategoryLogicalView {
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
			WillDelete: buildTotal,
			Blocking:   buildExecuting,
		},
		DiscoverTasks: interfaces.CatalogDeletionTaskImpact{
			WillDelete: discoverTotal,
			Blocking:   runningDiscover,
		},
		SemanticUnderstandingTasks: interfaces.CatalogDeletionTaskImpact{
			WillDelete: semanticTotal,
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

	allowed, catalog, err := cs.CheckCatalogPermission(ctx, id,
		[]string{interfaces.OPERATION_TYPE_DELETE}, true)
	if err != nil {
		span.SetStatus(codes.Error, "Authorize catalog deletion failed")
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.ErrorCode == verrors.VegaBackend_Catalog_InternalError_GetFailed {
			return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
				WithErrorDetails("failed to inspect catalog dependencies")
		}
		return err
	}
	if !allowed {
		span.SetStatus(codes.Error, "Authorize catalog deletion failed")
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
	}

	impact, err := cs.getDeletionImpact(ctx, catalog)
	if err != nil {
		span.SetStatus(codes.Error, "Get catalog deletion impact failed")
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			return httpErr
		}
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to inspect catalog dependencies")
	}
	if !impact.CanDelete {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_InvalidParameter).
			WithErrorDetails(impact)
	}

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Delete catalog transaction failed")
		otellog.LogError(ctx, "Delete catalog transaction failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete catalog")
	}
	defer func() { _ = tx.Rollback() }()

	err = cs.bta.DeleteByCatalogID(ctx, tx, id)
	if err == nil {
		err = cs.dta.DeleteByCatalogID(ctx, tx, id)
	}
	if err == nil {
		err = cs.suta.DeleteByCatalogID(ctx, tx, id)
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
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_DeleteFailed).
			WithErrorDetails("failed to delete catalog")
	}

	// The database is the source of truth. Permission cleanup is best-effort
	// after commit and must not turn a completed deletion into an API error.
	if len(impact.ResourceIDs) > 0 {
		parentCleanupCtx, cancelParentCleanup := context.WithTimeout(
			context.WithoutCancel(ctx), catalogResourceCleanupTimeout)
		if cleanupErr := cs.ps.DeleteResourceParents(parentCleanupCtx,
			interfaces.AUTH_RESOURCE_TYPE_RESOURCE, impact.ResourceIDs); cleanupErr != nil {
			logger.Errorf("delete catalog %s: delete resource parent relations failed: %v", id, cleanupErr)
		}
		cancelParentCleanup()

		resourceCleanupCtx, cancelResourceCleanup := context.WithTimeout(
			context.WithoutCancel(ctx), catalogResourceCleanupTimeout)
		if cleanupErr := cs.ps.DeleteResources(resourceCleanupCtx,
			interfaces.AUTH_RESOURCE_TYPE_RESOURCE, impact.ResourceIDs); cleanupErr != nil {
			logger.Errorf("delete catalog %s: delete resource permissions failed: %v", id, cleanupErr)
		}
		cancelResourceCleanup()
	}
	catalogCleanupCtx, cancelCatalogCleanup := context.WithTimeout(
		context.WithoutCancel(ctx), catalogPermissionCleanupTimeout)
	if cleanupErr := cs.ps.DeleteResources(catalogCleanupCtx,
		interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{id}); cleanupErr != nil {
		logger.Errorf("delete catalog %s: delete catalog permission failed: %v", id, cleanupErr)
	}
	cancelCatalogCleanup()

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExistByID checks if a Catalog exists by ID.
func (cs *catalogService) CheckExistByID(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check catalog exist by ID")
	defer span.End()

	catalog, err := cs.ca.GetByID(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "GetByID failed")
		return false, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return catalog != nil, nil
}

// checkExistByName checks if a Catalog exists by name.
func (cs *catalogService) checkExistByName(ctx context.Context, name string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check catalog exist by name")
	defer span.End()

	catalog, err := cs.ca.GetByName(ctx, name)
	if err != nil {
		span.SetStatus(codes.Error, "GetByName failed")
		return false, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).
			WithErrorDetails("failed to get catalog for connection test")
	}
	if catalog == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
			WithErrorDetails("built-in catalogs are restricted to the built-in administrator")
	}

	result, err := cs.testCatalogConnection(ctx, catalog)
	if err != nil {
		span.SetStatus(codes.Error, "Test catalog connection failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// InternalTestConnection checks source health for workers; API probes only TCP/TLS reachability.
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Catalog_NotFound)
	}

	if catalog.ConnectorType == "" {
		result := interfaces.CatalogHealthCheckStatus{
			HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
			LastCheckTime:     time.Now().UnixMilli(),
			HealthCheckResult: "Logical catalogs do not support connection tests.",
		}
		return &result, nil
	}

	sensitiveFields, err := cs.cf.GetSensitiveFields(catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, connectorInitializationError(ctx, err)
	}
	config, err := cs.decryptSensitiveFields(sensitiveFields, catalog.ConnectorCfg)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			verrors.VegaBackend_Catalog_InvalidParameter_SensitiveFieldNotEncrypted).WithErrorDetails(err.Error())
	}
	result, err := cs.probeConnection(ctx, catalog.ConnectorType, config)
	if err != nil {
		return nil, err
	}
	if err := cs.ca.UpdateHealthCheckStatus(ctx, catalog.ID, *result); err != nil {
		otellog.LogError(ctx, "Update catalog health check status failed", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).
			WithErrorDetails("failed to update catalog health check status")
	}
	return result, nil
}

// TestConnectionConfig tests an unpersisted physical catalog configuration without creating a Catalog.
func (cs *catalogService) TestConnectionConfig(ctx context.Context,
	req *interfaces.CatalogConnectionTestRequest) (*interfaces.CatalogHealthCheckStatus, error) {
	if req == nil || req.ConnectorType == "" {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Catalog_InvalidParameter)
	}

	if err := cs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE}); err != nil {
		return nil, err
	}

	config := deepcopy.Copy(req.ConnectorCfg).(interfaces.ConnectorConfig)
	return cs.probeConnection(ctx, req.ConnectorType, config)
}

func (cs *catalogService) probeConnection(ctx context.Context, connectorType string,
	config interfaces.ConnectorConfig) (*interfaces.CatalogHealthCheckStatus, error) {
	connector, err := cs.cf.CreateConnectorInstance(ctx, connectorType, config)
	if err != nil {
		otellog.LogError(ctx, "Failed to create connector", err)
		return nil, connectorInitializationError(ctx, err)
	}
	defer func() { _ = connector.Close(ctx) }()

	result := &interfaces.CatalogHealthCheckStatus{
		LastCheckTime: time.Now().UnixMilli(),
	}
	if err := cs.testConnectorConnection(ctx, connector); err != nil {
		otellog.LogError(ctx, "Failed to test connection to data source", err)
		result.HealthCheckStatus = interfaces.CatalogHealthStatusUnhealthy
		result.HealthCheckResult = connectionErrorResult(err)
		return result, nil
	}
	result.HealthCheckStatus = interfaces.CatalogHealthStatusHealthy
	result.HealthCheckResult = connectionTestSucceededResult
	return result, nil
}

// connectionErrorResult formats the connector's credential-free error for health status storage.
func connectionErrorResult(err error) string {
	if err == nil {
		return ""
	}
	result := strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error()))
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

// encryptSensitiveFields validates plaintext and returns an independent storage configuration.
func (cs *catalogService) encryptSensitiveFields(sensitiveFields []string,
	config interfaces.ConnectorConfig) (encryptedConfig interfaces.ConnectorConfig, err error) {
	encryptedConfig = deepcopy.Copy(config).(interfaces.ConnectorConfig)
	variables, _ := encryptedConfig["variables"].(map[string]any)
	targets := []interfaces.ConnectorConfig{encryptedConfig, variables}
	for _, field := range sensitiveFields {
		for _, target := range targets {
			value, ok := target[field].(string)
			if ok && len(value) >= len(EncryptedPrefix) && strings.EqualFold(value[:len(EncryptedPrefix)], EncryptedPrefix) {
				return nil, fmt.Errorf("field %s: %w", field, errSensitivePlaintextPrefix)
			}
		}
	}
	if !cs.appSetting.CryptoSetting.Enabled {
		return encryptedConfig, nil
	}
	if cs.cipher == nil {
		return nil, errors.New("catalog encryption key is unavailable")
	}
	for _, field := range sensitiveFields {
		for _, target := range targets {
			value, ok := target[field].(string)
			if !ok || value == "" {
				continue
			}
			ciphertext, err := cs.cipher.Encrypt(value)
			if err != nil {
				return nil, fmt.Errorf("field %s: encryption failed", field)
			}
			target[field] = EncryptedPrefix + ciphertext
		}
	}
	return encryptedConfig, nil
}

// removeSensitiveFields hides declared names in both the top level and variables.
func (cs *catalogService) removeSensitiveFields(catalog *interfaces.Catalog) error {
	if catalog == nil || catalog.ConnectorType == "" {
		return nil
	}
	fields, err := cs.cf.GetSensitiveFields(catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return err
	}
	config := deepcopy.Copy(catalog.ConnectorCfg).(interfaces.ConnectorConfig)
	variables, _ := config["variables"].(map[string]any)
	for _, field := range fields {
		delete(config, field)
		delete(variables, field)
	}
	catalog.ConnectorCfg = config
	return nil
}

// decryptSensitiveFields prepares credentials without changing the stored configuration.
func (cs *catalogService) decryptSensitiveFields(sensitiveFields []string,
	config interfaces.ConnectorConfig) (decryptedConfig interfaces.ConnectorConfig, err error) {
	decryptedConfig = deepcopy.Copy(config).(interfaces.ConnectorConfig)
	variables, _ := decryptedConfig["variables"].(map[string]any)
	for _, field := range sensitiveFields {
		for _, target := range []interfaces.ConnectorConfig{decryptedConfig, variables} {
			value, ok := target[field].(string)
			if !ok || value == "" || !strings.HasPrefix(value, EncryptedPrefix) {
				continue
			}
			if cs.cipher == nil {
				return nil, fmt.Errorf("field %s: decryption key is unavailable", field)
			}
			plaintext, err := cs.cipher.Decrypt(strings.TrimPrefix(value, EncryptedPrefix))
			if err != nil {
				return nil, fmt.Errorf("field %s: invalid encrypted value", field)
			}
			target[field] = plaintext
		}
	}
	return decryptedConfig, nil
}

func (cs *catalogService) UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateMetadata")
	defer span.End()

	err := cs.ca.UpdateMetadata(ctx, id, metadata)
	if err != nil {
		otellog.LogError(ctx, "Update metadata failed", err)
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_UpdateFailed).WithErrorDetails(err.Error())
	}

	return nil
}

// ListAuthResourceEntries lists catalog authorization entries with filters.
func (cs *catalogService) ListAuthResourceEntries(ctx context.Context,
	params interfaces.AuthResourceQueryParams) ([]*interfaces.AuthResourceEntry, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListAuthResourceEntries")
	defer span.End()

	params.IncludeBuiltin = interfaces.IsBuiltinAdmin(ctx)
	entries, total, err := cs.ca.ListAuthResourceEntries(ctx, params)
	if err != nil {
		span.SetStatus(codes.Error, "ListAuthResourceEntries failed")
		return []*interfaces.AuthResourceEntry{}, 0, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Catalog_InternalError_GetFailed).WithErrorDetails(err.Error())
	}
	if len(entries) == 0 {
		return []*interfaces.AuthResourceEntry{}, total, nil
	}

	span.SetStatus(codes.Ok, "")
	return entries, total, nil
}
