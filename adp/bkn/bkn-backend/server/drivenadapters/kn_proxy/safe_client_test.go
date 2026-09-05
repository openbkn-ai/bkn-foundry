// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bkn-backend/interfaces"
)

func TestSafeClientUsesManagedInternalContracts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/safe/in/v1/managed-proxy-accounts", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["managed_resource_type"] != "knowledge_network" || body["managed_resource_id"] != "kn-1" {
			t.Fatalf("create body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(interfaces.ManagedProxyAccount{
			ProxyAccountID: "proxy-1", AccountType: interfaces.KNProxyAccountTypeApp,
			ManagedBy: "bkn", ManagedResourceType: "knowledge_network", ManagedResourceID: "kn-1",
			LifecycleStatus: interfaces.KNProxyLifecycleActive, Enabled: true,
		})
	})
	mux.HandleFunc("/api/safe/in/v1/proxy-grant-sources/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProxyID string                          `json:"proxy_account_id"`
			Grantor string                          `json:"grantor_id"`
			Source  interfaces.ProxyGrantSourceSpec `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ProxyID != "proxy-1" || body.Grantor != "grantor-1" || body.Source.BindingID != "ot-1" {
			t.Fatalf("check body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(interfaces.ProxyGrantCheckResult{Allowed: true})
	})
	mux.HandleFunc("/api/safe/in/v1/managed-proxy-accounts/proxy-1/restore", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(interfaces.ManagedProxyAccount{
			ProxyAccountID: "proxy-1", LifecycleStatus: interfaces.KNProxyLifecycleActive, Enabled: true,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewManagedProxyAccess(server.URL)
	account, created, err := client.Create(t.Context(), "kn-1", "network proxy")
	if err != nil || !created || account.ProxyAccountID != "proxy-1" {
		t.Fatalf("Create() = account %#v, created %v, err %v", account, created, err)
	}
	account, err = client.Restore(t.Context(), "proxy-1")
	if err != nil || !account.Enabled || account.LifecycleStatus != interfaces.KNProxyLifecycleActive {
		t.Fatalf("Restore() = account %#v, err %v", account, err)
	}
	result, err := client.CheckGrant(t.Context(), "proxy-1", "grantor-1", interfaces.ProxyGrantSourceSpec{
		ResourceType: "resource", ResourceID: "resource-1", Operation: "query_data",
		SourceType: interfaces.ProxyGrantSourceTypeKNBinding, SourceID: "source-1", KNID: "kn-1",
		BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1",
	})
	if err != nil || !result.Allowed {
		t.Fatalf("CheckGrant() = %#v, %v", result, err)
	}
}

func TestSafeClientDoesNotExposeErrorResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"secret":"must-not-escape"}`))
	}))
	defer server.Close()

	client := NewManagedProxyAccess(server.URL)
	_, _, err := client.Create(t.Context(), "kn-1", "proxy")
	if err == nil || contains(err.Error(), "must-not-escape") {
		t.Fatalf("Create() error = %v", err)
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
