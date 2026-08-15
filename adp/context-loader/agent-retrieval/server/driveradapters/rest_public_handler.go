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
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"

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
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	infrarest "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsSkills "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

type restPublicHandler struct {
	Hydra              interfaces.Hydra
	AppKeys            interfaces.AppKeyVerifier
	KnRetrievalHandler knretrieval.KnRetrievalHandler
	MCPHandler         http.Handler
	// PTCMCPHandler 是 PTC 的独立 MCP 端点（…/mcp/ptc）。与 MCPHandler 分开，
	// 是因为两者的工具面互斥：客户端同时看到 run_code 与二十个业务工具时，
	// 模型会挑后者，PTC 就退化成普通工具调用。装配失败时为 nil，该路由报 503，
	// 不影响主工具面。
	PTCMCPHandler                  http.Handler
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

var buildMCPInfo = mcp.BuildMCPInfo

// NewRestPublicHandler 创建restHandler实例
// servicePort 用于推导沙箱回访地址；沙箱在集群内，走不了浏览器侧的网关地址。
func NewRestPublicHandler(logger interfaces.Logger, servicePort int) interfaces.HTTPRouterInterface {
	return &restPublicHandler{
		Hydra:                          drivenadapters.NewHydra(),
		AppKeys:                        drivenadapters.NewAppKeyVerifier(),
		KnRetrievalHandler:             knretrieval.NewKnRetrievalHandler(),
		MCPHandler:                     mcp.NewMCPHandler(),
		PTCMCPHandler:                  newPTCMCPHandlerOrNil(logger),
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
	mws = append(mws, middlewareRequestLog(r.Logger), middlewareTrace, sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware(), middlewareIntrospectVerify(r.Hydra, r.AppKeys), middlewareResponseFormat(), middlewareLifecycle(r.LifecycleClient))
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
	engine.POST("/kn/search_instance", r.KnSearchHandler.SearchInstance)
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
	toolkit, err := mcp.BuildPTCToolkit(publicEndpointURL(c.Request, mcpPath), r.ServicePort)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toolkit)
}

// handleMCP 在 MCP catch-all 路由内分流。
//
// 路径布局（两台 MCP Server，接入时二选一）：
//
//	/mcp                 MCP，二十来个业务工具逐个暴露
//	/mcp/info            文档：描述 /mcp
//	/mcp/ptc             MCP，只有 run_code / run_shell 加生命周期
//	/mcp/ptc/info        文档：描述 /mcp/ptc
//	/mcp/ptc/toolkit     自己实现 PTC 的素材（stub / digest / 工具表）
//	/mcp/toolkit         /mcp/ptc/toolkit 的旧别名，已弃用
//
// 分流顺序是有意的：**精确匹配必须排在 /ptc 的前缀判断之前**。mcp-go 按前缀吃
// 路径，把前缀判断提前会让 /mcp/ptc/toolkit 被当成一次 MCP 调用交给 PTC Server，
// 然后 404——而且是静默的，看起来就像端点没上线。
func (r *restPublicHandler) handleMCP(c *gin.Context) {
	path := c.Param("path")
	isGet := c.Request.Method == http.MethodGet

	switch {
	case isGet && path == ptcToolkitPath:
		r.handlePTCToolkit(c)
		return
	// 旧路径。它描述的是 PTC 那套，却挂在 /mcp 下面，读起来像在描述 /mcp——
	// 与 /mcp/info 的对称关系是错的。保留只为兼容先上线的客户端。
	case isGet && path == legacyToolkitPath:
		r.handlePTCToolkit(c)
		return
	case isGet && path == ptcInfoPath:
		r.replyMCPInfo(c, publicEndpointURL(c.Request, ptcMCPPath))
		return
	case isGet && path == mcpInfoPath:
		r.replyMCPInfo(c, mcpEndpointURL(c.Request))
		return
	case strings.HasPrefix(path, ptcPathPrefix):
		if r.PTCMCPHandler == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "PTC MCP endpoint is unavailable"})
			return
		}
		r.PTCMCPHandler.ServeHTTP(c.Writer, c.Request)
		return
	}
	r.MCPHandler.ServeHTTP(c.Writer, c.Request)
}

// MCP catch-all 之下的子路径。集中在一处，好让分流顺序与这份清单对着看。
const (
	mcpPath           = "/mcp"
	ptcMCPPath        = "/mcp/ptc"
	ptcPathPrefix     = "/ptc"
	mcpInfoPath       = "/info"
	ptcInfoPath       = "/ptc/info"
	ptcToolkitPath    = "/ptc/toolkit"
	legacyToolkitPath = "/toolkit"
)

// replyMCPInfo 输出某个 MCP 端点的自描述文档。
//
// 两个 info 端点（/mcp/info 与 /mcp/ptc/info）共用这一份实现，差别只在 endpoint。
func (r *restPublicHandler) replyMCPInfo(c *gin.Context, endpoint string) {
	info, err := buildMCPInfo(endpoint)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Errorf("BuildMCPInfo failed: %v", err)
		}
		sharedrest.MarkLocalizedCacheableResponse(c)
		infrarest.ReplyError(c, infraerrors.NewHTTPError(
			c.Request.Context(), http.StatusInternalServerError, infraerrors.ErrExtMCPInfoBuildFailed, nil))
		return
	}
	c.JSON(http.StatusOK, info)
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

// newPTCMCPHandlerOrNil 装配 PTC MCP 端点，失败只记日志并返回 nil。
//
// PTC 依赖内嵌的工具元数据渲染工具包，读失败时不该把整个服务拖垮——主工具面
// 与全部 REST 端点与它无关。该路由随后返回 503，故障是可见的。
func newPTCMCPHandlerOrNil(logger interfaces.Logger) http.Handler {
	handler, err := mcp.NewPTCMCPHandler()
	if err != nil {
		if logger != nil {
			logger.Errorf("[RestPublicHandler] PTC MCP endpoint unavailable: %v", err)
		}
		return nil
	}
	return handler
}
