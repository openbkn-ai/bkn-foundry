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

func TestNoopPermission_CheckPermission(t *testing.T) {
	svc := NewNoopPermissionService(nil)
	err := svc.CheckPermission(context.Background(), interfaces.PermissionResource{
		Type: "catalog",
		ID:   "all",
	}, []string{"create"})
	require.NoError(t, err)
}

func TestNoopPermission_CreateResources(t *testing.T) {
	svc := NewNoopPermissionService(nil)
	err := svc.CreateResources(context.Background(), []interfaces.PermissionResource{
		{ID: "r1", Type: "catalog", Name: "test"},
	}, []string{"view", "modify"})
	require.NoError(t, err)
}

func TestNoopPermission_DeleteResources(t *testing.T) {
	svc := NewNoopPermissionService(nil)
	err := svc.DeleteResources(context.Background(), "catalog", []string{"r1", "r2"})
	require.NoError(t, err)
}

func TestNoopPermission_FilterResources(t *testing.T) {
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
}

func TestNoopPermission_UpdateResource(t *testing.T) {
	svc := NewNoopPermissionService(nil)
	err := svc.UpdateResource(context.Background(), interfaces.PermissionResource{
		ID:   "r1",
		Type: "catalog",
		Name: "updated",
	})
	require.NoError(t, err)
}
