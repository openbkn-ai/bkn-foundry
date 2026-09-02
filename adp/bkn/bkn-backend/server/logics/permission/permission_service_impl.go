// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	mqclient "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

func localizedPermissionDetail(ctx context.Context, key string) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail."+key, nil)
}

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
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckPermission")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "CheckPermission missing account ID or type", httpErr)
		return httpErr
	}

	// Permission checks are temporarily disabled.
	ok, err := ps.pa.CheckPermission(ctx, interfaces.PermissionCheck{
		Accessor: interfaces.PermissionAccessor{
			ID:   accountInfo.ID,
			Type: accountInfo.Type,
		},
		Resource:   resource,
		Operations: ops,
	})
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CheckPermissionFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "CheckPermission failed", httpErr)
		return httpErr
	}
	if !ok {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "PermissionDenied"))
		otellog.LogError(ctx, "CheckPermission denied", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// CreateResources creates permission policies for newly created resources.
func (ps *PermissionServiceImpl) CreateResources(ctx context.Context, resources []interfaces.PermissionResource, ops []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreatePermissionResources")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "CreateResources missing account ID or type", httpErr)
		return httpErr
	}

	// Permission resource creation is temporarily disabled.
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
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err.Error())
		otellog.LogError(ctx, "CreateResources failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteResources deletes permission policies for the supplied resources.
func (ps *PermissionServiceImpl) DeleteResources(ctx context.Context, resourceType string, ids []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeletePermissionResources")
	defer span.End()

	if len(ids) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	// Permission resource deletion is temporarily disabled.

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			Type: resourceType,
			ID:   id,
		})
	}

	err := ps.pa.DeleteResources(ctx, resources)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "DeleteResources failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// UpsertResourceParents synchronizes concrete KN child ownership into bkn-safe.
func (ps *PermissionServiceImpl) UpsertResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpsertPermissionResourceParents")
	defer span.End()

	if len(items) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := ps.pa.UpsertResourceParents(ctx, resourceType, parentType, items); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_CreateResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpsertResourceParents failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteResourceParents removes concrete KN child ownership from bkn-safe.
func (ps *PermissionServiceImpl) DeleteResourceParents(ctx context.Context, resourceType string,
	resourceIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeletePermissionResourceParents")
	defer span.End()

	if len(resourceIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := ps.pa.DeleteResourceParents(ctx, resourceType, resourceIDs); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_DeleteResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "DeleteResourceParents failed", httpErr)
		return httpErr
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// Filter the resource list.
func (ps *PermissionServiceImpl) FilterResources(ctx context.Context, resourceType string, ids []string,
	ops []string, allowOperation bool, fullOps []string) (map[string]interfaces.PermissionResourceOps, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "FilterPermissionResources")
	defer span.End()

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	if accountInfo.ID == "" || accountInfo.Type == "" {
		httpErr := rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails(localizedPermissionDetail(ctx, "AccountInfoMissing"))
		otellog.LogError(ctx, "FilterResources missing account ID or type", httpErr)
		return nil, httpErr
	}

	resources := []interfaces.PermissionResource{}
	for _, id := range ids {
		resources = append(resources, interfaces.PermissionResource{
			ID:   id,
			Type: resourceType,
		})
	}

	// The access filter and fullOps both need to be sent to preserve the operation candidates returned to Studio.
	// bkn-safe intersects the requested operation list, so omitting fullOps hides edit and delete actions.
	matchResouces, err := ps.pa.FilterResources(ctx, interfaces.PermissionResourcesFilter{
		Accessor: interfaces.PermissionAccessor{
			ID:   accountInfo.ID,
			Type: accountInfo.Type,
		},
		Resources:           resources,
		Operations:          ops,
		CandidateOperations: fullOps,
		AllowOperation:      allowOperation,
	})
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_FilterResourcesFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "FilterResources failed", httpErr)
		return nil, httpErr
	}

	// Convert resource IDs to a lookup map.
	requestedIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		requestedIDs[id] = struct{}{}
	}
	idMap := make(map[string]interfaces.PermissionResourceOps, len(matchResouces))
	for key, resourceOps := range matchResouces {
		if _, ok := requestedIDs[key]; !ok || resourceOps.ResourceID != key {
			httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_InternalError_FilterResourcesFailed).
				WithErrorDetails("invalid resource-filter response")
			otellog.LogError(ctx, "FilterResources returned an invalid resource", httpErr)
			return nil, httpErr
		}
		idMap[resourceOps.ResourceID] = resourceOps
	}

	span.SetStatus(codes.Ok, "")
	return idMap, nil
}

// UpdateResource updates a resource name through the authorization event channel.
func (ps *PermissionServiceImpl) UpdateResource(ctx context.Context, resource interfaces.PermissionResource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdatePermissionResource")
	defer span.End()

	bytes, err := sonic.Marshal(resource)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_MarshalDataFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpdateResource marshal failed", httpErr)
		return httpErr
	}

	err = ps.mqClient.Pub(interfaces.AUTHORIZATION_RESOURCE_NAME_MODIFY, bytes)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_InternalError_UpdateResourceFailed).WithErrorDetails(err)
		otellog.LogError(ctx, "UpdateResource publish failed", httpErr)
		return httpErr
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
