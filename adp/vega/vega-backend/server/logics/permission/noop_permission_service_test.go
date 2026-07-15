// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestNoopPermissionCheckPermission(t *testing.T) {
	t.Run("noop permission check permission", func(t *testing.T) {
		svc := NewNoopPermissionService(nil)
		err := svc.CheckPermission(context.Background(), interfaces.PermissionResource{
			Type: "catalog",
			ID:   "all",
		}, []string{"create"})
		require.NoError(t, err)
	})
}

func TestNoopPermissionCreateResources(t *testing.T) {
	t.Run("noop permission create resources", func(t *testing.T) {
		svc := NewNoopPermissionService(nil)
		err := svc.CreateResources(context.Background(), []interfaces.PermissionResource{
			{ID: "r1", Type: "catalog", Name: "test"},
		}, []string{"view", "modify"})
		require.NoError(t, err)
	})
}

func TestNoopPermissionDeleteResources(t *testing.T) {
	t.Run("noop permission delete resources", func(t *testing.T) {
		svc := NewNoopPermissionService(nil)
		err := svc.DeleteResources(context.Background(), "catalog", []string{"r1", "r2"})
		require.NoError(t, err)
	})
}

func TestNoopPermissionFilterResources(t *testing.T) {
	t.Run("noop permission filter resources", func(t *testing.T) {
		svc := NewNoopPermissionService(nil)
		ids := []string{"r1", "r2", "r3"}
		ops := []string{"view_detail", "modify"}

		result, err := svc.FilterResources(context.Background(), "catalog",
			ids, ops, true, interfaces.COMMON_OPERATIONS)
		require.NoError(t, err)
		require.Len(t, result, 3)
		for _, id := range ids {
			r, ok := result[id]
			require.True(t, ok)
			assert.Equal(t, id, r.ResourceID)
			assert.Equal(t, interfaces.COMMON_OPERATIONS, r.Operations)
		}
	})
}

func TestNoopPermissionUpdateResource(t *testing.T) {
	t.Run("noop permission update resource", func(t *testing.T) {
		svc := NewNoopPermissionService(nil)
		err := svc.UpdateResource(context.Background(), interfaces.PermissionResource{
			ID:   "r1",
			Type: "catalog",
			Name: "updated",
		})
		require.NoError(t, err)
	})
}
