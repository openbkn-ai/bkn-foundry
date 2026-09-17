// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestCheckCatalogPermission(t *testing.T) {
	resource := interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "cat-1"}
	ops := []string{interfaces.OPERATION_TYPE_TASK_MANAGE}

	t.Run("allowed without catalog lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ps: ps}
		ps.EXPECT().CheckPermission(gomock.Any(), resource, ops).Return(nil)

		allowed, catalog, err := cs.CheckCatalogPermission(context.Background(), "cat-1", ops, false)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Nil(t, catalog)
	})

	t.Run("permission refusal is a negative decision", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ps: ps}
		ps.EXPECT().CheckPermission(gomock.Any(), resource, ops).
			Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))

		allowed, catalog, err := cs.CheckCatalogPermission(context.Background(), "cat-1", ops, true)
		require.NoError(t, err)
		assert.False(t, allowed)
		assert.Nil(t, catalog)
	})

	t.Run("allowed with catalog lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}
		want := &interfaces.Catalog{ID: "cat-1"}
		ps.EXPECT().CheckPermission(gomock.Any(), resource, ops).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "cat-1").Return(want, nil)

		allowed, catalog, err := cs.CheckCatalogPermission(context.Background(), "cat-1", ops, true)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, want, catalog)
	})

	t.Run("internal catalog remains hidden from non-admin", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}
		ps.EXPECT().CheckPermission(gomock.Any(), resource, ops).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "cat-1").Return(&interfaces.Catalog{ID: "cat-1", Internal: true}, nil)

		allowed, catalog, err := cs.CheckCatalogPermission(context.Background(), "cat-1", ops, true)
		require.Error(t, err)
		assert.False(t, allowed)
		assert.Nil(t, catalog)
	})
}

func TestListPermittedCatalogIDs(t *testing.T) {
	t.Run("pulls catalog IDs and filters each through bkn-safe", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return([]interfaces.CatalogPermissionRef{{CatalogID: "cat-1"}, {CatalogID: "cat-2"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"cat-1", "cat-2"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL, true).
			Return(map[string]interfaces.PermissionResourceOps{"cat-1": {ResourceID: "cat-1"}}, nil)

		ids, _, err := cs.ListPermittedCatalogIDs(context.Background(),
			[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, interfaces.VISIBILITY_MATCH_ALL, true, interfaces.CatalogsQueryParams{})
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
			[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, interfaces.VISIBILITY_MATCH_ALL, true, interfaces.CatalogsQueryParams{})
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
			[]string{"cat-1"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL, false,
		).
			Return(map[string]interfaces.PermissionResourceOps{"cat-1": {ResourceID: "cat-1"}}, nil)

		ids, _, err := cs.ListPermittedCatalogIDs(context.Background(),
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, interfaces.VISIBILITY_MATCH_ALL, false, interfaces.CatalogsQueryParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"cat-1"}, ids)
	})
}
