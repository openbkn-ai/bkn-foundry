package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/model"
)

const (
	meKeys    = "/api/safe/v1/me/api-keys"
	adminKeys = "/api/safe/v1/admin/api-keys"
	introsURL = "/api/safe/v1/api-keys/introspect"
)

// issued is the subset of the issue response the tests assert on.
type issued struct {
	ID        string  `json:"id"`
	KeyID     string  `json:"key_id"`
	Name      string  `json:"name"`
	Key       string  `json:"key"`        // plaintext, returned exactly once
	ExpiresAt *string `json:"expires_at"` // null = never expires
}

// TestAPIKeyIssueListRevoke covers the self-service happy path: issue returns a
// one-time plaintext key; list never leaks a secret; revoke removes it.
func TestAPIKeyIssueListRevoke(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-key", Account: "uk", Enabled: true})

	// auth gate
	if w := tokReq(t, r, http.MethodGet, meKeys, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", w.Code)
	}

	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "k1"}, "u-key")
	if w.Code != http.StatusCreated {
		t.Fatalf("issue: want 201, got %d (%s)", w.Code, w.Body.String())
	}
	var iss issued
	if err := json.Unmarshal(w.Body.Bytes(), &iss); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(iss.Key, auth.KeyPrefix) {
		t.Errorf("plaintext key %q must start with %q", iss.Key, auth.KeyPrefix)
	}
	if iss.KeyID == "" || iss.ID == "" || iss.Name != "k1" {
		t.Errorf("issue body = %+v", iss)
	}
	if iss.ExpiresAt == nil {
		t.Error("default issue must have a non-null expires_at (1y)")
	}

	// list: exactly one, and the secret/plaintext must NOT be present
	w = tokReq(t, r, http.MethodGet, meKeys, nil, "u-key")
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret") || strings.Contains(body, iss.Key) {
		t.Errorf("list leaks secret: %s", body)
	}
	var listResp struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Keys) != 1 || listResp.Keys[0]["name"] != "k1" {
		t.Fatalf("list = %+v", listResp.Keys)
	}
	if _, hasKey := listResp.Keys[0]["key"]; hasKey {
		t.Error("list must not contain the plaintext key field")
	}

	// revoke
	if w := tokReq(t, r, http.MethodDelete, meKeys+"/"+iss.ID, nil, "u-key"); w.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d (%s)", w.Code, w.Body.String())
	}
	// revoke again -> 404
	if w := tokReq(t, r, http.MethodDelete, meKeys+"/"+iss.ID, nil, "u-key"); w.Code != http.StatusNotFound {
		t.Errorf("re-revoke: want 404, got %d", w.Code)
	}
	// list empty
	w = tokReq(t, r, http.MethodGet, meKeys, nil, "u-key")
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Keys) != 0 {
		t.Errorf("after revoke list = %+v, want empty", listResp.Keys)
	}
}

// TestAPIKeyDuplicateName: issuing a second key with the same name -> 409.
func TestAPIKeyDuplicateName(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-dup", Account: "dup", Enabled: true})

	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "same"}, "u-dup"); w.Code != http.StatusCreated {
		t.Fatalf("first: want 201, got %d", w.Code)
	}
	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "same"}, "u-dup"); w.Code != http.StatusConflict {
		t.Errorf("dup name: want 409, got %d (%s)", w.Code, w.Body.String())
	}
	// different owner, same name -> ok
	db.Create(&model.User{ID: "u-dup2", Account: "dup2", Enabled: true})
	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "same"}, "u-dup2"); w.Code != http.StatusCreated {
		t.Errorf("other owner same name: want 201, got %d", w.Code)
	}
}

// TestAPIKeyExpiryRules covers resolveExpiry via the handler: default 1y,
// never_expire -> null, explicit future, and the 400s.
func TestAPIKeyExpiryRules(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-exp", Account: "ue", Enabled: true})

	decode := func(w *httptest.ResponseRecorder) issued {
		t.Helper()
		var i issued
		if err := json.Unmarshal(w.Body.Bytes(), &i); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
		return i
	}

	// default -> ~1 year out
	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "d"}, "u-exp")
	i := decode(w)
	if i.ExpiresAt == nil {
		t.Fatal("default expires_at must be set")
	}
	exp, err := time.Parse(time.RFC3339, *i.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	if d := time.Until(exp); d < 360*24*time.Hour || d > 366*24*time.Hour {
		t.Errorf("default expiry %v not ~1y", d)
	}

	// never_expire -> null
	w = tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "n", "never_expire": true}, "u-exp")
	if decode(w).ExpiresAt != nil {
		t.Error("never_expire must yield null expires_at")
	}

	// explicit future -> honored
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	w = tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "f", "expires_at": future}, "u-exp")
	if got := decode(w).ExpiresAt; got == nil {
		t.Error("explicit expires_at dropped")
	}

	// past -> 400
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "p", "expires_at": past}, "u-exp"); w.Code != http.StatusBadRequest {
		t.Errorf("past expires_at: want 400, got %d", w.Code)
	}
	// bad format -> 400
	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "b", "expires_at": "not-a-date"}, "u-exp"); w.Code != http.StatusBadRequest {
		t.Errorf("bad expires_at: want 400, got %d", w.Code)
	}
	// missing name -> 400 (binding required)
	if w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{}, "u-exp"); w.Code != http.StatusBadRequest {
		t.Errorf("missing name: want 400, got %d", w.Code)
	}
}

// TestAPIKeyOwnershipIsolation: a user cannot see or revoke another user's keys.
func TestAPIKeyOwnershipIsolation(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "owner-a", Account: "a", Enabled: true})
	db.Create(&model.User{ID: "owner-b", Account: "b", Enabled: true})

	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "a-key"}, "owner-a")
	var ia issued
	_ = json.Unmarshal(w.Body.Bytes(), &ia)

	// B cannot delete A's key
	if w := tokReq(t, r, http.MethodDelete, meKeys+"/"+ia.ID, nil, "owner-b"); w.Code != http.StatusNotFound {
		t.Errorf("cross-owner delete: want 404, got %d", w.Code)
	}
	// B's list does not include A's key
	w = tokReq(t, r, http.MethodGet, meKeys, nil, "owner-b")
	var lr struct {
		Keys []map[string]any `json:"keys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lr)
	if len(lr.Keys) != 0 {
		t.Errorf("owner-b sees %d keys, want 0", len(lr.Keys))
	}
}

// TestAPIKeyIntrospect covers the internal verify endpoint shape: active:true
// resolves owner identity; bogus and revoked keys return active:false.
func TestAPIKeyIntrospect(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-intro", Account: "ui", Enabled: true, AccountType: model.AccountTypeOther})

	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "ik"}, "u-intro")
	var iss issued
	_ = json.Unmarshal(w.Body.Bytes(), &iss)

	type introResp struct {
		Active      bool   `json:"active"`
		Sub         string `json:"sub"`
		AccountType string `json:"account_type"`
		KeyID       string `json:"key_id"`
	}
	post := func(token string) introResp {
		t.Helper()
		w := tokReq(t, r, http.MethodPost, introsURL, map[string]any{"token": token}, "")
		if w.Code != http.StatusOK {
			t.Fatalf("introspect: want 200, got %d", w.Code)
		}
		var ir introResp
		if err := json.Unmarshal(w.Body.Bytes(), &ir); err != nil {
			t.Fatal(err)
		}
		return ir
	}

	if ir := post(iss.Key); !ir.Active || ir.Sub != "u-intro" || ir.AccountType != string(model.AccountTypeOther) || ir.KeyID != iss.KeyID {
		t.Errorf("valid introspect = %+v", ir)
	}
	if ir := post("bak_dead_beef"); ir.Active {
		t.Error("bogus key must be inactive")
	}
	// revoke then introspect -> inactive
	_ = tokReq(t, r, http.MethodDelete, meKeys+"/"+iss.ID, nil, "u-intro")
	if ir := post(iss.Key); ir.Active {
		t.Error("revoked key must be inactive")
	}
}

// TestAPIKeyRegenerate: rotate returns a new one-time plaintext; the old key is
// rejected by introspect, the new one is accepted; same id.
func TestAPIKeyRegenerate(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-rg", Account: "rg", Enabled: true, AccountType: model.AccountTypeOther})

	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "rg"}, "u-rg")
	var first issued
	_ = json.Unmarshal(w.Body.Bytes(), &first)

	w = tokReq(t, r, http.MethodPost, meKeys+"/"+first.ID+"/regenerate", nil, "u-rg")
	if w.Code != http.StatusOK {
		t.Fatalf("regenerate: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var second issued
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("regenerate changed id: %s -> %s", first.ID, second.ID)
	}
	if second.Key == "" || second.Key == first.Key {
		t.Errorf("regenerate must return a new plaintext key")
	}

	introspect := func(token string) bool {
		w := tokReq(t, r, http.MethodPost, introsURL, map[string]any{"token": token}, "")
		var ir struct {
			Active bool `json:"active"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &ir)
		return ir.Active
	}
	if introspect(first.Key) {
		t.Error("old key must be inactive after regenerate")
	}
	if !introspect(second.Key) {
		t.Error("new key must be active after regenerate")
	}

	// regenerate of a non-owned / missing key -> 404
	if w := tokReq(t, r, http.MethodPost, meKeys+"/nope/regenerate", nil, "u-rg"); w.Code != http.StatusNotFound {
		t.Errorf("regenerate missing: want 404, got %d", w.Code)
	}
}

// TestAPIKeyAdmin covers admin oversight: list all (with owner), filter, revoke
// any; and that a non-admin is rejected.
func TestAPIKeyAdmin(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	db.Create(&model.User{ID: "u-a1", Account: "a1", Enabled: true})

	w := tokReq(t, r, http.MethodPost, meKeys, map[string]any{"name": "ak"}, "u-a1")
	var iss issued
	_ = json.Unmarshal(w.Body.Bytes(), &iss)

	// non-admin -> 403
	if w := tokReq(t, r, http.MethodGet, adminKeys, nil, "u-a1"); w.Code != http.StatusForbidden {
		t.Errorf("non-admin admin-list: want 403, got %d", w.Code)
	}

	// admin list shows owner_user_id
	w = adminReq(t, r, http.MethodGet, adminKeys, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin list: want 200, got %d", w.Code)
	}
	var lr struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil {
		t.Fatal(err)
	}
	if len(lr.Keys) != 1 || lr.Keys[0]["owner_user_id"] != "u-a1" {
		t.Fatalf("admin list = %+v", lr.Keys)
	}

	// filter by owner
	if w := adminReq(t, r, http.MethodGet, adminKeys+"?owner_id=nobody", nil); w.Code == http.StatusOK {
		var f struct {
			Keys []map[string]any `json:"keys"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &f)
		if len(f.Keys) != 0 {
			t.Errorf("owner filter = %+v, want empty", f.Keys)
		}
	}

	// admin revokes any key
	if w := adminReq(t, r, http.MethodDelete, adminKeys+"/"+iss.ID, nil); w.Code != http.StatusNoContent {
		t.Errorf("admin revoke: want 204, got %d", w.Code)
	}
}
