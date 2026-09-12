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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knlogicpropertyresolver"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knqueryobjectinstance"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knquerysubgraph"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knquerytools"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knsearch"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/knskills"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/kntools"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/driveradapters/mcp"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	infrarest "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsSkills "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

type restPublicHandler struct {
	Hydra                          interfaces.Hydra
	AppKeys                        interfaces.AppKeyVerifier
	MCPHandler                     http.Handler
	KnLogicPropertyResolverHandler knlogicpropertyresolver.KnLogicPropertyResolverHandler
	KnActionRecallHandler          knactionrecall.KnActionRecallHandler
	KnQueryObjectInstanceHandler   knqueryobjectinstance.KnQueryObjectInstanceHandler
	KnQuerySubgraphHandler         knquerysubgraph.KnQuerySubgraphHandler
	KnSearchHandler                knsearch.KnSearchHandler
	KnQueryToolsHandler            knquerytools.KnQueryToolsHandler
	KnSkillsHandler                knskills.KnSkillsHandler
	KnToolsHandler                 kntools.KnToolsHandler
	Logger                         interfaces.Logger
	LifecycleClient                *bkntrace.LifecycleClient
}

var buildMCPInfo = mcp.BuildMCPInfoForLocale

// NewRestPublicHandler createrestHandlerinstance.
// sandboxPort is used to derive the sandbox return address; the sandbox is within the cluster and cannot reach the gateway address on the browser side.
func NewRestPublicHandler(logger interfaces.Logger, sandboxPort int) interfaces.HTTPRouterInterface {
	return &restPublicHandler{
		Hydra:                          drivenadapters.NewHydra(),
		AppKeys:                        drivenadapters.NewAppKeyVerifier(),
		MCPHandler:                     mcp.NewMCPHandlerForSandboxPort(sandboxPort),
		KnLogicPropertyResolverHandler: knlogicpropertyresolver.NewKnLogicPropertyResolverHandler(),
		KnActionRecallHandler:          knactionrecall.NewKnActionRecallHandler(),
		KnQueryObjectInstanceHandler:   knqueryobjectinstance.NewKnQueryObjectInstanceHandler(),
		KnQuerySubgraphHandler:         knquerysubgraph.NewKnQuerySubgraphHandler(),
		KnSearchHandler:                knsearch.NewKnSearchHandler(),
		KnQueryToolsHandler:            knquerytools.NewKnQueryToolsHandler(),
		KnSkillsHandler:                knskills.NewKnSkillsHandler(),
		KnToolsHandler:                 kntools.NewKnToolsHandler(),
		Logger:                         logger,
		LifecycleClient:                bkntrace.NewLifecycleClientFromEnv(),
	}
}

// RegisterRouter registers public routes.
func (r *restPublicHandler) RegisterRouter(engine *gin.RouterGroup) {
	mws := []gin.HandlerFunc{}
	mws = append(mws, middlewareRequestLog(r.Logger), middlewareTrace, sharedrest.LanguageMiddleware(), sharedrest.PrivateNoCacheMiddleware(), middlewareIntrospectVerify(r.Hydra, r.AppKeys), middlewareResponseFormat(), middlewareLifecycle(r.LifecycleClient))
	engine.Use(mws...)

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

	// Skill surface: list, read, and execute after search_capabilities discovery.
	engine.POST("/kn/list_skills", r.KnSkillsHandler.ListSkills)
	engine.POST("/kn/get_skill_content", r.KnSkillsHandler.GetSkillContent)
	engine.POST("/kn/read_skill_file", r.KnSkillsHandler.ReadSkillFile)
	// Use the same gate as the MCP tool surface. When disabled, do not register
	// the route instead of registering it and rejecting requests later.
	if logicsSkills.ExecuteEnabled() {
		engine.POST("/kn/execute_skill", r.KnSkillsHandler.ExecuteSkill)
	}

	// One entry over every kind the network mounted, then run what it found. find_skills and
	// search_tools were this call with types pinned and were removed once callers moved (#1401).
	engine.POST("/kn/search_capabilities", r.KnToolsHandler.SearchCapabilities)
	engine.POST("/kn/execute_tool", r.KnToolsHandler.ExecuteTool)

	// MCP Server (Bearer token auth, supports Cursor/Claude Desktop)
	// GET /mcp/info returns the self-description (tool catalog and connection
	// details). All other
	// requests use standard MCP Streamable HTTP.
	engine.Any("/mcp/*path", r.handleMCP)
}

// handleMCP dispatches requests inside the MCP catch-all route.
//
// Path layout:
//
//	/mcp                 MCP with business tools and run_code / run_shell
//	/mcp/info            Self-description for /mcp
//
// There used to be a second server at /mcp/ptc exposing only run_code and
// run_shell, plus a GET /mcp/ptc/toolkit serving the assets a client needed to
// implement that flow itself. Both are gone: the execution tools are on /mcp
// through registerInlinePTCTools, so a host that wants them no longer has to
// choose an endpoint, and the assets are generated into the sandbox image at
// build time by cmd/ptc-stub rather than fetched. The rendering they shared is
// untouched - only the two HTTP surfaces are.
func (r *restPublicHandler) handleMCP(c *gin.Context) {
	if c.Request.Method == http.MethodGet && c.Param("path") == mcpInfoPath {
		r.replyMCPInfo(c, mcpEndpointURL(c.Request))
		return
	}
	r.MCPHandler.ServeHTTP(c.Writer, c.Request)
}

// MCP catch-all subpaths are centralized so this list matches dispatch order.
const (
	mcpPath     = "/mcp"
	mcpInfoPath = "/info"
)

// replyMCPInfo returns the self-description for the MCP endpoint.
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
//
//nolint:unused // Retained for public endpoint URL construction.
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
