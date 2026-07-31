// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
)

const (
	// servicePrefix 是本服务对外路由组的前缀，与 main.go 中的 engine.Group 一致。
	servicePrefix = "/api/agent-retrieval/v1"
	// mcpEndpointPath MCP Streamable HTTP 端点的对外路径。
	mcpEndpointPath = servicePrefix + "/mcp"
	// prmPath 受保护资源元数据文档挂在本服务前缀下的路径。挂在前缀下（而不是
	// 只挂 RFC 9728 的根路径）是为了让它天然落在既有 ingress 规则里——WWW-Authenticate
	// 指向的就是这个地址，存量环境不更新 ingress 也能走通。
	prmPath = servicePrefix + "/.well-known/oauth-protected-resource"
	// prmCanonicalPath 是 RFC 9728 规定的推导路径：在资源路径前插入
	// /.well-known/oauth-protected-resource。部分客户端在没有 WWW-Authenticate
	// 时会直接按这个形式猜，所以一并提供（需要 ingress 放行，见 chart values）。
	prmCanonicalPath = "/.well-known/oauth-protected-resource" + mcpEndpointPath
)

// mcpScopes 是 MCP 客户端应当申请的 scope，与 bkn-safe 里 openbkn-mcp 这个
// client 注册的 scope 保持一致。
var mcpScopes = []string{"openid", "offline", "all"}

// protectedResourceMetadata 是 RFC 9728 定义的受保护资源元数据文档。MCP 客户端
// 收到 401 后按 WWW-Authenticate 里的 resource_metadata 取这份文档，从中得知该
// 去哪个授权服务器换令牌。
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceDocumentation  string   `json:"resource_documentation,omitempty"`
}

// publicOrigin 依据请求推导本服务对外的 scheme + host。服务本身跑在网关后面的
// 明文 HTTP 上，所以协议以 X-Forwarded-Proto 为准。
func publicOrigin(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		// 多级代理会串成 "https, http"，取最外层那个。
		if i := strings.Index(p, ","); i >= 0 {
			p = p[:i]
		}
		scheme = strings.TrimSpace(p)
	}
	return scheme + "://" + req.Host
}

// authorizationServerURL 返回 PRM 中要公布的授权服务器地址。默认取请求推导出的
// 对外 origin——hydra 与本服务同在一个网关后面，issuer 就是网关地址。授权服务器
// 另挂域名时用 oauth.issuer_url 覆盖。
func authorizationServerURL(req *http.Request) string {
	if issuer := strings.TrimSpace(config.NewConfigLoader().OAuth.IssuerURL); issuer != "" {
		return strings.TrimSuffix(issuer, "/")
	}
	return publicOrigin(req)
}

// handleProtectedResourceMetadata 返回本服务 MCP 端点的 RFC 9728 元数据。该端点
// 必须免鉴权——客户端正是因为还没有令牌才来读它。
func handleProtectedResourceMetadata(c *gin.Context) {
	origin := publicOrigin(c.Request)
	c.JSON(http.StatusOK, protectedResourceMetadata{
		Resource:               origin + mcpEndpointPath,
		AuthorizationServers:   []string{authorizationServerURL(c.Request)},
		ScopesSupported:        mcpScopes,
		BearerMethodsSupported: []string{"header"},
		ResourceDocumentation:  origin + mcpEndpointPath + "/info",
	})
}

// RegisterOAuthMetadataRoutes 在根引擎上挂 RFC 9728 推导路径的元数据端点。
// 前缀下的那份由 restPublicHandler 自己注册（免鉴权，排在鉴权中间件之前）。
func RegisterOAuthMetadataRoutes(engine gin.IRoutes) {
	engine.GET(prmCanonicalPath, handleProtectedResourceMetadata)
}

// challengeWriter 在响应码为 401 时补上 WWW-Authenticate。包一层而不是提前无条件
// 设置，是为了不让成功响应也带上这个头。
type challengeWriter struct {
	gin.ResponseWriter
	challenge string
}

func (w *challengeWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", w.challenge)
	}
	w.ResponseWriter.WriteHeader(code)
}

// middlewareMCPAuthChallenge 让 MCP 端点的 401 带上 WWW-Authenticate。没有这个头
// 客户端只会把 401 当普通错误报出来，不会去发起 OAuth 流程。必须排在鉴权中间件
// 之前，才能包住它写出的 401。
func middlewareMCPAuthChallenge() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, mcpEndpointPath) {
			c.Next()
			return
		}
		challenge := `Bearer resource_metadata="` + publicOrigin(c.Request) + prmPath + `"`
		c.Writer = &challengeWriter{ResponseWriter: c.Writer, challenge: challenge}
		c.Next()
	}
}
