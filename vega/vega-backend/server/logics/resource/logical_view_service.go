// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

var lvService interfaces.LogicalViewService

// SetLogicalViewService registers the service during application assembly.
func SetLogicalViewService(service interfaces.LogicalViewService) {
	lvService = service
}

// GetLogicalViewService returns the registered service, or nil when unavailable.
func GetLogicalViewService() interfaces.LogicalViewService {
	return lvService
}

func logicalViewServiceUnavailable(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
		WithErrorDetails("logical view service is unavailable")
}

// ValidateLogicalViewRequest validates a view request with the registered service.
func ValidateLogicalViewRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
	if lvs := GetLogicalViewService(); lvs != nil {
		return lvs.ValidateRequest(ctx, req)
	}
	return logicalViewServiceUnavailable(ctx)
}

// PrepareLogicalView prepares a view request with the registered service.
func PrepareLogicalView(ctx context.Context, req *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	if lvs := GetLogicalViewService(); lvs != nil {
		req.SourceMetadata = nil
		logicType, fields, err := lvs.Prepare(ctx, req)
		if err != nil {
			return "", nil, err
		}
		if logicType == interfaces.LogicType_Derived {
			sourceResource, ok := req.SourceMetadata["source_resource"].(map[string]any)
			if !ok || len(sourceResource) == 0 {
				return "", nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails("logical view service did not provide source_resource metadata")
			}
		}
		return logicType, fields, nil
	}
	return "", nil, logicalViewServiceUnavailable(ctx)
}

// QueryLogicalViewWithPaging queries a view with the registered service.
func QueryLogicalViewWithPaging(ctx context.Context, view *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if lvs := GetLogicalViewService(); lvs != nil {
		return lvs.QueryWithPaging(ctx, view, params)
	}
	return nil, logicalViewServiceUnavailable(ctx)
}
