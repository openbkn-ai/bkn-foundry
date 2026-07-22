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

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knactionrecall"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knfindskills"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knlogicpropertyresolver"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knqueryobjectinstance"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knquerysubgraph"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knquerytools"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knretrieval"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/knsearch"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/driveradapters/mcp"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

type restPublicHandler struct {
	Hydra                          interfaces.Hydra
	AppKeys                        interfaces.AppKeyVerifier
	KnRetrievalHandler             knretrieval.KnRetrievalHandler
	MCPHandler                     http.Handler
	KnLogicPropertyResolverHandler knlogicpropertyresolver.KnLogicPropertyResolverHandler
	KnActionRecallHandler          knactionrecall.KnActionRecallHandler
	KnQueryObjectInstanceHandler   knqueryobjectinstance.KnQueryObjectInstanceHandler
	KnQuerySubgraphHandler         knquerysubgraph.KnQuerySubgraphHandler
	KnSearchHandler                knsearch.KnSearchHandler
	KnFindSkillsHandler            knfindskills.KnFindSkillsHandler
	KnQueryToolsHandler            knquerytools.KnQueryToolsHandler
	Logger                         interfaces.Logger
}

// NewRestPublicHandler 创建restHandler实例
func NewRestPublicHandler(logger interfaces.Logger) interfaces.HTTPRouterInterface {
	return &restPublicHandler{
		Hydra:                          drivenadapters.NewHydra(),
		AppKeys:                        drivenadapters.NewAppKeyVerifier(),
		KnRetrievalHandler:             knretrieval.NewKnRetrievalHandler(),
		MCPHandler:                     mcp.NewMCPHandler(),
		KnLogicPropertyResolverHandler: knlogicpropertyresolver.NewKnLogicPropertyResolverHandler(),
		KnActionRecallHandler:          knactionrecall.NewKnActionRecallHandler(),
		KnQueryObjectInstanceHandler:   knqueryobjectinstance.NewKnQueryObjectInstanceHandler(),
		KnQuerySubgraphHandler:         knquerysubgraph.NewKnQuerySubgraphHandler(),
		KnSearchHandler:                knsearch.NewKnSearchHandler(),
		KnFindSkillsHandler:            knfindskills.NewKnFindSkillsHandler(),
		KnQueryToolsHandler:            knquerytools.NewKnQueryToolsHandler(),
		Logger:                         logger,
	}
}

// RegisterPublic 注册公共路由
func (r *restPublicHandler) RegisterRouter(engine *gin.RouterGroup) {
	mws := []gin.HandlerFunc{}
	mws = append(mws, middlewareRequestLog(r.Logger), middlewareTrace, middlewareIntrospectVerify(r.Hydra, r.AppKeys), middlewareResponseFormat())
	engine.Use(mws...)

	engine.POST("/kn/semantic-search", r.KnRetrievalHandler.SemanticSearch)
	engine.POST("/kn/logic-property-resolver", r.KnLogicPropertyResolverHandler.ResolveLogicProperties)
	engine.POST("/kn/get_action_info", r.KnActionRecallHandler.GetActionInfo)
	engine.POST("/kn/execute_action", r.KnActionRecallHandler.ExecuteAction)
	engine.POST("/kn/query_object_instance", r.KnQueryObjectInstanceHandler.QueryObjectInstance)
	engine.POST("/kn/query_instance_subgraph", r.KnQuerySubgraphHandler.QueryInstanceSubgraph)
	engine.POST("/kn/search_schema", r.KnSearchHandler.SearchSchema)
	engine.POST("/kn/kn_search", r.KnSearchHandler.KnSearch)
	engine.POST("/kn/find_skills", r.KnFindSkillsHandler.FindSkills)

	// 同时作为 MCP 工具 + operator-integration toolbox(OpenAPI HTTP)入口
	engine.POST("/kn/run_sql", r.KnQueryToolsHandler.RunSQL)
	engine.POST("/kn/list_knowledge_networks", r.KnQueryToolsHandler.ListKnowledgeNetworks)
	engine.POST("/kn/get_kn_detail", r.KnQueryToolsHandler.GetKnDetail)
	engine.POST("/kn/get_object_types", r.KnQueryToolsHandler.GetObjectTypes)
	engine.POST("/kn/get_relation_types", r.KnQueryToolsHandler.GetRelationTypes)
	engine.POST("/kn/list_resources", r.KnQueryToolsHandler.ListResources)
	engine.POST("/kn/describe_resource", r.KnQueryToolsHandler.DescribeResource)

	// MCP Server (Bearer token auth, supports Cursor/Claude Desktop)
	// GET /mcp/info 返回自描述文档（工具目录 + 连接方式），其余走标准 MCP Streamable HTTP。
	engine.Any("/mcp/*path", r.handleMCP)
}

// handleMCP 在 MCP catch-all 路由内分流：GET …/mcp/info 返回自描述文档，其余交给 MCP Server。
func (r *restPublicHandler) handleMCP(c *gin.Context) {
	if c.Request.Method == http.MethodGet && c.Param("path") == "/info" {
		info, err := mcp.BuildMCPInfo(mcpEndpointURL(c.Request))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, info)
		return
	}
	r.MCPHandler.ServeHTTP(c.Writer, c.Request)
}

// mcpEndpointURL 依据请求推导本服务对外的 MCP 端点（去掉末尾的 /info）。
func mcpEndpointURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	base := strings.TrimSuffix(req.URL.Path, "/info")
	return scheme + "://" + req.Host + base
}
