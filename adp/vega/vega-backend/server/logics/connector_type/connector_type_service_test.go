// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package connector_type

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func newTestConnectorTypeService(t *testing.T) (*connectorTypeService, *vmock.MockConnectorTypeAccess, *vmock.MockPermissionService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	cta := vmock.NewMockConnectorTypeAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)

	return &connectorTypeService{
		cta: cta,
		ps:  ps,
	}, cta, ps
}

func TestConnectorTypeServiceGetByType(t *testing.T) {
	t.Run("returns connector type with allowed operations", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorType := &interfaces.ConnectorType{Type: "remote-api", Name: "Remote API"}

		cta.EXPECT().GetByType(gomock.Any(), "remote-api").Return(connectorType, nil)
		ps.EXPECT().
			FilterResources(
				gomock.Any(),
				interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
				[]string{"remote-api"},
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
				true,
				interfaces.COMMON_OPERATIONS,
			).
			Return(map[string]interfaces.PermissionResourceOps{
				"remote-api": {
					ResourceID: "remote-api",
					Operations: []string{
						interfaces.OPERATION_TYPE_VIEW_DETAIL,
						interfaces.OPERATION_TYPE_MODIFY,
					},
				},
			}, nil)

		got, err := service.GetByType(context.Background(), "remote-api")

		require.NoError(t, err)
		require.Same(t, connectorType, got)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY}, got.Operations)
	})

	t.Run("returns not found when access returns nil", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().GetByType(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.GetByType(context.Background(), "missing")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "VegaBackend.ConnectorType.NotFound")
	})

	t.Run("returns forbidden when permission filter excludes resource", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		cta.EXPECT().GetByType(gomock.Any(), "remote-api").
			Return(&interfaces.ConnectorType{Type: "remote-api"}, nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		got, err := service.GetByType(context.Background(), "remote-api")

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "Access denied")
	})
}

func TestConnectorTypeServiceList(t *testing.T) {
	t.Run("filters by permission then paginates", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		params := interfaces.ConnectorTypesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 1},
		}
		types := []*interfaces.ConnectorType{
			{Type: "a", Name: "A"},
			{Type: "b", Name: "B"},
			{Type: "c", Name: "C"},
		}

		cta.EXPECT().List(gomock.Any(), params).Return(types, int64(len(types)), nil)
		ps.EXPECT().
			FilterResources(
				gomock.Any(),
				interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
				[]string{"a", "b", "c"},
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
				true,
				interfaces.COMMON_OPERATIONS,
			).
			Return(map[string]interfaces.PermissionResourceOps{
				"a": {ResourceID: "a", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c": {ResourceID: "c", Operations: []string{interfaces.OPERATION_TYPE_DELETE}},
			}, nil)

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		require.Len(t, got, 1)
		assert.Equal(t, "c", got[0].Type)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_DELETE}, got[0].Operations)
	})

	t.Run("limit -1 returns all authorized connector types", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		params := interfaces.ConnectorTypesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		}
		types := []*interfaces.ConnectorType{{Type: "a"}, {Type: "b"}}

		cta.EXPECT().List(gomock.Any(), params).Return(types, int64(len(types)), nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"a": {ResourceID: "a"},
				"b": {ResourceID: "b"},
			}, nil)

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		assert.Len(t, got, 2)
	})

	t.Run("offset outside authorized list returns empty page with total", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		params := interfaces.ConnectorTypesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 2, Limit: 10},
		}

		cta.EXPECT().List(gomock.Any(), params).
			Return([]*interfaces.ConnectorType{{Type: "a"}}, int64(1), nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"a": {ResourceID: "a"}}, nil)

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Empty(t, got)
	})

	t.Run("access error is wrapped", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), errors.New("db down"))

		got, total, err := service.List(context.Background(), interfaces.ConnectorTypesQueryParams{})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Zero(t, total)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestConnectorTypeServiceListAuthResources(t *testing.T) {
	t.Run("filters authorized entries and paginates", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		params := interfaces.AuthResourceQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 1},
		}
		entries := []*interfaces.AuthResourceEntry{
			{ID: "a", Name: "A"},
			nil,
			{ID: "b", Name: "B"},
			{ID: "c", Name: "C"},
		}

		cta.EXPECT().ListAuthResources(gomock.Any(), params).Return(entries, nil)
		ps.EXPECT().
			FilterResources(
				gomock.Any(),
				interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
				[]string{"a", "b", "c"},
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
				false,
				interfaces.COMMON_OPERATIONS,
			).
			Return(map[string]interfaces.PermissionResourceOps{
				"a": {ResourceID: "a"},
				"c": {ResourceID: "c"},
			}, nil)

		got, total, err := service.ListAuthResources(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		require.Len(t, got, 1)
		assert.Equal(t, "c", got[0].ID)
	})

	t.Run("empty access result short circuits permission filter", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		params := interfaces.AuthResourceQueryParams{}

		cta.EXPECT().ListAuthResources(gomock.Any(), params).Return(nil, nil)

		got, total, err := service.ListAuthResources(context.Background(), params)

		require.NoError(t, err)
		assert.Zero(t, total)
		assert.Empty(t, got)
	})
}

func TestConnectorTypeServiceExistenceAndEnabled(t *testing.T) {
	t.Run("checks existence by type", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().GetByType(gomock.Any(), "exists").
			Return(&interfaces.ConnectorType{Type: "exists"}, nil)

		exists, err := service.CheckExistByType(context.Background(), "exists")

		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("checks absence by name", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().GetByName(gomock.Any(), "missing").Return(nil, nil)

		exists, err := service.CheckExistByName(context.Background(), "missing")

		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("set enabled checks permission and updates access", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		ps.EXPECT().
			CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
				ID:   "remote-api",
			}, []string{interfaces.OPERATION_TYPE_MODIFY}).
			Return(nil)
		cta.EXPECT().SetEnabled(gomock.Any(), "remote-api", true).Return(nil)

		require.NoError(t, service.SetEnabled(context.Background(), "remote-api", true))
	})

	t.Run("set enabled wraps access error", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cta.EXPECT().SetEnabled(gomock.Any(), "remote-api", false).Return(errors.New("db down"))

		err := service.SetEnabled(context.Background(), "remote-api", false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestPaginateConnectorTypeAuthResources(t *testing.T) {
	entries := []*interfaces.AuthResourceEntry{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}

	assert.Equal(t, entries, paginateConnectorTypeAuthResources(entries, 0, -1))
	assert.Equal(t, []*interfaces.AuthResourceEntry{{ID: "b"}, {ID: "c"}}, paginateConnectorTypeAuthResources(entries, 1, 10))
	assert.Empty(t, paginateConnectorTypeAuthResources(entries, -1, 10))
	assert.Empty(t, paginateConnectorTypeAuthResources(entries, 3, 10))
}
