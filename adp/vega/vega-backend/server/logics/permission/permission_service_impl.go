package permission

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	mqclient "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

type PermissionServiceImpl struct {
	appSetting *common.AppSetting
	mqClient   mqclient.OpenBKNMQClient
	pa         interfaces.PermissionAccess
}

func NewPermissionServiceImpl(appSetting *common.AppSetting) interfaces.PermissionService {
	mqSetting := appSetting.MQSetting
	client, err := mqclient.NewOpenBKNMQClient(mqSetting.MQHost, mqSetting.MQPort,
		mqSetting.MQHost, mqSetting.MQPort, mqSetting.MQType,
		mqclient.UserInfo(mqSetting.Auth.Username, mqSetting.Auth.Password),
		mqclient.AuthMechanism(mqSetting.Auth.Mechanism),
	)
	if err != nil {
		logger.Fatal("failed to create a openbkn mq client:", err)
	}
	return &PermissionServiceImpl{
		appSetting: appSetting,
		mqClient:   client,
		pa:         logics.PA,
	}
}

func (ps *PermissionServiceImpl) CheckPermission(ctx context.Context, resource interfaces.PermissionResource, ops []string) error {
	requirements := make([]interfaces.PermissionRequirement, 0, len(ops))
	for _, operation := range ops {
		requirements = append(requirements, interfaces.PermissionRequirement{Resource: resource, Operation: operation})
	}
	return ps.RequirePermissions(ctx, requirements)
}

func (ps *PermissionServiceImpl) CheckPermissions(ctx context.Context,
	requirements []interfaces.PermissionRequirement) ([]interfaces.PermissionCheckResult, error) {
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("Access denied: missing account ID or type")
	}
	if len(requirements) == 0 {
		return []interfaces.PermissionCheckResult{}, nil
	}

	response, err := ps.pa.CheckPermissions(ctx, interfaces.PermissionChecksRequest{
		AccessorID: accountInfo.ID,
		Checks:     requirements,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_CheckPermissionFailed).WithErrorDetails(err)
	}
	return response.Results, nil
}

func (ps *PermissionServiceImpl) RequirePermissions(ctx context.Context,
	requirements []interfaces.PermissionRequirement) error {
	results, err := ps.CheckPermissions(ctx, requirements)
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Allowed {
			continue
		}
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(fmt.Sprintf("Access denied: insufficient permissions for %s on %s:%s",
				result.Operation, result.ResourceType, result.ResourceID))
	}
	return nil
}

func (ps *PermissionServiceImpl) UpsertResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) error {
	if len(items) == 0 {
		return nil
	}
	if err := ps.pa.UpsertResourceParents(ctx, resourceType, parentType, items); err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err)
	}
	return nil
}

func (ps *PermissionServiceImpl) DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	if err := ps.pa.DeleteResourceParents(ctx, resourceType, resourceIDs); err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
	}
	return nil
}

func (ps *PermissionServiceImpl) GetResourceParents(ctx context.Context, resourceType string,
	resourceIDs []string) (map[string]interfaces.PermissionResourceParent, error) {
	if len(resourceIDs) == 0 {
		return map[string]interfaces.PermissionResourceParent{}, nil
	}
	items, err := ps.pa.GetResourceParents(ctx, resourceType, resourceIDs)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_FilterResourcesFailed).WithErrorDetails(err)
	}
	return items, nil
}

func (ps *PermissionServiceImpl) CreateResources(ctx context.Context, resources []interfaces.PermissionResource, ops []string) error {
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		return rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("Access denied: missing account ID or type")
	}

	allowOps := []interfaces.PermissionOperation{}
	for _, op := range ops {
		allowOps = append(allowOps, interfaces.PermissionOperation{
			Operation: op,
		})
	}

	policies := []interfaces.PermissionPolicy{}
	for _, resource := range resources {
		policies = append(policies, interfaces.PermissionPolicy{
			Accessor: interfaces.PermissionAccessor{
				Type: accountInfo.Type,
				ID:   accountInfo.ID,
			},
			Resource: resource,
			Operations: interfaces.PermissionPolicyOps{
				Allow: allowOps,
				Deny:  []interfaces.PermissionOperation{},
			},
		})
	}

	err := ps.pa.CreateResources(ctx, policies)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err.Error())
	}
	return nil
}

func (ps *PermissionServiceImpl) DeleteResources(ctx context.Context, resourceType string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			Type: resourceType,
			ID:   id,
		})
	}

	err := ps.pa.DeleteResources(ctx, resources)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
	}
	return nil
}

func (ps *PermissionServiceImpl) UpdateResource(ctx context.Context, resource interfaces.PermissionResource) error {
	bytes, err := sonic.Marshal(resource)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_MarshalDataFailed).WithErrorDetails(err)
	}

	err = ps.mqClient.Pub(interfaces.AUTHORIZATION_RESOURCE_NAME_MODIFY, bytes)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_UpdateResourceFailed).WithErrorDetails(err)
	}

	return nil
}

func (ps *PermissionServiceImpl) FilterVisibleResources(ctx context.Context, resourceType string, ids []string,
	visibilityOperations []string, visibilityMatch string) (map[string]interfaces.PermissionResourceOps, error) {
	return ps.filterResources(ctx, resourceType, ids, visibilityOperations, visibilityMatch, false)
}

func (ps *PermissionServiceImpl) FilterVisibleResourcesWithOperations(ctx context.Context, resourceType string,
	ids []string, visibilityOperations []string, visibilityMatch string) (map[string]interfaces.PermissionResourceOps, error) {
	return ps.filterResources(ctx, resourceType, ids, visibilityOperations, visibilityMatch, true)
}

func (ps *PermissionServiceImpl) filterResources(ctx context.Context, resourceType string, ids []string,
	visibilityOperations []string, visibilityMatch string,
	includeOperations bool) (map[string]interfaces.PermissionResourceOps, error) {
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("Access denied: missing account ID or type")
	}

	if len(ids) == 0 {
		return map[string]interfaces.PermissionResourceOps{}, nil
	}

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			ID:   id,
			Type: resourceType,
		})
	}

	matchResouces, err := ps.pa.FilterResources(ctx, interfaces.PermissionResourcesFilter{
		Accessor: interfaces.PermissionAccessor{
			ID:   accountInfo.ID,
			Type: accountInfo.Type,
		},
		Resources:         resources,
		Operations:        visibilityOperations,
		VisibilityMatch:   visibilityMatch,
		IncludeOperations: includeOperations,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_FilterResourcesFailed).WithErrorDetails(err)
	}

	idMap := map[string]interfaces.PermissionResourceOps{}
	for _, resourceOps := range matchResouces {
		idMap[resourceOps.ResourceID] = resourceOps
	}

	return idMap, nil
}
