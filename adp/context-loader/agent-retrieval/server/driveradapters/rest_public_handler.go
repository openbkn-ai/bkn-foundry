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

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knactionrecall"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knfindskills"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knlogicpropertyresolver"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knqueryobjectinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knquerysubgraph"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knquerytools"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knretrieval"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knsearch"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knskills"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsSkills "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
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
	KnSkillsHandler                knskills.KnSkillsHandler
	Logger                         interfaces.Logger
	LifecycleClient                *bkntrace.LifecycleClient
	// ServicePort 用于推导沙箱回访本服务的地址（见 PTC 工具包端点）。
	ServicePort int
}

// NewRestPublicHandler 创建restHandler实例
// servicePort 用于推导沙箱回访地址；沙箱在集群内，走不了浏览器侧的网关地址。
func NewRestPublicHandler(logger interfaces.Logger, servicePort int) interfaces.HTTPRouterInterface {
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
		KnSkillsHandler:                knskills.NewKnSkillsHandler(),
		Logger:                         logger,
		LifecycleClient:                bkntrace.NewLifecycleClientFromEnv(),
		ServicePort:                    servicePort,
	}
}

// RegisterPublic 注册公共路由
func (r *restPublicHandler) RegisterRouter(engine *gin.RouterGroup) {
	mws := []gin.HandlerFunc{}
	mws = append(mws, middlewareRequestLog(r.Logger), middlewareTrace, middlewareIntrospectVerify(r.Hydra, r.AppKeys), middlewareResponseFormat(), middlewareLifecycle(r.LifecycleClient))
	engine.Use(mws...)

	engine.POST("/kn/semantic-search", r.KnRetrievalHandler.SemanticSearch)
	engine.POST("/kn/logic-property-resolver", r.KnLogicPropertyResolverHandler.ResolveLogicProperties)
	engine.POST("/kn/get_action_info", r.KnActionRecallHandler.GetActionInfo)
	engine.POST("/kn/execute_action", r.KnActionRecallHandler.ExecuteAction)
	engine.POST("/kn/get_action_execution", r.KnActionRecallHandler.GetActionExecution)
	engine.POST("/kn/list_action_executions", r.KnActionRecallHandler.ListActionExecutions)
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
	engine.POST("/kn/query_metric", r.KnQueryToolsHandler.QueryMetric)
	engine.POST("/kn/list_resources", r.KnQueryToolsHandler.ListResources)
	engine.POST("/kn/describe_resource", r.KnQueryToolsHandler.DescribeResource)

	// 技能面：浏览 / 读文件 / 执行（find_skills 之后的下钻链路）
	engine.POST("/kn/list_skills", r.KnSkillsHandler.ListSkills)
	engine.POST("/kn/get_skill_content", r.KnSkillsHandler.GetSkillContent)
	engine.POST("/kn/read_skill_file", r.KnSkillsHandler.ReadSkillFile)
	// 与 MCP 工具面同一道闸：关闭时这条路由根本不注册，而不是注册后再拒绝。
	// 「这个部署没有技能执行能力」要在路由表上成立，否则文档里那句「唯一的
	// 命令执行通道」在 REST 这侧就是假的。
	if logicsSkills.ExecuteEnabled() {
		engine.POST("/kn/execute_skill", r.KnSkillsHandler.ExecuteSkill)
	}

	// MCP Server (Bearer token auth, supports Cursor/Claude Desktop)
	// GET /mcp/info 返回自描述文档（工具目录 + 连接方式），
	// GET /mcp/toolkit 返回同一工具面的代码模式形态，其余走标准 MCP Streamable HTTP。
	engine.Any("/mcp/*path", r.handleMCP)
}

// handlePTCToolkit 返回 PTC 工具包（GET …/mcp/toolkit）。
// 内容随工具面变化，客户端按 version 缓存。
func (r *restPublicHandler) handlePTCToolkit(c *gin.Context) {
	toolkit, err := mcp.BuildPTCToolkit(publicEndpointURL(c.Request, "/mcp"), r.ServicePort)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toolkit)
}

// handleMCP 在 MCP catch-all 路由内分流：GET …/mcp/info 返回自描述文档，其余交给 MCP Server。
func (r *restPublicHandler) handleMCP(c *gin.Context) {
	// /toolkit 与 /info 是同一份工具面的两种投影：/info 给要接 MCP 的客户端，
	// /toolkit 给把工具面收进沙箱的代码模式客户端。放在同一前缀下，是因为两者
	// 的内容都随工具面变化，分开会让人以为它们可能不一致。
	if c.Request.Method == http.MethodGet && c.Param("path") == "/toolkit" {
		r.handlePTCToolkit(c)
		return
	}
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
	scheme := requestScheme(req)
	base := strings.TrimSuffix(req.URL.Path, "/info")
	return scheme + "://" + req.Host + base
}

// publicEndpointURL 按本组路由前缀拼出同一服务下另一个端点的对外地址。
// 供路径不同的端点复用（如 /ptc/toolkit 需要报出 /mcp 的地址），
// 不能用 mcpEndpointURL 那套「从当前路径去尾」的推法。
func publicEndpointURL(req *http.Request, suffix string) string {
	base := req.URL.Path
	if i := strings.Index(base, "/v1/"); i >= 0 {
		base = base[:i+len("/v1")]
	}
	return requestScheme(req) + "://" + req.Host + base + suffix
}

func requestScheme(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme
}
