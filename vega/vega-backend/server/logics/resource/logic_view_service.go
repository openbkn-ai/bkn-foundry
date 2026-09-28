// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var lvService interfaces.LogicViewService

// SetLogicViewService registers the service during application assembly.
func SetLogicViewService(service interfaces.LogicViewService) {
	lvService = service
}

// GetLogicViewService returns the registered service, or nil when unavailable.
func GetLogicViewService() interfaces.LogicViewService {
	return lvService
}

func logicViewServiceUnavailable(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
		WithErrorDetails("logic view service is unavailable")
}

// ValidateLogicViewRequest validates a view request with the registered service.
func ValidateLogicViewRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
	if lvs := GetLogicViewService(); lvs != nil {
		return lvs.ValidateRequest(ctx, req)
	}
	return logicViewServiceUnavailable(ctx)
}

// PrepareLogicView prepares a view request with the registered service.
func PrepareLogicView(ctx context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	if lvs := GetLogicViewService(); lvs != nil {
		return lvs.Prepare(ctx, req)
	}
	return "", nil, logicViewServiceUnavailable(ctx)
}

// QueryLogicViewWithPaging queries a view with the registered service.
func QueryLogicViewWithPaging(ctx context.Context, view *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if lvs := GetLogicViewService(); lvs != nil {
		return lvs.QueryWithPaging(ctx, view, params)
	}
	return nil, logicViewServiceUnavailable(ctx)
}
