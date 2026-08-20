// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package connector_type

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
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

func mockConnectorAvailability(t *testing.T, service *connectorTypeService, availableByType map[string]bool) {
	t.Helper()

	connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
	connectorFactory.EXPECT().IsConnectorAvailable(gomock.Any()).AnyTimes().
		DoAndReturn(func(tp string) bool { return availableByType[tp] })
	service.cf = connectorFactory
}

func TestConnectorTypeServiceRegister(t *testing.T) {
	t.Run("persists validated connector definition before enabling it", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		request := &interfaces.ConnectorTypeReq{
			Type:        "localdb",
			Name:        "Local DB",
			Description: "Local database",
			Mode:        interfaces.ConnectorModeLocal,
			Category:    interfaces.ConnectorCategoryTable,
			Enabled:     true,
		}
		events := make([]string, 0, 3)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		connectorFactory.EXPECT().ValidateConnectorTypeRegistration(gomock.Any()).
			DoAndReturn(func(got *interfaces.ConnectorType) error {
				events = append(events, "validate")
				assert.Nil(t, got.FieldConfig)
				return nil
			})
		connectorFactory.EXPECT().RegisterConnector(gomock.Any(), request.Type, gomock.Any()).
			DoAndReturn(func(_ context.Context, connectorType string, got *interfaces.ConnectorType) error {
				events = append(events, "register")
				assert.Equal(t, request.Type, connectorType)
				assert.Equal(t, request.Name, got.Name)
				return nil
			})
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
			ID:   interfaces.RESOURCE_ID_ALL,
		}, []string{interfaces.OPERATION_TYPE_CREATE}).Return(nil)
		cta.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, got *interfaces.ConnectorType) error {
			events = append(events, "create")
			assert.Equal(t, request.Type, got.Type)
			return nil
		})

		err := service.Register(context.Background(), request)

		require.NoError(t, err)
		assert.Equal(t, []string{"validate", "create", "register"}, events)
	})

	t.Run("rejects unavailable local connector before database write", func(t *testing.T) {
		service, _, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		connectorFactory.EXPECT().ValidateConnectorTypeRegistration(gomock.Any()).
			Return(errors.New("local connector is unavailable"))
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := service.Register(context.Background(), &interfaces.ConnectorTypeReq{
			Type: "future-local", Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "VegaBackend.ConnectorType.BadRequest")
	})

	t.Run("does not enable connector when database write fails", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		connectorFactory.EXPECT().ValidateConnectorTypeRegistration(gomock.Any()).Return(nil)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cta.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("database unavailable"))

		err := service.Register(context.Background(), &interfaces.ConnectorTypeReq{
			Type: "localdb", Mode: interfaces.ConnectorModeLocal,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "database unavailable")
	})
}

func TestConnectorTypeServiceGetByType(t *testing.T) {
	t.Run("returns connector type with allowed operations", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorType := &interfaces.ConnectorType{Type: "remote-api", Name: "Remote API"}
		mockConnectorAvailability(t, service, map[string]bool{"remote-api": true})

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
		service.cf.(*vmock.MockConnectorFactory).EXPECT().
			GetConnectorFieldConfig(gomock.Any(), connectorType).
			Return(map[string]interfaces.ConnectorFieldConfig{"host": {Type: "string", Required: true}}, nil)

		got, err := service.GetByType(context.Background(), "remote-api")

		require.NoError(t, err)
		require.Same(t, connectorType, got)
		assert.True(t, got.Available)
		assert.Equal(t, map[string]interfaces.ConnectorFieldConfig{"host": {Type: "string", Required: true}}, got.FieldConfig)
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

	t.Run("returns unavailable connector metadata without runtime field config", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorType := &interfaces.ConnectorType{Type: "sqlserver", Name: "SQL Server"}
		mockConnectorAvailability(t, service, map[string]bool{"sqlserver": false})

		cta.EXPECT().GetByType(gomock.Any(), "sqlserver").Return(connectorType, nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"sqlserver": {Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)

		got, err := service.GetByType(context.Background(), "sqlserver")

		require.NoError(t, err)
		assert.Same(t, connectorType, got)
		assert.False(t, got.Available)
		assert.Nil(t, got.FieldConfig)
	})

	t.Run("returns a dedicated error when runtime field config is unavailable", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorType := &interfaces.ConnectorType{Type: "remote-api", Name: "Remote API"}
		mockConnectorAvailability(t, service, map[string]bool{"remote-api": true})

		cta.EXPECT().GetByType(gomock.Any(), "remote-api").Return(connectorType, nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"remote-api": {Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)
		service.cf.(*vmock.MockConnectorFactory).EXPECT().
			GetConnectorFieldConfig(gomock.Any(), connectorType).
			Return(nil, errors.New("field config is unavailable"))

		got, err := service.GetByType(context.Background(), "remote-api")

		require.Nil(t, got)
		require.Error(t, err)
		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusServiceUnavailable, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_ConnectorType_FieldConfigUnavailable, httpErr.BaseError.ErrorCode)
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
		mockConnectorAvailability(t, service, map[string]bool{"a": true, "c": true})
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
		mockConnectorAvailability(t, service, map[string]bool{"a": true, "b": true})
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
		assert.True(t, got[0].Available)
		assert.True(t, got[1].Available)
	})

	t.Run("filters by runtime availability before pagination", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		available := true
		params := interfaces.ConnectorTypesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 1},
			Available:             &available,
		}
		types := []*interfaces.ConnectorType{
			{Type: "a"},
			{Type: "b"},
			{Type: "c"},
		}
		mockConnectorAvailability(t, service, map[string]bool{"b": true, "c": true})

		cta.EXPECT().List(gomock.Any(), params).Return(types, int64(len(types)), nil)
		ps.EXPECT().
			FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"a": {ResourceID: "a"},
				"b": {ResourceID: "b"},
				"c": {ResourceID: "c"},
			}, nil)

		got, total, err := service.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		require.Len(t, got, 1)
		assert.Equal(t, "c", got[0].Type)
		assert.True(t, got[0].Available)
	})

	t.Run("offset outside authorized list returns empty page with total", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		mockConnectorAvailability(t, service, map[string]bool{"a": true})
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

func TestConnectorTypeServiceUpdate(t *testing.T) {
	t.Run("uses resolved code definition when updating local connector", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		current := &interfaces.ConnectorType{
			Type: "localdb", Name: "Local DB", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
		}
		request := &interfaces.ConnectorTypeReq{
			Type: "localdb", Name: "Renamed Local DB", Mode: interfaces.ConnectorModeLocal,
			Category: interfaces.ConnectorCategoryTable,
			Enabled:  true,
		}
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory.EXPECT().ValidateConnectorTypeRegistration(gomock.Any()).
			DoAndReturn(func(candidate *interfaces.ConnectorType) error {
				assert.Nil(t, candidate.FieldConfig)
				assert.Equal(t, "Renamed Local DB", candidate.Name)
				return nil
			})
		cta.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory.EXPECT().RegisterConnector(gomock.Any(), "localdb", gomock.Any()).Return(nil)
		ps.EXPECT().UpdateResource(gomock.Any(), interfaces.PermissionResource{
			ID: "localdb", Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE, Name: "Renamed Local DB",
		}).Return(nil)

		err := service.Update(context.Background(), current, request)

		require.NoError(t, err)
	})
}

func TestConnectorTypeServiceCheckExistByType(t *testing.T) {
	t.Run("checks existence by type", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().GetByType(gomock.Any(), "exists").
			Return(&interfaces.ConnectorType{Type: "exists"}, nil)

		exists, err := service.CheckExistByType(context.Background(), "exists")

		require.NoError(t, err)
		assert.True(t, exists)
	})

}

func TestConnectorTypeServiceCheckExistByName(t *testing.T) {
	t.Run("checks absence by name", func(t *testing.T) {
		service, cta, _ := newTestConnectorTypeService(t)
		cta.EXPECT().GetByName(gomock.Any(), "missing").Return(nil, nil)

		exists, err := service.CheckExistByName(context.Background(), "missing")

		require.NoError(t, err)
		assert.False(t, exists)
	})

}

func TestConnectorTypeServiceDeleteByType(t *testing.T) {
	t.Run("deletes registration and permission resource", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory

		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
			ID:   "remote-api",
		}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		cta.EXPECT().DeleteByType(gomock.Any(), "remote-api").Return(nil)
		connectorFactory.EXPECT().DeleteConnector("remote-api")
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE, []string{"remote-api"}).
			Return(nil)

		err := service.DeleteByType(context.Background(), "remote-api")

		require.NoError(t, err)
	})
}

func TestConnectorTypeServiceSetEnabled(t *testing.T) {
	t.Run("set enabled checks permission and updates access", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		ps.EXPECT().
			CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_CONNECTOR_TYPE,
				ID:   "remote-api",
			}, []string{interfaces.OPERATION_TYPE_MODIFY}).
			Return(nil)
		cta.EXPECT().SetEnabled(gomock.Any(), "remote-api", true).Return(nil)
		connectorFactory.EXPECT().SetConnectorEnabled("remote-api", true)

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

	t.Run("allows updating unavailable connector during binary downgrade", func(t *testing.T) {
		service, cta, ps := newTestConnectorTypeService(t)
		connectorFactory := vmock.NewMockConnectorFactory(gomock.NewController(t))
		service.cf = connectorFactory
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cta.EXPECT().SetEnabled(gomock.Any(), "enterprise-local", false).Return(nil)
		connectorFactory.EXPECT().SetConnectorEnabled("enterprise-local", false)

		require.NoError(t, service.SetEnabled(context.Background(), "enterprise-local", false))
	})
}

func TestPaginateConnectorTypeAuthResources(t *testing.T) {
	t.Run("paginate connector type auth resources", func(t *testing.T) {
		entries := []*interfaces.AuthResourceEntry{
			{ID: "a"},
			{ID: "b"},
			{ID: "c"},
		}

		assert.Equal(t, entries, paginateConnectorTypeAuthResources(entries, 0, -1))
		assert.Equal(t, []*interfaces.AuthResourceEntry{{ID: "b"}, {ID: "c"}}, paginateConnectorTypeAuthResources(entries, 1, 10))
		assert.Empty(t, paginateConnectorTypeAuthResources(entries, -1, 10))
		assert.Empty(t, paginateConnectorTypeAuthResources(entries, 3, 10))
	})
}
