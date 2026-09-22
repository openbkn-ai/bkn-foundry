// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func newSafeTestClient(t *testing.T, handler http.HandlerFunc) *safeClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newSafeClient(server.URL)
}

func TestNewSafeClientAllowsLongRunningCleanupRequests(t *testing.T) {
	client := newSafeClient("http://bkn-safe")

	assert.Equal(t, 30*time.Second, client.http.Timeout)
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

func TestSafePermissionAccessResourceParentsBatches(t *testing.T) {
	putBatchSizes := make([]int, 0, 3)
	deleteBatchSizes := make([]int, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Items []interfaces.PermissionResourceParent `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			putBatchSizes = append(putBatchSizes, len(body.Items))
		case http.MethodDelete:
			var body struct {
				ResourceIDs []string `json:"resource_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			deleteBatchSizes = append(deleteBatchSizes, len(body.ResourceIDs))
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	items := make([]interfaces.PermissionResourceParent, resourceParentBatchSize*2+1)
	ids := make([]string, len(items), len(items)+1)
	for i := range items {
		id := fmt.Sprintf("resource-%d", i)
		items[i] = interfaces.PermissionResourceParent{ResourceID: id, ParentID: "catalog-1"}
		ids[i] = id
	}
	ids = append(ids, ids[0]) // Duplicate IDs must not consume another batch slot.

	access := NewPermissionAccess(&common.AppSetting{BknSafeURL: server.URL})
	require.NoError(t, access.UpsertResourceParents(context.Background(), "resource", "catalog", items))
	require.NoError(t, access.DeleteResourceParents(context.Background(), "resource", ids))

	assert.Equal(t, []int{resourceParentBatchSize, resourceParentBatchSize, 1}, putBatchSizes)
	assert.Equal(t, []int{resourceParentBatchSize, resourceParentBatchSize, 1}, deleteBatchSizes)
}
