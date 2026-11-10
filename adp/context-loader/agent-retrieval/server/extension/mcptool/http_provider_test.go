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
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
)

func TestHTTPProviderPreservesOptionalDisplayMetadata(t *testing.T) {
	for _, withDisplay := range []bool{false, true} {
		definition := map[string]any{
			"name": "probe", "description": "provider description", "capability": "probe",
			"min_edition": "professional", "control_plane": true, "group": "evaluation",
			"input_schema": map[string]any{"type": "object"},
		}
		if withDisplay {
			definition["title"] = "Start evaluation"
			definition["group_title"] = "Quality evaluation"
			definition["order"] = 820
		}
		raw, err := json.Marshal(map[string]any{"call_path": "/api/extension/call", "tools": []any{definition}})
		if err != nil {
			t.Fatal(err)
		}
		tools, err := LoadHTTPProviderManifest("https://provider.invalid/manifest", raw, nil)
		if err != nil || len(tools) != 1 {
			t.Fatalf("load display=%v: %v", withDisplay, err)
		}
		tool := tools[0]
		if tool.Name != "probe" || tool.Desc != "provider description" || tool.Group != "evaluation" || !tool.ControlPlane {
			t.Fatalf("provider metadata changed: %+v", tool)
		}
		if withDisplay {
			if tool.Title != "Start evaluation" || tool.GroupTitle != "Quality evaluation" || tool.Order != 820 {
				t.Fatalf("display metadata lost in HTTP adapter: %+v", tool)
			}
		} else if tool.Title != "" || tool.GroupTitle != "" || tool.Order != 0 {
			t.Fatalf("legacy provider display defaults changed: %+v", tool)
		}
	}
}

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

func TestConfiguredProviderBootsWithoutDiscoveryAndRechecksLicence(t *testing.T) {
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodGet {
			t.Error("configured schemas must not probe the paid discovery endpoint")
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/extension/call" || r.Header.Get("Authorization") != "Bearer user" {
			t.Error("provider call lost its fixed origin or user identity")
		}
		json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
	}))
	defer endpoint.Close()
	manifest := []byte(`{"call_path":"/api/extension/call","tools":[{"name":"probe","capability":"probe","min_edition":"professional","input_schema":{"type":"object"}}]}`)
	ResetForTest(entitlement.FixedGate(licverify.EditionCommunity))
	t.Cleanup(func() { ResetForTest(entitlement.FixedGate(licverify.EditionCommunity)) })
	tools, err := LoadConfiguredHTTPProvider(context.Background(), endpoint.URL+"/manifest", manifest, nil)
	if err != nil || len(tools) != 1 || calls != 0 {
		t.Fatalf("configured discovery failed: %v", err)
	}
	Register(tools[0])
	entitlement.Freeze()
	request := mcp.CallToolRequest{Header: http.Header{"Authorization": []string{"Bearer user"}}}
	request.Params.Name = "probe"
	handler := Gated(tools[0])
	if tools[0].Allowed() {
		t.Fatal("unlicensed tool became visible")
	}
	if _, err := handler(context.Background(), request); err == nil || calls != 0 {
		t.Fatal("unlicensed tool called provider")
	}
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))
	if !tools[0].Allowed() {
		t.Fatal("activation requires a restart")
	}
	result, err := handler(context.Background(), request)
	if err != nil || result.IsError || calls != 1 {
		t.Fatal("licensed call failed")
	}
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))
	if _, err := handler(context.Background(), request); err == nil || calls != 1 {
		t.Fatal("revocation did not take effect")
	}
}

func TestConfiguredProviderRejectsUnsafeDeploymentDefinitions(t *testing.T) {
	for _, raw := range []string{`{`, strings.Repeat("x", 256*1024+1), `{"call_path":"https://foreign/api/call","tools":[{"name":"x"}]}`} {
		if _, err := LoadConfiguredHTTPProvider(context.Background(), "https://provider/manifest", []byte(raw), nil); err == nil {
			t.Fatal("invalid configured manifest accepted")
		}
	}
	if _, err := LoadConfiguredHTTPProvider(context.Background(), "", []byte(`{}`), nil); err == nil {
		t.Fatal("configured manifest without provider origin accepted")
	}
}
