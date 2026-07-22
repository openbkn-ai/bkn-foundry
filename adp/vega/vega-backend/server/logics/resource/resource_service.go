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

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
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
	interfaces.BuildTaskStatusInit,
	interfaces.BuildTaskStatusRunning,
	interfaces.BuildTaskStatusStopping,
}

type resourceService struct {
	appSetting *common.AppSetting
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

// resourceAuthResourceType 返回数据资源在权限服务中的资源类型：
// 系统内部目录下的资源按 internal_resource 注册，业务角色的 resource:* 通配授权匹配不到，仅超级管理员可见
func resourceAuthResourceType(internal bool) string {
	if internal {
		return interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE
	}
	return interfaces.AUTH_RESOURCE_TYPE_RESOURCE
}

// internalCatalogIDSet 查询所有系统内部目录 ID 集合
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

// internalResourceIDSet 查询所有系统内部目录下的资源 ID 集合
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

// partitionResourceIDs 将资源 ID 按是否属于系统内部目录分组
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

// fillResourceOpsBulk 用批量可访问集解析代替逐资源鉴权：对 resource 与
// internal_resource 两类各按 op 发一次 bulk 调用，得到「每个 op 的可访问 id 集
// 或通配标记」，据此判定可见性（view_detail）并计算每个可见资源的完整操作权限。
// 对每个可见 id 写入一条 out（与 filterResourcePermissions 的结果等价），但鉴权
// 往返数从 O(资源数) 降为 O(op 数 × 资源类型数)，与资源规模脱钩。
func (rs *resourceService) fillResourceOpsBulk(ctx context.Context, ids []string,
	internalSet map[string]struct{}, lister interfaces.AccessibleResourceLister,
	out map[string]interfaces.PermissionResourceOps) error {

	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}
	// fail-closed：账户缺失时不以空 accessor 调鉴权后端，回退到逐资源路径——
	// 那里的 PermissionServiceImpl.FilterResources 对空账户返回 403，与旧行为一致。
	if accountInfo.ID == "" || accountInfo.Type == "" {
		return interfaces.ErrBulkAuthzUnsupported
	}

	// 只对本次 ids 里实际出现的资源类型发起 bulk 解析：多数账号/部署没有内部资源，
	// 否则每次 List 都会白打一组 internal_resource 的鉴权往返。
	var hasNormal, hasInternal bool
	for _, id := range ids {
		if _, isInternal := internalSet[id]; isInternal {
			hasInternal = true
		} else {
			hasNormal = true
		}
		if hasNormal && hasInternal {
			break
		}
	}

	var (
		resAccess      map[string]interfaces.OpAccess
		internalAccess map[string]interfaces.OpAccess
		err            error
	)
	if hasNormal {
		resAccess, err = lister.AccessibleResourceIDs(ctx, accountInfo.ID,
			resourceAuthResourceType(false), interfaces.COMMON_OPERATIONS)
		if err != nil {
			return err
		}
	}
	if hasInternal {
		internalAccess, err = lister.AccessibleResourceIDs(ctx, accountInfo.ID,
			resourceAuthResourceType(true), interfaces.COMMON_OPERATIONS)
		if err != nil {
			return err
		}
	}

	for _, id := range ids {
		access := resAccess
		if _, isInternal := internalSet[id]; isInternal {
			access = internalAccess
		}
		// 无 view_detail 则不可见，跳过
		if vd := access[interfaces.OPERATION_TYPE_VIEW_DETAIL]; !vd.All && !vd.IDs[id] {
			continue
		}
		ops := make([]string, 0, len(interfaces.COMMON_OPERATIONS))
		for _, op := range interfaces.COMMON_OPERATIONS {
			if a := access[op]; a.All || a.IDs[id] {
				ops = append(ops, op)
			}
		}
		out[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: ops}
	}
	return nil
}

// filterResourcePermissions 按内部/普通资源分组做权限过滤：内部目录下的资源按
// internal_resource 类型校验，其余按 resource 类型校验，结果合并返回
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
	return result, nil
}

// Create creates a new Resource.
func (rs *resourceService) Create(ctx context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create resource")
	defer span.End()

	// 内部目录下的资源按 internal_resource 类型校验/注册，默认仅超级管理员/系统 S2S 身份可建
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	_, parentInternal := internalCatalogs[req.CatalogID]
	authType := resourceAuthResourceType(parentInternal)

	// 判断userid是否有创建数据资源的权限（策略决策）
	err = rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: authType,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE})
	if err != nil {
		return nil, err
	}

	// Get account info from context
	accountInfo := interfaces.AccountInfo{}
	if v := ctx.Value(interfaces.ACCOUNT_INFO_KEY); v != nil {
		accountInfo = v.(interfaces.AccountInfo)
	}

	// 检查catalog是否存在
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

	if err := extensions.ValidateSchemaPropertiesExtensions(ctx, req.SchemaDefinition); err != nil {
		return nil, err
	}
	if err := rs.validateIndexConfigModels(ctx, req.SchemaDefinition, req.IndexConfig); err != nil {
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
		Database:         req.Database,
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

	err = rs.ra.Create(ctx, resource)
	if err != nil {
		otellog.LogError(ctx, "Create resource failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	if req.Extensions != nil {
		if err := entityextension.NewStore(rs.appSetting).Replace(ctx, entityextension.KindResource, resource.ID, *req.Extensions); err != nil {
			_ = rs.ra.DeleteByIDs(ctx, []string{resource.ID})
			logger.Errorf("Replace resource extensions failed: %v", err)
			span.SetStatus(codes.Error, "Replace resource extensions failed")
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
				WithErrorDetails(err.Error())
		}
	}

	switch resource.Category {
	case interfaces.ResourceCategoryDataset:
		// create dataset
		if err := rs.ds.Create(ctx, resource); err != nil {
			logger.Errorf("Create dataset failed: %v", err)
			// 数据集创建失败不影响资源创建，只记录错误
		}
	}

	// 注册资源
	err = rs.ps.CreateResources(ctx, []interfaces.PermissionResource{{
		ID:   resource.ID,
		Type: authType,
		Name: resource.Name,
	}}, interfaces.COMMON_OPERATIONS)
	if err != nil {
		logger.Errorf("CreateResources error: %s", err.Error())
		span.SetStatus(codes.Error, "创建资源失败")
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

	// 根据权限过滤有查看权限的对象，过滤后的数组的总长度就是总数，无需再请求总数；
	// 内部目录下的资源按 internal_resource 类型校验
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return nil, err
	}
	_, parentInternal := internalCatalogs[resource.CatalogID]
	if parentInternal && interfaces.IsS2SInternalAccess(ctx) {
		// 内部目录资源经集群内 S2S 访问（/in/ 内网端点）：系统内部基础设施默认放行，
		// 不做 per-account view_detail 校验——这类资源从不授权给业务用户，
		// 内部服务代用户访问时按 per-account 校验只会误拒。外网端点不会带该标记。
		resource.Operations = interfaces.COMMON_OPERATIONS
	} else {
		matchResoucesMap, err := rs.ps.FilterResources(ctx, resourceAuthResourceType(parentInternal), []string{resource.ID},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return nil, err
		}

		if resrc, exist := matchResoucesMap[resource.ID]; exist {
			resource.Operations = resrc.Operations // 用户当前有权限的操作
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
	}

	accountInfos := []*interfaces.AccountInfo{&resource.Creator, &resource.Updater}
	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return resource, nil
}

func (rs *resourceService) InternalGetByID(ctx context.Context, id string) (*interfaces.Resource, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalGetByID")
	defer span.End()

	return rs.ra.GetByID(ctx, id)
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

	// 根据权限过滤有查看权限的对象，过滤后的数组的总长度就是总数，无需再请求总数；
	// 内部目录下的资源按 internal_resource 类型校验
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
			resource.Operations = resrc.Operations // 用户当前有权限的操作
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
				WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for[%v]", interfaces.OPERATION_TYPE_VIEW_DETAIL))
		}
		accountInfos = append(accountInfos, &resource.Creator, &resource.Updater)
	}

	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_Resource_InternalError_GetAccountNamesFailed).WithErrorDetails(err.Error())
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

	// 查询所有资源的ID
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

	// 内部目录下的资源 ID 集合，权限校验时按 internal_resource 类型分组
	internalResources, err := rs.internalResourceIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal resource IDs failed")
		return []*interfaces.Resource{}, 0, err
	}

	// 根据权限过滤有查看权限的ID数组，附带每个可见资源的操作权限
	// 所有有权限的resource及其操作权限
	matchResourceOpsMap := make(map[string]interfaces.PermissionResourceOps)

	// 快路径：权限服务支持批量可访问集解析时（bkn-safe），按 op 各发一次 bulk
	// 调用解析可见集与每资源操作权限，避免对全部资源逐个鉴权的 fan-out——持全量
	// 目录授权的账号会因逐个鉴权而超时（#357）。后端无 bulk 解析器时返回
	// ErrBulkAuthzUnsupported，退回逐批 filter 的旧路径。
	bulkDone := false
	if lister, ok := rs.ps.(interfaces.AccessibleResourceLister); ok {
		err = rs.fillResourceOpsBulk(ctx, ids, internalResources, lister, matchResourceOpsMap)
		switch {
		case err == nil:
			bulkDone = true
		case errors.Is(err, interfaces.ErrBulkAuthzUnsupported):
			// 后端不支持 bulk 解析，退回逐资源过滤
		default:
			span.SetStatus(codes.Error, "Bulk authz resolve error")
			return []*interfaces.Resource{}, 0, err
		}
	}
	if !bulkDone {
		// 旧路径：分批逐个过滤，每批1万个ids，
		// fix权限接口报错prepared statement contains too many placeholders
		batchSize := 10000
		for i := 0; i < len(ids); i += batchSize {
			end := i + batchSize
			if end > len(ids) {
				end = len(ids)
			}
			batchIDs := ids[i:end]

			var batchMatchResources map[string]interfaces.PermissionResourceOps
			// 校验权限管理的操作权限
			batchMatchResources, err = rs.filterResourcePermissions(ctx, batchIDs, internalResources,
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true)
			if err != nil {
				span.SetStatus(codes.Error, "Filter resources error")
				return []*interfaces.Resource{}, 0, err
			}

			// 合并结果
			for _, resourceOps := range batchMatchResources {
				matchResourceOpsMap[resourceOps.ResourceID] = resourceOps
			}
		}
	}

	// 提取有权限的资源ID，保持与ids的顺序一致
	authorizedIDs := make([]string, 0, len(matchResourceOpsMap))
	for _, id := range ids {
		if _, exist := matchResourceOpsMap[id]; exist {
			authorizedIDs = append(authorizedIDs, id)
		}
	}
	total := int64(len(authorizedIDs))

	// 如果没有有权限的资源，直接返回空结果
	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.Resource{}, total, nil
	}

	// 根据有权限的ID数组查询完整资源，并应用分页
	// limit = -1,则返回所有
	if params.Limit != -1 {
		// 分页处理authorizedIDs
		// 检查起始位置是否越界
		if params.Offset < 0 || params.Offset >= len(authorizedIDs) {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.Resource{}, total, nil
		}
		// 计算结束位置
		end := params.Offset + params.Limit
		if end > len(authorizedIDs) {
			end = len(authorizedIDs)
		}
		// 只查询当前页的资源ID
		authorizedIDs = authorizedIDs[params.Offset:end]
	}

	// 根据有权限的ID数组查询完整资源
	// 分批处理，每批10000个ids, 避免prepared statement contains too many placeholders错误
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

	// 设置资源操作权限
	for _, c := range resources {
		if resrc, exist := matchResourceOpsMap[c.ID]; exist {
			c.Operations = resrc.Operations // 用户当前有权限的操作
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(resources)*2)
	for _, c := range resources {
		accountInfos = append(accountInfos, &c.Creator, &c.Updater)
	}

	err = rs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.Resource{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
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
	// 判断userid是否有修改权限；内部目录下的资源按 internal_resource 类型校验
	internalCatalogs, err := rs.internalCatalogIDSet(ctx)
	if err != nil {
		span.SetStatus(codes.Error, "List internal catalog IDs failed")
		return err
	}
	_, parentInternal := internalCatalogs[resource.CatalogID]
	authType := resourceAuthResourceType(parentInternal)
	err = rs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: authType,
		ID:   resource.ID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
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
		applyMutableSchemaFields(resource.SchemaDefinition, req.SchemaDefinition)
	}
	if req.IndexConfig != nil {
		resource.IndexConfig = req.IndexConfig
	}

	if err := extensions.ValidateSchemaPropertiesExtensions(ctx, resource.SchemaDefinition); err != nil {
		return err
	}
	if err := rs.validateIndexConfigModels(ctx, resource.SchemaDefinition, resource.IndexConfig); err != nil {
		return err
	}
	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return err
		}
	}

	// 检查catalog是否存在
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

	if err := rs.ra.Update(ctx, nil, resource); err != nil {
		span.SetStatus(codes.Error, "Update resource failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	if req.Extensions != nil {
		if err := entityextension.NewStore(rs.appSetting).Replace(ctx, entityextension.KindResource, resource.ID, *req.Extensions); err != nil {
			span.SetStatus(codes.Error, "Replace resource extensions failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateStatus updates a Resource's status.
func (rs *resourceService) UpdateStatus(ctx context.Context, id string, status string, statusMessage string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource status")
	defer span.End()

	if err := rs.ra.UpdateStatus(ctx, id, status, statusMessage); err != nil {
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

	// 判断userid是否有删除权限；内部目录下的资源按 internal_resource 类型校验
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

	// 检查是否有删除权限
	if len(matchResoucesMap) != len(ids) {
		// 请求的资源id可以重复，未去重，资源过滤出来的资源id是去重过的，所以单纯判断数量不准确
		for _, id := range ids {
			if _, exist := matchResoucesMap[id]; !exist {
				return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
					WithErrorDetails("Access denied: insufficient permissions for resource's delete operation.")
			}
		}
	}

	// 先获取要删除的资源信息，以便对不同的资源进行不同的处理
	resources, err := rs.ra.GetByIDs(ctx, ids)
	if err != nil {
		span.SetStatus(codes.Error, "Get resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_GetFailed).
			WithErrorDetails(err.Error())
	}

	for _, resource := range resources {
		switch resource.Category {
		case interfaces.ResourceCategoryTable:
			// 级联清掉该资源的全部构建任务 + 对应 OpenSearch 索引（含历史孤儿）。
			// 改自原先"有任务就拒删"：现在删资源连带删任务/索引（危险操作由前端二次确认把关）。
			// 运行中/停止中任务会被 cascade 拒绝（HasRunningExecution），用户需先停止再删。
			if err := logics.CascadeDeleteBuildTasks(ctx, rs.bta, rs.lim,
				interfaces.BuildTasksQueryParams{ResourceID: resource.ID}); err != nil {
				span.SetStatus(codes.Error, "Cascade delete build tasks failed")
				return err
			}
		case interfaces.ResourceCategoryDataset:
			// Delete dataset
			if err := rs.ds.Delete(ctx, resource.ID); err != nil {
				logger.Errorf("Delete dataset failed: %v", err)
				// 数据集删除失败不影响资源删除，只记录错误
			}
		}
	}

	if err := rs.ra.DeleteByIDs(ctx, ids); err != nil {
		span.SetStatus(codes.Error, "Delete resources failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	//  清除资源策略，按内部/普通资源分组删除对应类型的策略
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

// UpdateResource updates a Resource directly.
func (rs *resourceService) UpdateResource(ctx context.Context, resource *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update resource")
	defer span.End()

	if err := rs.ra.Update(ctx, nil, resource); err != nil {
		span.SetStatus(codes.Error, "Update resource failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (rs *resourceService) InternalUpdate(ctx context.Context, tx *sql.Tx, resource *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ResourceService.InternalUpdate")
	defer span.End()

	return rs.ra.Update(ctx, tx, resource)
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
		Database:         req.Database,
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
	if err := rs.ra.CreateWithTx(ctx, tx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func (rs *resourceService) InternalUpdateStatus(ctx context.Context, tx *sql.Tx, id string, status string, statusMessage string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	return rs.ra.UpdateStatusWithTx(ctx, tx, id, status, statusMessage)
}

func (rs *resourceService) rejectBuildRelevantUpdateWhenActiveBuildTask(ctx context.Context, resource *interfaces.Resource) error {
	tasks, _, err := rs.bta.List(ctx, interfaces.BuildTasksQueryParams{
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

func (rs *resourceService) validateResourceUpdateScope(ctx context.Context, resource *interfaces.Resource, req *interfaces.ResourceRequest) (bool, error) {
	if resource.Category == interfaces.ResourceCategoryLogicView {
		return req.LogicDefinition != nil && !reflect.DeepEqual(resource.LogicDefinition, req.LogicDefinition), nil
	}
	if req.Database != "" && resource.Database != req.Database {
		return false, unsupportedResourceUpdateError(ctx, "database is managed by discover and cannot be updated directly")
	}
	if req.SourceIdentifier != "" && resource.SourceIdentifier != req.SourceIdentifier {
		return false, unsupportedResourceUpdateError(ctx, "source_identifier is managed by discover and cannot be updated directly")
	}
	if req.SourceMetadata != nil && !reflect.DeepEqual(resource.SourceMetadata, req.SourceMetadata) {
		return false, unsupportedResourceUpdateError(ctx, "source_metadata is managed by discover and cannot be updated directly")
	}
	indexConfigChanged := req.IndexConfig != nil && !reflect.DeepEqual(resource.IndexConfig, req.IndexConfig)
	if req.SchemaDefinition == nil {
		return indexConfigChanged, nil
	}
	schemaChanged, err := validateMutableSchemaUpdate(ctx, resource.SchemaDefinition, req.SchemaDefinition)
	return schemaChanged || indexConfigChanged, err
}

func (rs *resourceService) validateIndexConfigModels(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if err := validateIndexConfigBuildKeyFields(ctx, schema, indexConfig); err != nil {
		return err
	}
	if rs.mfs == nil {
		return nil
	}
	defaultEmbeddingModel := ""
	if indexConfig != nil {
		defaultEmbeddingModel = strings.TrimSpace(indexConfig.DefaultEmbeddingModel)
	}
	checkedModels := map[string]struct{}{}
	for _, prop := range schema {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != interfaces.PropertyFeatureType_Vector {
				continue
			}
			modelName := ""
			if feature.Config != nil {
				if value, ok := feature.Config["embedding_model"].(string); ok {
					modelName = strings.TrimSpace(value)
				}
			}
			if modelName == "" {
				modelName = defaultEmbeddingModel
			}
			if modelName == "" {
				modelName = interfaces.DEFAULT_EMBEDDING_MODEL
			}
			if _, ok := checkedModels[modelName]; ok {
				continue
			}
			if _, err := rs.mfs.GetModelByName(ctx, modelName); err != nil {
				fieldName := prop.Name
				if feature.RefProperty != "" {
					fieldName = feature.RefProperty
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
					WithErrorDetails(fmt.Sprintf("embedding model %q for field %q not found", modelName, fieldName))
			}
			checkedModels[modelName] = struct{}{}
		}
	}
	return nil
}

func validateIndexConfigBuildKeyFields(ctx context.Context, schema []*interfaces.Property, indexConfig *interfaces.ResourceIndexConfig) error {
	if indexConfig == nil || len(indexConfig.BuildKeyFields) == 0 {
		return nil
	}

	schemaFields := make(map[string]struct{}, len(schema))
	for _, prop := range schema {
		if prop != nil {
			schemaFields[prop.Name] = struct{}{}
		}
	}
	for _, field := range indexConfig.BuildKeyFields {
		if _, exists := schemaFields[field]; !exists {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
				WithErrorDetails(fmt.Sprintf("build_key_fields field %q is not in the resource schema", field))
		}
	}
	return nil
}

func unsupportedResourceUpdateError(ctx context.Context, details string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_RequestBody).
		WithErrorDetails(details)
}

func validateMutableSchemaUpdate(ctx context.Context, current []*interfaces.Property, requested []*interfaces.Property) (bool, error) {
	if len(current) != len(requested) {
		return false, unsupportedResourceUpdateError(ctx, "schema_definition can only update field display_name, description, and features")
	}

	currentByName := make(map[string]*interfaces.Property, len(current))
	for _, prop := range current {
		if prop == nil || prop.Name == "" {
			return false, unsupportedResourceUpdateError(ctx, "current schema_definition contains an invalid field")
		}
		currentByName[prop.Name] = prop
	}

	featuresChanged := false
	seen := make(map[string]struct{}, len(requested))
	for _, requestedProp := range requested {
		if requestedProp == nil || requestedProp.Name == "" {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition contains an invalid field")
		}
		currentProp, ok := currentByName[requestedProp.Name]
		if !ok {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition cannot add, remove, or rename fields")
		}
		if _, dup := seen[requestedProp.Name]; dup {
			return false, unsupportedResourceUpdateError(ctx, "schema_definition contains duplicate fields")
		}
		seen[requestedProp.Name] = struct{}{}

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
			featuresChanged = true
		}
	}
	return featuresChanged, nil
}

func applyMutableSchemaFields(current []*interfaces.Property, requested []*interfaces.Property) {
	if requested == nil {
		return
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
		}
	}
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
	// 系统内部目录下的资源按 internal_resource 类型授权，不进入 resource 类型的授权资源清单
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
