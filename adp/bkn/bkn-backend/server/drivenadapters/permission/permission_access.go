// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	pAccessOnce sync.Once
	pAccess     interfaces.PermissionAccess
)

type permissionAccess struct {
	appSetting    *common.AppSetting
	permissionUrl string
	httpClient    rest.HTTPClient
}

type PermissionError struct {
	Code        string `json:"code"`        // Error code
	Message     string `json:"message"`     // Error message
	Description string `json:"description"` // Error description
	Cause       any    `json:"cause"`       // Cause
}

func NewPermissionAccess(appSetting *common.AppSetting) interfaces.PermissionAccess {
	pAccessOnce.Do(func() {
		pAccess = &permissionAccess{
			appSetting:    appSetting,
			permissionUrl: appSetting.PermissionUrl,
			httpClient:    common.NewHTTPClient(),
		}
	})

	return pAccess
}

// CheckPermission evaluates an access policy.
func (pa *permissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "请求策略的决策接口")
	defer span.End()

	span.SetAttributes(
		attr.Key("user_id").String(check.Accessor.ID),
		attr.Key("resource_id").String(check.Resource.ID),
		attr.Key("Operation").StringSlice(check.Operations),
	)

	httpUrl := fmt.Sprintf("%s/operation-check", pa.permissionUrl)

	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:            httpUrl,
		HttpMethod:         http.MethodPost,
		HttpContentType:    rest.ContentTypeJson,
		HttpMethodOverride: http.MethodGet,
	})

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}

	check.Method = http.MethodGet
	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, check)
	logger.Debugf("CheckPermission finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Record the failure.
		common.LogSafeError(ctx, "Post operation-check request failed", err)
		return false, fmt.Errorf("post operation-check request failed: %v", err)
	}

	if respCode != http.StatusOK {
		// Convert to a base error.
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the failure.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Record the failure.
			common.LogSafeError(ctx, "Unmalshal PermissionError failed", err)
			return false, err
		}

		description := permissionError.Message
		if description == "" {
			description = permissionError.Description
		}
		httpErr := &rest.HTTPError{
			HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    permissionError.Code,
				Description:  description,
				ErrorDetails: permissionError.Cause,
			}}

		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Record the failure.
		common.LogSafeError(ctx, "Post operation-check failed", httpErr)
		return false, httpErr
	}

	if result == nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		// Record the missing-model failure.
		otellog.LogWarn(ctx, "Http response body is null")
		return false, nil
	}

	// Process the response result.
	var checkResult interfaces.PermissionCheckResult
	if err := sonic.Unmarshal(result, &checkResult); err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal operation-check result failed")
		// Record the failure.
		common.LogSafeError(ctx, "Unmalshal operation-check result failed", err)
		return false, err
	}

	// Add trace attributes for a successful response.
	oteltrace.AddHttpAttrs4Ok(span, respCode)

	return checkResult.Result, nil
}

func isKNChildResourceType(resourceType string) bool {
	switch resourceType {
	case interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.RESOURCE_TYPE_METRIC,
		interfaces.RESOURCE_TYPE_RISK_TYPE:
		return true
	default:
		return false
	}
}

// Create a policy.
func (pa *permissionAccess) CreateResources(ctx context.Context, policies []interfaces.PermissionPolicy) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "请求创建决策接口")
	defer span.End()

	span.SetAttributes(
		attr.Key("user_id").String(policies[0].Accessor.ID),
		attr.Key("resource_id").String(policies[0].Resource.ID),
		attr.Key("Operation").String(fmt.Sprintf("%v", policies[0].Operations)),
	)

	httpUrl := fmt.Sprintf("%s/policy", pa.permissionUrl)

	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:            httpUrl,
		HttpMethod:         http.MethodPost,
		HttpContentType:    rest.ContentTypeJson,
		HttpMethodOverride: http.MethodGet,
	})

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}

	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, policies)
	logger.Debugf("CreateResources finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Record the failure.
		common.LogSafeError(ctx, "Post create policy request failed", err)
		return fmt.Errorf("post create policy request failed: %v", err)
	}

	if respCode != http.StatusNoContent {
		// Convert to a base error.
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the failure.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Record the failure.
			common.LogSafeError(ctx, "Unmalshal PermissionError failed", err)
			return err
		}

		description := permissionError.Message
		if description == "" {
			description = permissionError.Description
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    permissionError.Code,
				Description:  description,
				ErrorDetails: permissionError.Cause,
			}}
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Record the failure.
		common.LogSafeError(ctx, "Post create policy failed", httpErr)
		return httpErr
	}

	// Add trace attributes for a successful response.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// Delete a resource policy.
func (pa *permissionAccess) DeleteResources(ctx context.Context, res []interfaces.PermissionResource) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "请求删除决策接口")
	defer span.End()

	createUrl := fmt.Sprintf("%s/policy-delete", pa.permissionUrl)

	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:            createUrl,
		HttpMethod:         http.MethodPost,
		HttpContentType:    rest.ContentTypeJson,
		HttpMethodOverride: http.MethodDelete,
	})

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}

	st := map[string]any{
		"method":    http.MethodDelete,
		"resources": res,
	}

	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, createUrl, headers, st)
	logger.Debugf("DeleteResources finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Record the failure.
		common.LogSafeError(ctx, "Post delete policy request failed", err)
		return fmt.Errorf("post delete policy request failed: %v", err)
	}

	if respCode != http.StatusNoContent {
		// Convert to a base error.
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the failure.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Record the failure.
			common.LogSafeError(ctx, "Unmalshal PermissionError failed", err)
			return err
		}

		description := permissionError.Message
		if description == "" {
			description = permissionError.Description
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    permissionError.Code,
				Description:  description,
				ErrorDetails: permissionError.Cause,
			}}
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Record the failure.
		common.LogSafeError(ctx, "Post delete policy failed", httpErr)
		return httpErr
	}

	// Add trace attributes for a successful response.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// UpsertResourceParents is a no-op for the legacy permission backend, which has
// no instance hierarchy API. The bkn-safe and shadow adapters override it.
func (pa *permissionAccess) UpsertResourceParents(_ context.Context, _, _ string,
	_ []interfaces.PermissionResourceParent) error {
	return nil
}

// DeleteResourceParents is a no-op for the legacy permission backend.
func (pa *permissionAccess) DeleteResourceParents(_ context.Context, _ string, _ []string) error {
	return nil
}

// Evaluate an access policy.
func (pa *permissionAccess) FilterResources(ctx context.Context,
	filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "请求资源过滤接口")
	defer span.End()

	span.SetAttributes(
		attr.Key("user_id").String(filter.Accessor.ID),
		attr.Key("Operation").StringSlice(filter.Operations),
	)

	httpUrl := fmt.Sprintf("%s/resource-filter", pa.permissionUrl)

	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:            httpUrl,
		HttpMethod:         http.MethodPost,
		HttpContentType:    rest.ContentTypeJson,
		HttpMethodOverride: http.MethodGet,
	})

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}

	filter.Method = http.MethodGet
	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, filter)
	logger.Debugf("FilterResources finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Record the failure.
		common.LogSafeError(ctx, "Post resource-filter request failed", err)
		return map[string]interfaces.PermissionResourceOps{}, fmt.Errorf("post resource-filter request failed: %v", err)
	}

	if respCode != http.StatusOK {
		// Convert to a base error.
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the failure.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Record the failure.
			common.LogSafeError(ctx, "Unmalshal PermissionError failed", err)
			return map[string]interfaces.PermissionResourceOps{}, err
		}

		description := permissionError.Message
		if description == "" {
			description = permissionError.Description
		}
		httpErr := &rest.HTTPError{HTTPCode: respCode,
			BaseError: rest.BaseError{
				ErrorCode:    permissionError.Code,
				Description:  description,
				ErrorDetails: permissionError.Cause,
			}}

		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Record the failure.
		common.LogSafeError(ctx, "Post resource-filter failed", httpErr)
		return map[string]interfaces.PermissionResourceOps{}, httpErr
	}

	if result == nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		// Record the missing-model failure.
		otellog.LogWarn(ctx, "Http response body is null")
		return map[string]interfaces.PermissionResourceOps{}, nil
	}

	allowOps := []struct {
		ResourceID string   `json:"id"`
		Operations []string `json:"allow_operation,omitempty"`
	}{}
	// Process the response result.
	if err := sonic.Unmarshal(result, &allowOps); err != nil {
		// Add trace attributes for the failure.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal resource-filter result failed")
		// Record the failure.
		common.LogSafeError(ctx, "Unmalshal resource-filter result failed", err)
		return map[string]interfaces.PermissionResourceOps{}, err
	}

	// Add trace attributes for a successful response.
	oteltrace.AddHttpAttrs4Ok(span, respCode)

	ops := map[string]interfaces.PermissionResourceOps{}
	for _, op := range allowOps {
		ops[op.ResourceID] = interfaces.PermissionResourceOps{
			ResourceID: op.ResourceID,
			Operations: op.Operations,
		}
	}
	return ops, nil
}
