// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
)

func TestPublicOrigin(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		forwards string
		want     string
	}{
		{name: "无转发头按明文推导", host: "10.0.0.1:30779", want: "http://10.0.0.1:30779"},
		{name: "网关转发 https", host: "bkn.example.com", forwards: "https", want: "https://bkn.example.com"},
		// 多级代理会把协议串成 "https, http"，取最外层那个，否则 PRM 里会公布出
		// 内网明文地址，客户端拿去比对 origin 直接失配。
		{name: "多级代理取最外层", host: "bkn.example.com", forwards: "https, http", want: "https://bkn.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agent-retrieval/v1/mcp", nil)
			req.Host = tc.host
			if tc.forwards != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwards)
			}
			if got := publicOrigin(req); got != tc.want {
				t.Fatalf("publicOrigin() = %q, want %q", got, tc.want)
			}
		})
	}
}

// newChallengeEngine 造一个「鉴权中间件恒返回 401」的最小引擎，用来验证挑战头
// 是否被正确附加。
func newChallengeEngine(status int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewareMCPAuthChallenge())
	handler := func(c *gin.Context) { c.JSON(status, gin.H{"ok": status == http.StatusOK}) }
	engine.Any("/api/agent-retrieval/v1/mcp/*path", handler)
	engine.POST("/api/agent-retrieval/v1/kn/search_schema", handler)
	return engine
}

func TestMCPAuthChallenge(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		status     int
		wantHeader bool
	}{
		{name: "MCP 端点 401 带挑战头", path: "/api/agent-retrieval/v1/mcp/", status: http.StatusUnauthorized, wantHeader: true},
		// 成功响应不该带这个头，否则客户端会误判成需要重新授权。
		{name: "MCP 端点 200 不带", path: "/api/agent-retrieval/v1/mcp/", status: http.StatusOK, wantHeader: false},
		// 挑战头只对 MCP 端点有意义；REST 工具面的 401 维持原样，避免既有调用方行为变化。
		{name: "非 MCP 路径 401 不带", path: "/api/agent-retrieval/v1/kn/search_schema", status: http.StatusUnauthorized, wantHeader: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Host = "bkn.example.com"
			req.Header.Set("X-Forwarded-Proto", "https")
			newChallengeEngine(tc.status).ServeHTTP(w, req)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			got := w.Header().Get("WWW-Authenticate")
			if !tc.wantHeader {
				if got != "" {
					t.Fatalf("WWW-Authenticate = %q, want empty", got)
				}
				return
			}
			want := `Bearer resource_metadata="https://bkn.example.com/api/agent-retrieval/v1/.well-known/oauth-protected-resource"`
			if got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
			}
		})
	}
}

func requestPRM(t *testing.T) protectedResourceMetadata {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET(prmPath, handleProtectedResourceMetadata)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, prmPath, nil)
	req.Host = "bkn.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var doc protectedResourceMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal PRM: %v", err)
	}
	return doc
}

func TestProtectedResourceMetadata(t *testing.T) {
	_ = os.Setenv("CONFIG_PROFILE", "../infra/config")
	config.NewConfigLoader().OAuth.IssuerURL = ""

	doc := requestPRM(t)

	// resource 必须与客户端配置的 MCP 端点同源且是其路径前缀，否则客户端会以
	// "Protected resource does not match" 拒绝这份文档。
	const endpoint = "https://bkn.example.com/api/agent-retrieval/v1/mcp/"
	if !strings.HasPrefix(endpoint, doc.Resource) {
		t.Fatalf("resource %q 不是端点 %q 的前缀", doc.Resource, endpoint)
	}
	// issuer 留空时应回落到请求推导出的对外 origin（hydra 与本服务同网关）。
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://bkn.example.com" {
		t.Fatalf("authorization_servers = %v", doc.AuthorizationServers)
	}
	if len(doc.BearerMethodsSupported) != 1 || doc.BearerMethodsSupported[0] != "header" {
		t.Fatalf("bearer_methods_supported = %v", doc.BearerMethodsSupported)
	}
}

func TestProtectedResourceMetadataIssuerOverride(t *testing.T) {
	_ = os.Setenv("CONFIG_PROFILE", "../infra/config")
	// 授权服务器另挂域名的场景：配置覆盖优先，且尾部斜杠要被裁掉——客户端按
	// issuer 拼 /.well-known/... ，多一个斜杠就 404。
	config.NewConfigLoader().OAuth.IssuerURL = "https://auth.example.com/"
	defer func() { config.NewConfigLoader().OAuth.IssuerURL = "" }()

	doc := requestPRM(t)

	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://auth.example.com" {
		t.Fatalf("authorization_servers = %v", doc.AuthorizationServers)
	}
}
