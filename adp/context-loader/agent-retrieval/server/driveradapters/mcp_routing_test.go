// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The catch-all under /mcp used to carry an ordering trap: /ptc matched by
// prefix, and mcp-go matches by prefix too, so putting it before the exact paths
// handed /mcp/ptc/toolkit to the PTC server, which answered a silent 404 that
// looked like the endpoint was not deployed. Both PTC surfaces are gone and with
// them the trap; what is left is one exact path and a fall-through.
//
// The real MCP server is not built here - assembling it needs downstream
// connections - so a probe stands in and only the dispatch is checked.
func newRoutingProbe(t *testing.T) (*gin.Engine, *string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var hit string
	handler := &restPublicHandler{
		MCPHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hit = "mcp"
			w.WriteHeader(http.StatusOK)
		}),
	}

	engine := gin.New()
	engine.Any("/api/agent-retrieval/v1/mcp/*path", func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && c.Param("path") == mcpInfoPath {
			hit = "mcp-info"
			c.Status(http.StatusOK)
			return
		}
		handler.handleMCP(c)
	})
	return engine, &hit
}

func TestMCPRoutingDispatch(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"info is served here", http.MethodGet, "/api/agent-retrieval/v1/mcp/info", "mcp-info"},
		{"tool calls go to the server", http.MethodPost, "/api/agent-retrieval/v1/mcp/", "mcp"},
		// The withdrawn paths are not special-cased any more. They reach the MCP
		// server like any other unknown subpath and are refused there, rather than
		// being routed somewhere that no longer exists.
		{"withdrawn ptc endpoint falls through", http.MethodPost, "/api/agent-retrieval/v1/mcp/ptc", "mcp"},
		{"withdrawn toolkit endpoint falls through", http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/toolkit", "mcp"},
		{"withdrawn toolkit alias falls through", http.MethodGet, "/api/agent-retrieval/v1/mcp/toolkit", "mcp"},
		{"withdrawn ptc info falls through", http.MethodGet, "/api/agent-retrieval/v1/mcp/ptc/info", "mcp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, hit := newRoutingProbe(t)
			*hit = ""
			engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, http.NoBody))
			if *hit != tc.want {
				t.Fatalf("%s %s reached %q, want %q", tc.method, tc.path, *hit, tc.want)
			}
		})
	}
}
