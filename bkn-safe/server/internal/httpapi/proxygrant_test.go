// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/proxygrant"
)

func TestProxyGrantSourceLifecycleAPI(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-api-grant",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedEnabledUser(t, db, "grantor-api")
	seedCatalogOps(t, db, "resource", "authorize", "query_data")
	if err := enforcer.GrantObjectPermission("grantor-api", "resource", "r-api", "authorize"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("grantor-api", "resource", "r-api", "query_data"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"proxy_account_id": proxy.ProxyAccountID,
		"grantor_id":       "grantor-api",
		"source": map[string]any{
			"resource_type": "resource", "resource_id": "r-api", "operation": "query_data",
			"source_type": "kn_proxy_binding", "source_id": "source-api", "kn_id": "kn-api-grant",
			"binding_type": "object_type", "binding_id": "ot-api",
		},
	}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, w.Body.String())
	}
	var source model.ProxyGrantSource
	if err := json.Unmarshal(w.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	if source.ID == "" || source.GrantedBy != "grantor-api" || source.LifecycleStatus != proxygrant.StatusActive {
		t.Fatalf("source = %+v", source)
	}

	w = do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources", body)
	if w.Code != http.StatusOK {
		t.Fatalf("replay = %d body=%s", w.Code, w.Body.String())
	}

	w = do(t, r, http.MethodDelete, "/api/safe/in/v1/proxy-grant-sources/"+source.ID, map[string]any{"grantor_id": "grantor-api"})
	if w.Code != http.StatusOK {
		t.Fatalf("revoke = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	if source.LifecycleStatus != proxygrant.StatusRevoked || source.RevokedAt == nil {
		t.Fatalf("revoked source = %+v", source)
	}
	allowed, err := enforcer.Check(proxy.ProxyAccountID, "resource", "r-api", "query_data")
	if err != nil || allowed {
		t.Fatalf("policy after revoke: allowed=%v err=%v", allowed, err)
	}
}

func TestProxyGrantAPIRejectsUnauthorizedGrantorAndWildcardTarget(t *testing.T) {
	r, _, db := newTestServer(t)
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-api-deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedEnabledUser(t, db, "grantor-denied")
	seedCatalogOps(t, db, "resource", "query_data")
	body := map[string]any{
		"proxy_account_id": proxy.ProxyAccountID,
		"grantor_id":       "grantor-denied",
		"source": map[string]any{
			"resource_type": "resource", "resource_id": "r-denied", "operation": "query_data",
			"source_id": "source-denied", "kn_id": "kn-api-deny",
			"binding_type": "object_type", "binding_id": "ot-denied",
		},
	}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized grant = %d body=%s", w.Code, w.Body.String())
	}

	source := body["source"].(map[string]any)
	source["resource_id"] = "*"
	w = do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wildcard grant = %d body=%s", w.Code, w.Body.String())
	}

	var count int64
	if err := db.Model(&model.ProxyGrantSource{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("source count = %d err=%v", count, err)
	}
}

func TestProxyGrantCheckAndReconcileAPI(t *testing.T) {
	r, enforcer, db := newTestServer(t)
	proxy, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-api-check",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedEnabledUser(t, db, "grantor-check")
	seedCatalogOps(t, db, "resource", "authorize", "view_detail")
	if err := enforcer.GrantObjectPermission("grantor-check", "resource", "r-check", "authorize"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("grantor-check", "resource", "r-check", "view_detail"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"proxy_account_id": proxy.ProxyAccountID,
		"grantor_id":       "grantor-check",
		"source": map[string]any{
			"resource_type": "resource", "resource_id": "r-check", "operation": "view_detail",
			"source_id": "source-check", "kn_id": "kn-api-check",
			"binding_type": "object_type", "binding_id": "ot-check",
		},
	}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources/check", body)
	if w.Code != http.StatusOK || !jsonBool(t, w.Body.Bytes(), "allowed") {
		t.Fatalf("check = %d body=%s", w.Code, w.Body.String())
	}

	w = do(t, r, http.MethodPost, "/api/safe/in/v1/proxy-grant-sources/reconcile", map[string]any{
		"proxy_account_id": proxy.ProxyAccountID,
		"requested_by":     "system:reconcile",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("reconcile = %d body=%s", w.Code, w.Body.String())
	}
}

func jsonBool(t *testing.T, body []byte, field string) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	result, _ := value[field].(bool)
	return result
}
