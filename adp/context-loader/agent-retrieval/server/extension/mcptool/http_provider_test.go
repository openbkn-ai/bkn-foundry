// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcptool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/licverify"
)

func TestHTTPProviderForwardsOnlyTransportCredentialToFixedOrigin(t *testing.T) {
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(HTTPManifest{CallPath: "/api/extension/call", Tools: []HTTPTool{{Name: "probe", Description: "test", Capability: "probe", MinEdition: licverify.EditionProfessional, ControlPlane: true, Input: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`)}}})
			return
		}
		calls++
		if r.URL.Path != "/api/extension/call" || r.Header.Get("Authorization") != "Bearer transport" || r.Header.Get("X-Account-Id") != "" {
			t.Error("credential scope changed")
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted": true})
	}))
	defer endpoint.Close()
	tools, err := LoadHTTPProvider(context.Background(), endpoint.URL+"/manifest", nil)
	if err != nil || len(tools) != 1 || !tools[0].ControlPlane {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = "probe"
	request.Params.Arguments = map[string]any{"value": "x", "Authorization": "Bearer forged"}
	reply, err := tools[0].Handle(context.Background(), request)
	if err != nil || !reply.IsError || calls != 0 {
		t.Fatal("argument credential was accepted")
	}
	request.Header = http.Header{"Authorization": []string{"Bearer transport"}, "X-Account-Id": []string{"forged"}}
	reply, err = tools[0].Handle(context.Background(), request)
	if err != nil || reply.IsError || calls != 1 || reply.StructuredContent == nil {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

func TestHTTPProviderRejectsManifestRedirectAndForeignCallPath(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls++; w.WriteHeader(200) }))
	defer foreign.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, foreign.URL, http.StatusFound) }))
	defer endpoint.Close()
	if _, err := LoadHTTPProvider(context.Background(), endpoint.URL, nil); err == nil || foreignCalls != 0 {
		t.Fatal("manifest followed a redirect")
	}
	for _, path := range []string{foreign.URL + "/api/call", "//foreign/api/call", "/api/../secret", "/api/call?token=x"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(HTTPManifest{CallPath: path, Tools: []HTTPTool{{Name: "probe", Capability: "probe", MinEdition: licverify.EditionProfessional, Input: json.RawMessage(`{"type":"object"}`)}}})
		}))
		_, err := LoadHTTPProvider(context.Background(), server.URL, nil)
		server.Close()
		if err == nil {
			t.Fatalf("accepted call path %s", path)
		}
	}
	if _, err := LoadHTTPProvider(context.Background(), "http://user:secret@localhost/manifest", nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe provider URL or error")
	}
}
