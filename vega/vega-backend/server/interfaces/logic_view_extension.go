// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// LogicViewExtension supplies the logic-view behavior assembled by an
// optional Vega distribution. Prepare validates the definition and returns
// the derived fields before Core persists the Resource.
type LogicViewExtension interface {
	ValidateRequest(ctx context.Context, req *ResourceRequest) error
	Prepare(ctx context.Context, req *ResourceRequest) (logicType string, schema []*Property, err error)
	QueryWithPaging(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*ResourceDataQueryResult, error)
}
