// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestProxyAuthorizationAccessGetManagedProxy(t *testing.T) {
	t.Run("loads current managed proxy", func(t *testing.T) {
		access := &proxyAuthorizationAccess{safe: newSafeTestClient(t, func(w http.ResponseWriter, req *http.Request) {
			assert.Equal(t, http.MethodGet, req.Method)
			assert.Equal(t, "/api/safe/in/v1/managed-proxy-accounts/proxy-1", req.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"proxy_account_id":"proxy-1","account_type":"app","managed_by":"bkn","managed_resource_type":"knowledge_network","managed_resource_id":"kn-1","lifecycle_status":"active","enabled":true,"version":3}`)
		})}

		account, err := access.GetManagedProxy(context.Background(), "proxy-1")

		require.NoError(t, err)
		require.NotNil(t, account)
		assert.Equal(t, "proxy-1", account.ProxyAccountID)
		assert.Equal(t, uint64(3), account.Version)
		assert.True(t, account.Enabled)
	})

	t.Run("returns no account for not found", func(t *testing.T) {
		access := &proxyAuthorizationAccess{safe: newSafeTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})}

		account, err := access.GetManagedProxy(context.Background(), "missing")

		require.NoError(t, err)
		assert.Nil(t, account)
	})

	t.Run("returns downstream failure", func(t *testing.T) {
		access := &proxyAuthorizationAccess{safe: newSafeTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})}

		account, err := access.GetManagedProxy(context.Background(), "proxy-1")

		require.Error(t, err)
		assert.Nil(t, account)
	})
}

func TestProxyAuthorizationAccessCheckPermission(t *testing.T) {
	access := &proxyAuthorizationAccess{safe: newSafeTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "/api/safe/v1/authz/check", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"accessor_id":"proxy-1","resource":{"type":"resource","id":"resource-1"},"operation":"query_data"}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"allowed":true}`)
	})}

	allowed, err := access.CheckPermission(context.Background(), "proxy-1", interfaces.ProxyTargetTypeResource, "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA)

	require.NoError(t, err)
	assert.True(t, allowed)
}
