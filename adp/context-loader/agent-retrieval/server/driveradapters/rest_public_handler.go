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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
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

var buildMCPInfo = mcp.BuildMCPInfoForLocale

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

// RegisterRouter registers public routes.
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
	engine.POST("/kn/explore_subgraph", r.KnQuerySubgraphHandler.ExploreSubgraph)
	engine.POST("/kn/search_schema", r.KnSearchHandler.SearchSchema)
	engine.POST("/kn/search_instance", r.KnSearchHandler.SearchInstance)
	engine.POST("/kn/kn_search", r.KnSearchHandler.KnSearch)
	engine.POST("/kn/find_skills", r.KnFindSkillsHandler.FindSkills)

	// These are available both as MCP tools and through the operator-integration
	// toolbox (OpenAPI HTTP) entry point.
	engine.POST("/kn/run_sql", r.KnQueryToolsHandler.RunSQL)
	engine.POST("/kn/list_knowledge_networks", r.KnQueryToolsHandler.ListKnowledgeNetworks)
	engine.POST("/kn/get_kn_detail", r.KnQueryToolsHandler.GetKnDetail)
	engine.POST("/kn/get_object_types", r.KnQueryToolsHandler.GetObjectTypes)
	engine.POST("/kn/get_relation_types", r.KnQueryToolsHandler.GetRelationTypes)
	engine.POST("/kn/query_metric", r.KnQueryToolsHandler.QueryMetric)
	engine.POST("/kn/list_resources", r.KnQueryToolsHandler.ListResources)
	engine.POST("/kn/describe_resource", r.KnQueryToolsHandler.DescribeResource)

	// Skill surface: list, read, and execute after find_skills discovery.
	engine.POST("/kn/list_skills", r.KnSkillsHandler.ListSkills)
	engine.POST("/kn/get_skill_content", r.KnSkillsHandler.GetSkillContent)
	engine.POST("/kn/read_skill_file", r.KnSkillsHandler.ReadSkillFile)
	// Use the same gate as the MCP tool surface. When disabled, do not register
	// the route instead of registering it and rejecting requests later.
	if logicsSkills.ExecuteEnabled() {
		engine.POST("/kn/execute_skill", r.KnSkillsHandler.ExecuteSkill)
	}

	// MCP Server (Bearer token auth, supports Cursor/Claude Desktop)
	// GET /mcp/info returns the self-description (tool catalog and connection
	// details). GET /mcp/toolkit returns the code-mode representation; all other
	// requests use standard MCP Streamable HTTP.
	engine.Any("/mcp/*path", r.handleMCP)
}

// handlePTCToolkit returns the PTC toolkit for GET .../mcp/toolkit.
// Its contents change with the tool surface; clients cache it by version.
func (r *restPublicHandler) handlePTCToolkit(c *gin.Context) {
	toolkit, err := buildPTCToolkit(
		publicEndpointURL(c.Request, mcpPath), r.ServicePort, common.GetLanguageFromCtx(c.Request.Context()),
	)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Errorf("BuildPTCToolkit failed: %v", err)
		}
		sharedrest.MarkLocalizedCacheableResponse(c)
		infrarest.ReplyError(c, infraerrors.NewHTTPError(
			c.Request.Context(), http.StatusInternalServerError, infraerrors.ErrExtMCPPTCToolkitBuildFailed, nil))
		return
	}
	c.JSON(http.StatusOK, toolkit)
}

var buildPTCToolkit = mcp.BuildPTCToolkitForLocale

// handleMCP dispatches requests inside the MCP catch-all route.
//
// Path layout (choose one of the two MCP servers per integration):
//
//	/mcp                 MCP with individual business tools
//	/mcp/info            Self-description for /mcp
//	/mcp/ptc             MCP with run_code/run_shell and lifecycle tools
//	/mcp/ptc/info        Self-description for /mcp/ptc
//	/mcp/ptc/toolkit     PTC assets (stub, digest, and tool catalog)
//	/mcp/toolkit         Deprecated alias for /mcp/ptc/toolkit
//
// Exact matches must precede the /ptc prefix match. Otherwise mcp-go consumes
// /mcp/ptc/toolkit as an MCP request for the PTC server and returns a silent
// 404, making the endpoint appear unavailable.
func (r *restPublicHandler) handleMCP(c *gin.Context) {
	path := c.Param("path")
	isGet := c.Request.Method == http.MethodGet

	switch {
	case isGet && path == ptcToolkitPath:
		r.handlePTCToolkit(c)
		return
	// Legacy path retained only for clients released before /mcp/ptc/toolkit.
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
		// When the global gate is disabled, make this endpoint indistinguishable
		// from one that was never deployed.
		// PTC 不受 EXECUTE_SKILL_ENABLED 约束，端点常开。这是明确的产品决策：
		// 该开关按语义是技能执行的闸，而 run_code / run_shell 是另一种能力，
		// 共用一个开关会让想开技能执行的人被迫连任意代码执行一起开，反之亦然。
		// 代价是本部署没有关闭 PTC 的手段——沙箱侧的隔离因此成为必答项而非可选项。
		if r.PTCMCPHandler == nil {
			sharedrest.MarkLocalizedCacheableResponse(c)
			infrarest.ReplyError(c, infraerrors.NewHTTPError(
				c.Request.Context(), http.StatusServiceUnavailable, infraerrors.ErrExtMCPPTCUnavailable, nil))
			return
		}
		r.PTCMCPHandler.ServeHTTP(c.Writer, c.Request)
		return
	}
	r.MCPHandler.ServeHTTP(c.Writer, c.Request)
}

// MCP catch-all subpaths are centralized so this list matches dispatch order.
const (
	mcpPath           = "/mcp"
	ptcMCPPath        = "/mcp/ptc"
	ptcPathPrefix     = "/ptc"
	mcpInfoPath       = "/info"
	ptcInfoPath       = "/ptc/info"
	ptcToolkitPath    = "/ptc/toolkit"
	legacyToolkitPath = "/toolkit"
)

// replyMCPInfo returns the self-description for an MCP endpoint.
//
// /mcp/info and /mcp/ptc/info share this implementation; only the endpoint differs.
func (r *restPublicHandler) replyMCPInfo(c *gin.Context, endpoint string) {
	info, err := buildMCPInfo(endpoint, string(common.GetLanguageFromCtx(c.Request.Context())))
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

// mcpEndpointURL derives the public MCP endpoint by removing a trailing /info.
func mcpEndpointURL(req *http.Request) string {
	scheme := requestScheme(req)
	base := strings.TrimSuffix(req.URL.Path, "/info")
	return scheme + "://" + req.Host + base
}

// publicEndpointURL builds another public endpoint of this service from the
// route-group prefix. It is used where removing a suffix from the current URL
// is insufficient, such as /ptc/toolkit describing /mcp.
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

// newPTCMCPHandlerOrNil assembles the PTC MCP endpoint and returns nil on failure.
//
// PTC depends on embedded tool metadata. A failure must not prevent the main
// MCP surface or REST APIs from starting; the PTC route reports a visible 503.
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
