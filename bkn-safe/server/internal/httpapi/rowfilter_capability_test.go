// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	rowfiltersocket "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
)

func TestRowFilterPublishedObjectTypeResolverUsesInternalCapabilityContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != rowFilterCapabilityPath {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object_type_ref":"kn-1/customer","published":true,"properties":{"region":{"type":"string","exact_filterable":true}}}`))
	}))
	defer server.Close()
	resolver, err := NewRowFilterPublishedObjectTypeResolver(config.UpstreamConfig{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := resolver.ResolvePublishedObjectType(t.Context(), "kn-1/customer")
	if err != nil {
		t.Fatal(err)
	}
	if !capability.Published || capability.Properties["region"].Type != rowfiltersocket.ValueString || !capability.Properties["region"].ExactFilterable {
		t.Fatalf("capability = %+v", capability)
	}
}

func TestRowFilterPublishedObjectTypeResolverRejectsMismatchedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(`{"object_type_ref":"kn-1/order"}`))
	}))
	defer server.Close()
	resolver, err := NewRowFilterPublishedObjectTypeResolver(config.UpstreamConfig{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolvePublishedObjectType(t.Context(), "kn-1/customer"); err == nil {
		t.Fatal("expected object type mismatch error")
	}
}
