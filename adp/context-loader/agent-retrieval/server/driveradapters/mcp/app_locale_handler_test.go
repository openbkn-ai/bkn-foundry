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

	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestLocalizedMCPHandlerFollowsRequestLocale(t *testing.T) {
	handler := &localizedMCPHandler{
		handlers: map[string]http.Handler{
			defaultMCPLocale: markerMCPHandler("zh-handler"),
			"en-US":          markerMCPHandler("en-handler"),
		},
	}

	english := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	english.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	englishRecorder := httptest.NewRecorder()
	handler.ServeHTTP(englishRecorder, english)
	if got := englishRecorder.Body.String(); got != "en-handler" {
		t.Fatalf("en-US request = %q, want the English handler", got)
	}

	// The same session asking in another language gets that language. The
	// handler holds no session state to contradict the header with.
	chinese := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	chinese.Header.Set(sharedrest.AcceptLanguageHeader, "zh-CN")
	chinese.Header.Set(server.HeaderKeySessionID, "en-handler")
	chineseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(chineseRecorder, chinese)
	if got := chineseRecorder.Body.String(); got != "zh-handler" {
		t.Fatalf("zh-CN request = %q, want the Chinese handler", got)
	}
}

func TestLocalizedMCPHandlerPutsRequestLocaleInContext(t *testing.T) {
	localeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(common.GetLanguageFromCtx(r.Context())))
	})
	handler := &localizedMCPHandler{
		handlers: map[string]http.Handler{
			defaultMCPLocale: localeHandler,
			"en-US":          localeHandler,
		},
	}

	request := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	request.Header.Set(sharedrest.AcceptLanguageHeader, "en-US")
	request.Header.Set(server.HeaderKeySessionID, "some-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "en-US" {
		t.Fatalf("request context locale = %q, want en-US", got)
	}
}

func TestLocalizedMCPHandlerWithoutHeaderUsesDefaultLocale(t *testing.T) {
	handler := &localizedMCPHandler{
		handlers: map[string]http.Handler{
			defaultMCPLocale: markerMCPHandler("zh-handler"),
			"en-US":          markerMCPHandler("en-handler"),
		},
	}

	request := httptest.NewRequest(http.MethodPost, endpointPath, nil)
	request.Header.Set(server.HeaderKeySessionID, "some-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "zh-handler" {
		t.Fatalf("header-less request = %q, want the service default handler", got)
	}
}

// TestMCPToolCatalogFollowsRequestLocale drives the real handler end to end:
// initialize in one language, then list tools in another, and check the catalog
// follows each request rather than whatever the session started with.
func TestMCPToolCatalogFollowsRequestLocale(t *testing.T) {
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

	englishDescription := searchSchemaDescription(t, handler, sessionID, "en-US")
	if !strings.HasPrefix(englishDescription, "Explore schema by natural language.") {
		t.Fatalf("en-US search_schema description = %q, want the English catalog", englishDescription)
	}

	chineseDescription := searchSchemaDescription(t, handler, sessionID, "zh-CN")
	if !strings.HasPrefix(chineseDescription, "统一的 Schema 探索入口") {
		t.Fatalf("zh-CN search_schema description = %q, want the default-locale catalog", chineseDescription)
	}
}

func searchSchemaDescription(t *testing.T, handler http.Handler, sessionID, locale string) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader(`{
		"jsonrpc":"2.0",
		"id":2,
		"method":"tools/list",
		"params":{}
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(server.HeaderKeySessionID, sessionID)
	request.Header.Set(sharedrest.AcceptLanguageHeader, locale)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	for _, tool := range response.Result.Tools {
		if tool.Name == toolKeySearchSchema {
			return tool.Description
		}
	}
	t.Fatalf("tools/list did not include %q", toolKeySearchSchema)
	return ""
}

func markerMCPHandler(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(marker))
	})
}
