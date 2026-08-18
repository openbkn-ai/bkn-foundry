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

	"vega-backend/common"
	"vega-backend/interfaces"
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
	Message     string `json:"message"`     // Incorrect description
	Description string `json:"description"` // Incorrect description
	Cause       any    `json:"cause"`       // Reason
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

// Strategic decision-making
func (pa *permissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "request policy decision")
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

	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}, "permission.resource.check", 1)

	check.Method = http.MethodGet
	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, check)
	logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Log the error.
		otellog.LogError(ctx, "Post operation-check request failed", err)

		return false, fmt.Errorf("post operation-check request failed: %v", err)
	}
	if respCode != http.StatusOK {
		// Convert to baseerror
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the error.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Log the error.
			otellog.LogError(ctx, "Unmalshal PermissionError failed", err)

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

		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Log the error.
		otellog.LogError(ctx, "Post operation-check failed", httpErr)

		return false, httpErr
	}

	if result == nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		// Log the empty response.
		otellog.LogWarn(ctx, "Http response body is null")

		return false, nil
	}

	// Process the returned result "result"
	var checkResult interfaces.PermissionCheckResult
	if err := sonic.Unmarshal(result, &checkResult); err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal operation-check result failed")
		// Log the error.
		otellog.LogError(ctx, "Unmalshal operation-check result failed", err)

		return false, err
	}

	// Add trace attributes for success.
	oteltrace.AddHttpAttrs4Ok(span, respCode)

	return checkResult.Result, nil
}

// Create a strategy
func (pa *permissionAccess) CreateResources(ctx context.Context, policies []interfaces.PermissionPolicy) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "request create decision")
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

	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}, "permission.resource.list", 1)

	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, policies)
	logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Log the error.
		otellog.LogError(ctx, "Post create policy request failed", err)

		return fmt.Errorf("post create policy request failed: %v", err)
	}
	if respCode != http.StatusNoContent {
		// Convert to baseerror
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the error.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Log the error.
			otellog.LogError(ctx, "Unmalshal PermissionError failed", err)

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

		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Log the error.
		otellog.LogError(ctx, "Post create policy failed", httpErr)

		return httpErr
	}

	// Add trace attributes for success.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// Resource deletion strategy
func (pa *permissionAccess) DeleteResources(ctx context.Context, res []interfaces.PermissionResource) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "request delete decision")
	defer span.End()

	createUrl := fmt.Sprintf("%s/policy-delete", pa.permissionUrl)

	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:            createUrl,
		HttpMethod:         http.MethodPost,
		HttpContentType:    rest.ContentTypeJson,
		HttpMethodOverride: http.MethodDelete,
	})

	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}, "permission.resource.create", 1)

	st := map[string]any{
		"method":    http.MethodDelete,
		"resources": res,
	}

	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, createUrl, headers, st)
	logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", createUrl, respCode, result, err)

	if err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Log the error.
		otellog.LogError(ctx, "Post delete policy request failed", err)

		return fmt.Errorf("post delete policy request failed: %v", err)
	}
	if respCode != http.StatusNoContent {
		// Convert to baseerror
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the error.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Log the error.
			otellog.LogError(ctx, "Unmalshal PermissionError failed", err)

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

		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Log the error.
		otellog.LogError(ctx, "Post delete policy failed", httpErr)

		return httpErr
	}

	// Add trace attributes for success.
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}

// Strategic decision-making
func (pa *permissionAccess) FilterResources(ctx context.Context,
	filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "request resource filter")
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

	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		interfaces.CONTENT_TYPE_NAME: interfaces.CONTENT_TYPE_JSON,
	}, "permission.resource.delete", 1)

	filter.Method = http.MethodGet
	respCode, result, err := pa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, filter)
	logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http Post Failed")
		// Log the error.
		otellog.LogError(ctx, "Post resource-filter request failed", err)

		return map[string]interfaces.PermissionResourceOps{}, fmt.Errorf("post resource-filter request failed: %v", err)
	}
	if respCode != http.StatusOK {
		// Convert to baseerror
		var permissionError PermissionError
		if err := sonic.Unmarshal(result, &permissionError); err != nil {
			// Add trace attributes for the error.
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal PermissionError failed")
			// Log the error.
			otellog.LogError(ctx, "Unmalshal PermissionError failed", err)

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

		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		// Log the error.
		otellog.LogError(ctx, "Post resource-filter failed", httpErr)

		return map[string]interfaces.PermissionResourceOps{}, httpErr
	}

	if result == nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		// Log the empty response.
		otellog.LogWarn(ctx, "Http response body is null")

		return map[string]interfaces.PermissionResourceOps{}, nil
	}

	allowOps := []struct {
		ResourceID string   `json:"id"`
		Operations []string `json:"allow_operation,omitempty"`
	}{}
	// Process the returned result "result"
	if err := sonic.Unmarshal(result, &allowOps); err != nil {
		// Add trace attributes for the error.
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmalshal resource-filter result failed")
		// Log the error.
		otellog.LogError(ctx, "Unmalshal resource-filter result failed", err)

		return map[string]interfaces.PermissionResourceOps{}, err
	}

	// Add trace attributes for success.
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
