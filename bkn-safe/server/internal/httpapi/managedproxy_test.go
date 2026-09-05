// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestManagedProxyAccountLifecycleAPI(t *testing.T) {
	r, _, _ := newTestServer(t)
	body := map[string]any{
		"managed_resource_type": managedproxy.ResourceKnowledgeNetwork,
		"managed_resource_id":   "kn-api",
		"name":                  "API proxy",
	}
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, w.Body.String())
	}
	var account managedproxy.Account
	if err := json.Unmarshal(w.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if account.ProxyAccountID == "" || account.LoginEnabled || account.CredentialIssuanceEnabled {
		t.Fatalf("create response = %+v", account)
	}

	w = do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", body)
	if w.Code != http.StatusOK {
		t.Fatalf("replay create = %d body=%s", w.Code, w.Body.String())
	}
	var replay managedproxy.Account
	_ = json.Unmarshal(w.Body.Bytes(), &replay)
	if replay.ProxyAccountID != account.ProxyAccountID {
		t.Fatalf("replay id = %q, want %q", replay.ProxyAccountID, account.ProxyAccountID)
	}

	w = do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts/"+account.ProxyAccountID+"/disable", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("disable = %d body=%s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &account)
	if account.Enabled || account.LifecycleStatus != managedproxy.StatusDisabling {
		t.Fatalf("disabled response = %+v", account)
	}

	w = do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts/"+account.ProxyAccountID+"/archive", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("archive = %d body=%s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &account)
	if account.Enabled || account.LifecycleStatus != managedproxy.StatusArchived {
		t.Fatalf("archived response = %+v", account)
	}
}

func TestManagedProxyStatusControlsAuthorizationDecisions(t *testing.T) {
	r, enforcer, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", map[string]any{
		"managed_resource_type": managedproxy.ResourceKnowledgeNetwork,
		"managed_resource_id":   "kn-pep",
	})
	var account managedproxy.Account
	_ = json.Unmarshal(w.Body.Bytes(), &account)
	if err := enforcer.GrantObjectPermission(account.ProxyAccountID, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	check := map[string]any{
		"accessor_id": account.ProxyAccountID,
		"resource":    map[string]any{"type": "resource", "id": "r-1"},
		"operation":   "query_data",
	}
	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", check)
	var decision struct {
		Allowed bool `json:"allowed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &decision)
	if !decision.Allowed {
		t.Fatalf("active proxy decision = %s", w.Body.String())
	}

	do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts/"+account.ProxyAccountID+"/disable", nil)
	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/check", check)
	_ = json.Unmarshal(w.Body.Bytes(), &decision)
	if decision.Allowed {
		t.Fatalf("disabled proxy remained allowed: %s", w.Body.String())
	}
}

func TestManagedProxyAPIRejectsUnsupportedOwnerType(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", map[string]any{
		"managed_resource_type": "tool_box",
		"managed_resource_id":   "box-1",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create unsupported = %d body=%s", w.Code, w.Body.String())
	}
}

func TestGenericPolicyEndpointCannotGrantManagedProxy(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/safe/in/v1/managed-proxy-accounts", map[string]any{
		"managed_resource_type": managedproxy.ResourceKnowledgeNetwork,
		"managed_resource_id":   "kn-policy",
	})
	var account managedproxy.Account
	_ = json.Unmarshal(w.Body.Bytes(), &account)

	w = do(t, r, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": account.ProxyAccountID,
		"resource":    map[string]any{"type": "resource", "id": "r-1"},
		"operations":  []string{"query_data"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("generic proxy grant = %d body=%s", w.Code, w.Body.String())
	}
}

func TestManagedProxyIsNotAGenericGranteeOrRoleSubject(t *testing.T) {
	_, _, db := newTestServer(t)
	account, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-subject",
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if ok, err := isUserAccessor(c, db, account.ProxyAccountID); err != nil || ok {
		t.Fatalf("isUserAccessor(proxy) = (%v, %v), want false", ok, err)
	}
	if ok, err := accessorExists(c, db, account.ProxyAccountID); err != nil || ok {
		t.Fatalf("accessorExists(proxy) = (%v, %v), want false", ok, err)
	}
}

func TestGenericRevokeAndRoleUnbindCannotMutateManagedProxy(t *testing.T) {
	r, enforcer, db, _ := newAdminServer(t)
	account, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-protected-writes",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedCatalogOps(t, db, "resource", "query_data")
	if err := enforcer.GrantObjectPermission(account.ProxyAccountID, "resource", "r-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Role{ID: "proxy-role", Name: "proxy-role", Source: model.RoleSourceCustom}).Error; err != nil {
		t.Fatal(err)
	}
	if err := enforcer.AssignRole(account.ProxyAccountID, "proxy-role"); err != nil {
		t.Fatal(err)
	}

	w := adminReq(t, r, http.MethodDelete, objectGrantsPath, gin.H{
		"accessor_id": account.ProxyAccountID,
		"resource":    gin.H{"type": "resource", "id": "r-1"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("generic revoke = %d body=%s, want 403", w.Code, w.Body.String())
	}
	allowed, err := enforcer.Check(account.ProxyAccountID, "resource", "r-1", "query_data")
	if err != nil || !allowed {
		t.Fatalf("generic revoke changed proxy permission: allowed=%v err=%v", allowed, err)
	}

	w = adminReq(t, r, http.MethodDelete, "/api/safe/v1/admin/role-bindings", gin.H{
		"accessor_id": account.ProxyAccountID,
		"role_id":     "proxy-role",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("generic role unbind = %d body=%s, want 403", w.Code, w.Body.String())
	}
	roles, err := enforcer.RolesForAccessor(account.ProxyAccountID)
	if err != nil || len(roles) != 1 || roles[0] != "proxy-role" {
		t.Fatalf("generic unbind changed proxy roles: roles=%v err=%v", roles, err)
	}
}

func TestManagedProxyCannotUseSelfServiceCredentialEndpoints(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	account, _, err := managedproxy.New(db).Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-self-credentials",
	})
	if err != nil {
		t.Fatal(err)
	}

	w := tokReq(t, r, http.MethodPost, "/api/safe/v1/me/api-keys", gin.H{
		"name": "forbidden",
	}, account.ProxyAccountID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("managed proxy issue key = %d body=%s, want 403", w.Code, w.Body.String())
	}
	w = tokReq(t, r, http.MethodPost, "/api/safe/v1/me/api-keys/missing/regenerate", nil, account.ProxyAccountID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("managed proxy regenerate key = %d body=%s, want 403", w.Code, w.Body.String())
	}
}
