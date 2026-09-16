// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestCheckTaskPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca := mock_interfaces.NewMockCatalogAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	cs := &catalogService{ca: ca, ps: ps}
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "cat-1",
	}, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}).Return(nil)
	ca.EXPECT().GetByID(gomock.Any(), "cat-1").Return(&interfaces.Catalog{ID: "cat-1"}, nil)
	require.NoError(t, cs.CheckTaskPermission(context.Background(), "cat-1", interfaces.OPERATION_TYPE_TASK_MANAGE))
}

func TestListPermittedCatalogIDs(t *testing.T) {
	t.Run("pulls catalog IDs and filters each through bkn-safe", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return([]interfaces.CatalogPermissionRef{{CatalogID: "cat-1"}, {CatalogID: "cat-2"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"cat-1", "cat-2"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"cat-1": {ResourceID: "cat-1"}}, nil)

		ids, _, err := cs.ListPermittedCatalogIDs(context.Background(),
			[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true, interfaces.CatalogsQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"cat-1"}, ids)
	})

	t.Run("一个都看不见:空集合而不是通配", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return([]interfaces.CatalogPermissionRef{{CatalogID: "cat-1"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		ids, _, err := cs.ListPermittedCatalogIDs(context.Background(),
			[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true, interfaces.CatalogsQueryParams{})
		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("passes allow operation through to bkn-safe", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).
			Return([]interfaces.CatalogPermissionRef{{CatalogID: "cat-1"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"cat-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, false,
			interfaces.COMMON_OPERATIONS).
			Return(map[string]interfaces.PermissionResourceOps{"cat-1": {ResourceID: "cat-1"}}, nil)

		ids, _, err := cs.ListPermittedCatalogIDs(context.Background(),
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, false, interfaces.CatalogsQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"cat-1"}, ids)
	})
}
