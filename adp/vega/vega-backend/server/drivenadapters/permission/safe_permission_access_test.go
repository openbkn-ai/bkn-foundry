// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func newSafeTestClient(t *testing.T, handler http.HandlerFunc) *safeClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newSafeClient(server.URL)
}

func TestSafePermissionAccessResourceParents(t *testing.T) {
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"items":[{"resource_id":"resource-1","parent_id":"catalog-1"}],"total":1}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	access := NewPermissionAccess(&common.AppSetting{BknSafeURL: server.URL})
	require.NoError(t, access.UpsertResourceParents(context.Background(), "resource", "catalog", []interfaces.PermissionResourceParent{{
		ResourceID: "resource-1", ParentID: "catalog-1",
	}}))
	require.NoError(t, access.DeleteResourceParents(context.Background(), "resource", []string{"resource-1"}))
	parents, err := access.GetResourceParents(context.Background(), "resource", []string{"resource-1"})
	require.NoError(t, err)
	assert.Equal(t, interfaces.PermissionResourceParent{ResourceID: "resource-1", ParentID: "catalog-1"}, parents["resource-1"])
	assert.Equal(t, []string{
		"PUT /api/safe/v1/authz/resource-parents",
		"DELETE /api/safe/v1/authz/resource-parents",
		"GET /api/safe/v1/authz/resource-parents",
	}, requests)
}
