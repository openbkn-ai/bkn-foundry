// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

type queryViewService struct {
	resource *interfaces.Resource
	params   *interfaces.ResourceDataQueryParams
}

func (*queryViewService) ValidateRequest(context.Context, *interfaces.ResourceRequest) error {
	return nil
}

func (*queryViewService) Prepare(context.Context, *interfaces.ResourceRequest) (string, []*interfaces.Property, error) {
	return "", nil, nil
}

func (viewService *queryViewService) QueryWithPaging(_ context.Context, resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	viewService.resource = resource
	viewService.params = params
	return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": "row-1"}}, Paging: &interfaces.PagingResponse{}}, nil
}

func TestResourceDataServiceQueryWithPagingUsesLogicalViewService(t *testing.T) {
	viewService := &queryViewService{}
	previous := resourcelogic.GetLogicalViewService()
	resourcelogic.SetLogicalViewService(viewService)
	t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })

	ctrl := gomock.NewController(t)
	resources := vmock.NewMockResourceService(ctrl)
	resources.EXPECT().CheckResourcePermission(gomock.Any(), "view-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	service := &resourceDataService{rs: resources}
	view := &interfaces.Resource{
		ID: "view-1", Category: interfaces.ResourceCategoryLogicalView,
		Enabled: true, Status: interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
	}
	params := &interfaces.ResourceDataQueryParams{}

	result, err := service.QueryWithPaging(context.Background(), view, params)

	require.NoError(t, err)
	assert.Same(t, view, viewService.resource)
	assert.Same(t, params, viewService.params)
	assert.Equal(t, []map[string]any{{"id": "row-1"}}, result.Entries)
}

func TestResourceDataServiceQueryWithPagingWithoutLogicalViewService(t *testing.T) {
	previous := resourcelogic.GetLogicalViewService()
	resourcelogic.SetLogicalViewService(nil)
	t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })

	ctrl := gomock.NewController(t)
	resources := vmock.NewMockResourceService(ctrl)
	resources.EXPECT().CheckResourcePermission(gomock.Any(), "view-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	service := &resourceDataService{rs: resources}
	view := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicalView,
		Enabled: true, Status: interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}}}

	_, err := service.QueryWithPaging(context.Background(), view, &interfaces.ResourceDataQueryParams{})
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotImplemented, httpErr.HTTPCode)
}
