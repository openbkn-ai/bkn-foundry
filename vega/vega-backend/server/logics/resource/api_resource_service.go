// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package resource

import (
	"context"
	"net/http"

	"github.com/mohae/deepcopy"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var apiService interfaces.APIResourceService

// SetAPIResourceService 在应用装配时注册 API Resource 扩展。
func SetAPIResourceService(service interfaces.APIResourceService) {
	apiService = service
}

// GetAPIResourceService 返回已注册扩展；未启用时返回 nil。
func GetAPIResourceService() interfaces.APIResourceService {
	return apiService
}

func apiResourceServiceUnavailable(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusNotImplemented, httperrors.PublicError_NotImplemented).
		WithErrorDetails("API resource service is unavailable")
}

// PrepareAPIResourceCreate 将 API 保存准备委托给扩展。
func PrepareAPIResourceCreate(ctx context.Context, candidate *interfaces.Resource, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	if service := GetAPIResourceService(); service != nil {
		return service.PrepareCreate(ctx, candidate, req)
	}
	return nil, apiResourceServiceUnavailable(ctx)
}

// PrepareAPIResourceUpdate 将 API 更新准备委托给扩展。
func PrepareAPIResourceUpdate(ctx context.Context, current *interfaces.Resource, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
	if service := GetAPIResourceService(); service != nil {
		return service.PrepareUpdate(ctx, current, req)
	}
	return nil, apiResourceServiceUnavailable(ctx)
}

// QueryAPIResource 将一次无会话 API 查询委托给扩展。
func QueryAPIResource(ctx context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if service := GetAPIResourceService(); service != nil {
		return service.Query(ctx, resource, params)
	}
	return nil, apiResourceServiceUnavailable(ctx)
}

// redactAPIResource 保留敏感声明，但不向公开调用方暴露其值或存储定义。
func redactAPIResource(resource *interfaces.Resource) *interfaces.Resource {
	if resource == nil || resource.Category != interfaces.ResourceCategoryAPI {
		return resource
	}
	redacted := deepcopy.Copy(resource).(*interfaces.Resource)
	if redacted.APIDefinition != nil {
		for name, variable := range redacted.APIDefinition.Variables {
			if variable.Sensitive {
				variable.Value = nil
				redacted.APIDefinition.Variables[name] = variable
			}
		}
	}
	redacted.LogicDefinitionStr = ""
	return redacted
}
