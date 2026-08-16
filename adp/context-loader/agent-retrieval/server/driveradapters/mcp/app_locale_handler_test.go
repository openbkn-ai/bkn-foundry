// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestLocalizedMCPHandlerPinsLocaleToSession(t *testing.T) {
	handler := &localizedMCPHandler{
		handlers: map[string]http.Handler{
			defaultMCPLocale: markerMCPHandler("zh-session"),
			"en-US":          markerMCPHandler("en-session"),
		},
	}

	first := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	first.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if got := firstRecorder.Body.String(); got != "en-session" {
		t.Fatalf("initialize response = %q, want English handler", got)
	}
	if sessionID := firstRecorder.Header().Get(server.HeaderKeySessionID); sessionID != "en-session" {
		t.Fatalf("Mcp-Session-Id = %q, want en-session", sessionID)
	}

	followup := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	followup.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
	followup.Header.Set(server.HeaderKeySessionID, "en-session")
	followupRecorder := httptest.NewRecorder()
	handler.ServeHTTP(followupRecorder, followup)
	if got := followupRecorder.Body.String(); got != "en-session" {
		t.Fatalf("follow-up response = %q, want session-pinned English handler", got)
	}
}

func TestLocalizedMCPHandlerReleasesAndExpiresSessionLocale(t *testing.T) {
	handler := &localizedMCPHandler{
		handlers: map[string]http.Handler{
			defaultMCPLocale: markerMCPHandler("zh-session"),
			"en-US":          markerMCPHandler("en-session"),
		},
	}
	handler.sessionLocales.Store("expired", mcpSessionLocale{
		locale: "en-US", lastUsed: time.Now().Add(-mcpSessionIdleTTL),
	})

	request := httptest.NewRequest(http.MethodDelete, endpointPath, nil)
	request.Header.Set(server.HeaderKeySessionID, "en-session")
	response := httptest.NewRecorder()
	handler.sessionLocales.Store("en-session", mcpSessionLocale{locale: "en-US", lastUsed: time.Now()})
	handler.ServeHTTP(response, request)

	if _, ok := handler.sessionLocales.Load("en-session"); ok {
		t.Fatal("DELETE did not release the session locale")
	}
	if _, ok := handler.sessionLocales.Load("expired"); ok {
		t.Fatal("expired session locale was not pruned")
	}
}

func TestMCPInitializeAndToolCatalogUseSessionLocale(t *testing.T) {
	handler := NewMCPHandlerWithLifecycle(nil)

	initialize := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{
			"protocolVersion":"2025-06-18",
			"capabilities":{},
			"clientInfo":{"name":"locale-test","version":"1.0"}
		}
	}`))
	initialize.Header.Set("Content-Type", "application/json")
	initialize.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	initializeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(initializeRecorder, initialize)

	if initializeRecorder.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", initializeRecorder.Code, initializeRecorder.Body.String())
	}
	sessionID := initializeRecorder.Header().Get(server.HeaderKeySessionID)
	if sessionID == "" {
		t.Fatal("initialize response did not return Mcp-Session-Id")
	}
	var initializeResponse struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initializeRecorder.Body.Bytes(), &initializeResponse); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if !strings.HasPrefix(initializeResponse.Result.Instructions, "Context Loader knowledge network tools.") {
		t.Fatalf("initialize instructions = %q, want English instructions", initializeResponse.Result.Instructions)
	}

	toolsList := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":2,
		"method":"tools/list",
		"params":{}
	}`))
	toolsList.Header.Set("Content-Type", "application/json")
	toolsList.Header.Set(server.HeaderKeySessionID, sessionID)
	toolsList.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
	toolsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(toolsRecorder, toolsList)

	if toolsRecorder.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body = %s", toolsRecorder.Code, toolsRecorder.Body.String())
	}
	var toolsResponse struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(toolsRecorder.Body.Bytes(), &toolsResponse); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	for _, tool := range toolsResponse.Result.Tools {
		if tool.Name == toolKeySearchSchema {
			if !strings.HasPrefix(tool.Description, "Explore schema by natural language.") {
				t.Fatalf("search_schema description = %q, want English session-pinned catalog", tool.Description)
			}
			return
		}
	}
	t.Fatalf("tools/list did not include %q", toolKeySearchSchema)
}

func markerMCPHandler(sessionID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(server.HeaderKeySessionID, sessionID)
		_, _ = w.Write([]byte(sessionID))
	})
}
