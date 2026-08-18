// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package mcp provides Streamable HTTP MCP Server for Agent Retrieval.
package mcp

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/extension/mcptool"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	logicsKar "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knactionrecall"
	logicsFs "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knfindskills"
	logicsKlp "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knlogicpropertyresolver"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	logicsKqs "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knsearch"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const (
	serverName                      = "context-loader"
	serverVersion                   = "1.0.0"
	endpointPath                    = "/api/agent-retrieval/v1/mcp"
	toolKeySearchSchema             = "search_schema"
	toolKeySearchInstance           = "search_instance"
	toolKeyQueryObjectInstance      = "query_object_instance"
	toolKeyQueryInstanceSubgraph    = "query_instance_subgraph"
	toolKeyExploreSubgraph          = "explore_subgraph"
	toolKeyGetLogicPropertiesValues = "get_logic_properties_values"
	toolKeyQueryMetric              = "query_metric"
	toolKeyGetActionInfo            = "get_action_info"
	toolKeyExecuteAction            = "execute_action"
	toolKeyGetActionExecution       = "get_action_execution"
	toolKeyListActionExecutions     = "list_action_executions"
	toolKeyFindSkills               = "find_skills"
	toolKeyListKnowledgeNetworks    = "list_knowledge_networks"
	toolKeyGetKnDetail              = "get_kn_detail"
	toolKeyGetObjectTypes           = "get_object_types"
	toolKeyGetRelationTypes         = "get_relation_types"
	toolKeyRunSQL                   = "run_sql"
	toolKeyListResources            = "list_resources"
	toolKeyDescribeResource         = "describe_resource"
	toolKeyListSkills               = "list_skills"
	toolKeyGetSkillContent          = "get_skill_content"
	toolKeyReadSkillFile            = "read_skill_file"
	toolKeyExecuteSkill             = "execute_skill"
	// Keep locale tracking aligned with the bounded lifetime of mcp-go's
	// in-memory session state.
	mcpSessionIdleTTL = 30 * time.Minute
)

// NewMCPHandler creates an http.Handler for the MCP Streamable HTTP Server.
// Tool metadata comes from schemas/tools_meta.json; schemas from schemas/*.json.
// NewMCPHandler creates an http.Handler for the MCP Streamable HTTP Server.
// Tool metadata comes from schemas/tools_meta.json; schemas from schemas/*.json.
//
// The tool set is fixed here, so this runs after the assembly registry is
// frozen — app.Run freezes first, then builds handlers. What each caller is
// shown is decided per request against the licence, not here.
func NewMCPHandler() http.Handler {
	return NewMCPHandlerWithLifecycle(bkntrace.NewLifecycleClientFromEnv())
}

func NewMCPHandlerWithLifecycle(lifecycleClient *bkntrace.LifecycleClient) http.Handler {
	return newLocalizedMCPHandler(lifecycleClient)
}

type localizedMCPHandler struct {
	handlers       map[string]http.Handler
	sessionLocales sync.Map // Mcp-Session-Id -> mcpSessionLocale
}

type mcpSessionLocale struct {
	locale   string
	lastUsed time.Time
}

func newLocalizedMCPHandler(lifecycleClient *bkntrace.LifecycleClient) http.Handler {
	handlers := make(map[string]http.Handler, 2)
	for _, locale := range []sharedrest.Language{sharedrest.SimplifiedChinese, sharedrest.AmericanEnglish} {
		srv, _ := newMCPServerForLocale(lifecycleClient, string(locale))
		handlers[normalizeMCPLocale(string(locale))] = newMCPStreamableHTTPHandler(srv, endpointPath)
	}
	return &localizedMCPHandler{handlers: handlers}
}

func newMCPStreamableHTTPHandler(srv *server.MCPServer, path string) http.Handler {
	return server.NewStreamableHTTPServer(srv,
		// The incoming ctx already carries the MCP client session; returning
		// r.Context() outright would drop it, and the session-level lifecycle
		// fallback would see every call as sessionless. Carry the gin
		// middleware's values across instead of replacing the whole context.
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return common.CopyRequestScopedValues(r.Context(), ctx)
		}),
		server.WithSessionIdleTTL(mcpSessionIdleTTL),
		server.WithEndpointPath(path),
	)
}

func (h *localizedMCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(server.HeaderKeySessionID)
	now := time.Now()
	h.cleanupExpiredSessionLocales(now)
	locale := h.localeForRequest(r, sessionID, now)
	handler, ok := h.handlers[locale]
	if !ok {
		handler = h.handlers[defaultMCPLocale]
	}

	// All tool handlers read the effective locale from request context. Pin it
	// to the session value so a later Accept-Language header cannot make tool
	// results disagree with the session's initialized catalog.
	r = r.WithContext(common.SetLanguageToCtx(r.Context(), common.Language(locale)))
	handler.ServeHTTP(w, r)
	if initializedSessionID := w.Header().Get(server.HeaderKeySessionID); initializedSessionID != "" {
		h.sessionLocales.Store(initializedSessionID, mcpSessionLocale{locale: locale, lastUsed: now})
	}
	if r.Method == http.MethodDelete && sessionID != "" {
		h.sessionLocales.Delete(sessionID)
	}
}

func (h *localizedMCPHandler) localeForRequest(r *http.Request, sessionID string, now time.Time) string {
	if sessionID != "" {
		if value, ok := h.sessionLocales.Load(sessionID); ok {
			session := value.(mcpSessionLocale)
			session.lastUsed = now
			h.sessionLocales.Store(sessionID, session)
			return session.locale
		}
	}
	return normalizeMCPLocale(string(sharedrest.ResolveLanguage(r.Header.Get(sharedrest.AcceptLanguageHeader))))
}

func (h *localizedMCPHandler) cleanupExpiredSessionLocales(now time.Time) {
	h.sessionLocales.Range(func(key, value any) bool {
		session, ok := value.(mcpSessionLocale)
		if !ok || now.Sub(session.lastUsed) >= mcpSessionIdleTTL {
			h.sessionLocales.Delete(key)
		}
		return true
	})
}

// newMCPServer assembles the tool surface. Split out of NewMCPHandler so tests
// can read the assembled set directly instead of driving a JSON-RPC handshake
// to find out what was registered.
//
// It returns the builder for those tests only — the serving path discards it.
// /mcp/info does NOT go through the builder: BuildMCPInfo is called per request
// from the REST handler, which has no access to one, and constructing a builder
// there would mean building all sixteen services again. It reads the same facts
// from mcptool instead (Extras / DecoratorFor / Allowed), and
// TestInfoAndToolsListAgree… pins the two answers together.
func newMCPServer(lifecycleClient *bkntrace.LifecycleClient) (*server.MCPServer, *toolBuilder) {
	return newMCPServerForLocale(lifecycleClient, mcpLocaleFromEnv())
}

func newMCPServerForLocale(lifecycleClient *bkntrace.LifecycleClient, locale string) (*server.MCPServer, *toolBuilder) {
	localeBundle := loadMCPLocaleBundle(locale)
	b := newToolBuilder(localeBundle)

	knSearchService := knsearch.NewKnSearchService()
	b.add(toolKeySearchSchema, handleSearchSchema(knSearchService))
	b.add(toolKeySearchInstance, handleSearchInstance(knSearchService))

	ontologyQuery := drivenadapters.NewOntologyQueryAccess()
	b.add(toolKeyQueryObjectInstance, handleQueryObjectInstance(ontologyQuery))

	knQuerySubgraphService := logicsKqs.NewKnQuerySubgraphService()
	b.add(toolKeyQueryInstanceSubgraph, handleQueryInstanceSubgraph(knQuerySubgraphService))
	b.add(toolKeyExploreSubgraph, handleExploreSubgraph(knQuerySubgraphService))

	getLogicPropertiesValuesService := logicsKlp.NewKnLogicPropertyResolverService()
	b.add(toolKeyGetLogicPropertiesValues, handleGetLogicPropertiesValues(getLogicPropertiesValuesService))

	getActionInfoService := logicsKar.NewKnActionRecallService()
	b.add(toolKeyGetActionInfo, handleGetActionInfo(getActionInfoService))
	b.add(toolKeyExecuteAction, handleExecuteAction(getActionInfoService))
	b.add(toolKeyGetActionExecution, handleGetActionExecution(getActionInfoService))
	b.add(toolKeyListActionExecutions, handleListActionExecutions(getActionInfoService))

	findSkillsService := logicsFs.NewFindSkillsService()
	b.add(toolKeyFindSkills, handleFindSkills(findSkillsService))

	metricsService := knmetrics.NewKnMetricsService()
	b.add(toolKeyQueryMetric, handleQueryMetric(metricsService))

	bknBackend := drivenadapters.NewBknBackendAccess()
	b.add(toolKeyListKnowledgeNetworks, handleListKnowledgeNetworks(bknBackend))
	b.add(toolKeyGetKnDetail, handleGetKnDetail(bknBackend, metricsService))
	b.add(toolKeyGetObjectTypes, handleGetObjectTypes(bknBackend, metricsService))
	b.add(toolKeyGetRelationTypes, handleGetRelationTypes(bknBackend))

	runSQLService := knrunsql.NewKnRunSQLService()
	b.add(toolKeyRunSQL, handleRunSQL(runSQLService))

	resourcesService := knresources.NewKnResourcesService()
	b.add(toolKeyListResources, handleListResources(resourcesService))
	b.add(toolKeyDescribeResource, handleDescribeResource(resourcesService))

	// The skill surface: find_skills returns only ID, name, and description; the
	// following tools are used after a client has selected a skill.
	skillsService := knskills.NewKnSkillsService()
	b.add(toolKeyListSkills, handleListSkills(skillsService))
	b.add(toolKeyGetSkillContent, handleGetSkillContent(skillsService))
	b.add(toolKeyReadSkillFile, handleReadSkillFile(skillsService))
	// execute_skill 是工具面唯一的命令执行通道，默认不装配（见 executeSkillEnabled）。
	if knskills.ExecuteEnabled() {
		b.add(toolKeyExecuteSkill, handleExecuteSkill(skillsService))
	}

	// The lifecycle tools are registered straight onto the server by the tracing
	// adapter rather than through the builder. Claim their advertised names all
	// the same, so an enterprise tool cannot shadow one of them — mcp-go's
	// AddTool replaces a same-named tool silently, and these are core capability.
	b.claimLifecycleNames()

	b.addExtras()
	b.verifyDecoratorsLanded()

	mcpServer := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(localeBundle.ServerInstructions()),
		// The licence gate goes first. mcp-go applies middlewares in reverse,
		// so the first one registered is the outermost and runs before the
		// lifecycle guard — an under-licensed enterprise tool must answer
		// "no such tool", not "conversation_required", or the paid surface
		// announces itself to anyone probing.
		server.WithToolHandlerMiddleware(mcptool.GateMiddleware()),
		server.WithToolHandlerMiddleware(lifecycleToolMiddleware(lifecycleClient)),
		// Not just a listing filter. Since mcp-go v0.55.0 the filter also runs
		// on tools/call, and a tool it drops is refused by mcp-go itself with
		// the same error code and text an unknown tool gets. That is what makes
		// an unlicensed enterprise tool indistinguishable from one that was
		// never built in — the gate middleware above still refuses it, but a
		// middleware's error can only ever surface as INTERNAL_ERROR, which is
		// a code an unknown tool never returns. Removing this line reopens that
		// difference; TestRefusalIsIndistinguishableFromAnUnknownTool is what
		// notices.
		server.WithToolFilter(b.filter),
	)
	registerLifecycleTools(mcpServer, lifecycleClient, localeBundle)
	b.attach(mcpServer)
	registerInlinePTCTools(mcpServer, localeBundle, locale)
	return mcpServer, b
}

// registerInlinePTCTools 把 run_code / run_shell 并进业务工具面。
//
// 为什么并在一起，而不是只留 /mcp/ptc 那个独立端点：实测同一道检索型问题，只给
// 业务工具是 9 次调用 258k token，只给 run_code 是 8 次 108k，两者都给是 3 次
// 96k。模型按任务性质自选——检索走工具，跨表编排走代码。此前独立端点的注释断言
// 「并列会让模型退化成逐个调工具」，那个判断是错的：检索型问题上业务工具本来就
// 更合适，选它不是退化。
//
// 并列还补上了代码模式「先定后见」的短板：模型可以先用 search_schema 看到真实的
// 返回结构，再照着写代码，不必凭 digest 猜字段名。
//
// 不受 EXECUTE_SKILL_ENABLED 约束，与 /mcp/ptc 端点一致，理由见
// rest_public_handler.go 上的说明。
//
// 装配失败只记日志：内嵌工具元数据读不出来时，不该连累其余二十来个业务工具。
func registerInlinePTCTools(mcpServer *server.MCPServer, localeBundle *mcpLocaleBundle, locale string) {
	toolkit, err := InlinePTCToolkit(defaultPTCServicePort, locale)
	if err != nil {
		log.Printf("WARN: inline PTC tools unavailable: %v; /mcp exposes business tools only", err)
		return
	}
	executor := drivenadapters.NewOperatorIntegrationClient()
	for _, tool := range toolkit.Tools {
		// schema 与展示元数据都走业务工具那条路：/mcp/info 读的是同两个来源，
		// 两处各拼一份的话它们迟早会不一致，而 /mcp/info 的用途正是让人不握手
		// 就看清工具面。描述是唯一的例外——工具面上给模型看的是按当前工具表
		// 动态渲染的 digest，tools_meta.json 里那句静态说明只够 /mcp/info 用。
		meta := localeBundle.ToolMeta(tool.Name)
		meta.Description = tool.Description
		input, output := tryLoadToolSchemas(localeBundle, tool.Name)
		mcpServer.AddTool(
			newToolWithSchemas(meta, input, output),
			handlePTCExecuteForLocale(executor, toolkit, tool, localeBundle),
		)
	}
}

// Prefix for this service's own `_meta` keys on a tool.
//
// MCP reserves modelcontextprotocol.io/ and mcp.dev/ for itself and asks
// everyone else to prefix with a domain they control, so these keys cannot
// collide with a future protocol field of the same short name.
const toolMetaKeyPrefix = "openbkn.ai/"

// Presentation hints that have no field of their own in the protocol. `title`
// does have one and is set on the tool directly.
const (
	toolMetaKeyGroup      = toolMetaKeyPrefix + "group"
	toolMetaKeyGroupTitle = toolMetaKeyPrefix + "group_title"
	toolMetaKeyOrder      = toolMetaKeyPrefix + "order"
)

func newToolWithSchemas(meta ToolMeta, input, output json.RawMessage) mcp.Tool {
	tool := mcp.NewToolWithRawSchema(meta.Name, meta.Description, input)
	tool.RawOutputSchema = output
	tool.Title = meta.Title
	if fields := toolDisplayMetaFields(meta); len(fields) > 0 {
		tool.Meta = &mcp.Meta{AdditionalFields: fields}
	}
	return tool
}

// toolDisplayMetaFields renders the non-protocol presentation hints as `_meta`
// entries, omitting whatever the tool did not declare — an absent key is how a
// client tells "ungrouped" from "grouped under the empty string".
func toolDisplayMetaFields(meta ToolMeta) map[string]any {
	fields := map[string]any{}
	if meta.Group != "" {
		fields[toolMetaKeyGroup] = meta.Group
	}
	if meta.GroupTitle != "" {
		fields[toolMetaKeyGroupTitle] = meta.GroupTitle
	}
	if meta.Order != 0 {
		fields[toolMetaKeyOrder] = meta.Order
	}
	return fields
}
