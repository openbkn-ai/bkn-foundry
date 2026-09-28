// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ResourceDataService defines resource data query behavior.
//
//go:generate mockgen -source ../interfaces/resource_data_service.go -destination ../interfaces/mock/mock_resource_data_service.go
type ResourceDataService interface {
	// QueryWithPaging queries resource data and returns cursor paging state when supported.
	QueryWithPaging(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*ResourceDataQueryResult, error)
	// QuerySourcePage executes one physical resource page after the enclosing view has been authorized.
	// It does not check permissions or manage cursor sessions.
	QuerySourcePage(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) ([]map[string]any, int64, error)
}
