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
	"net/http"

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
)

const (
	serverName                      = "context-loader"
	serverVersion                   = "1.0.0"
	endpointPath                    = "/api/agent-retrieval/v1/mcp"
	toolKeySearchSchema             = "search_schema"
	toolKeyQueryObjectInstance      = "query_object_instance"
	toolKeyQueryInstanceSubgraph    = "query_instance_subgraph"
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
)

// serverInstructions is returned at MCP initialize. It gives the LLM a
// shared exploration order and query-routing guide once, instead of
// repeating it across every tool description.
const serverInstructions = `ContextLoader 知识网络查询工具集使用指南。

探索顺序：
1. list_knowledge_networks 获取 kn_id（其余工具都需要 kn_id）。
2. 摸清 schema，两条路二选一：
   - 自然语言按需找对象类 → search_schema（返回 ot_id、属性、condition_operations、data_source.id）。默认精简（schema_brief=true，省约 70%，够写查询）；要属性备注/主键/标签的完整 Schema 才传 schema_brief=false。
   - 通读整网结构 → get_kn_detail（默认 summary：骨架 + 属性名，体积小）；要某对象完整字段映射再 get_object_types(ids)，要关系 mapping_rules 再 get_relation_types(ids)。别一上来就 detail_level=full。
3. 按查询类型选工具（见下）。

查询分流：
- 单对象类过滤 + 排序 + 分页（field op value，可 and/or 组合）→ query_object_instance；算子白名单以对象类的 condition_operations 为准。
- 聚合 / 统计 / 排名（SUM、COUNT、AVG、GROUP BY、按聚合值排序、跨表 join）→ run_sql（MySQL 只读 SQL）；表名用占位符 {{.<data_source.id>}}，<data_source.id> 必须替换成 search_schema 返回的真实 id 值（禁止照抄字面 resource_id；JOIN 多表时每个表用各自不同的 id）；列名用 search_schema 的 data_property.column（物理列，需 include_columns=true 获取），不是 name（逻辑名）。query_object_instance 不支持聚合。
- 沿关系多跳取子图 → query_instance_subgraph。
- 对象可执行行动召回 → get_action_info。

指标取数（OT 优先，三选一，禁止用 run_sql 重写已建模指标的口径）：
1. 先锁定对象类：search_schema 或 get_kn_detail（summary 里 related_metric_count>0 的对象类才有指标）。
2. get_object_types(ids) 看该对象类下有什么：logic_properties（data_source.type=metric）是实例级，related_metrics 是这个对象类 scope 下的全部指标（含未绑逻辑属性的）。
3. 选定后计算：
   - 实例级 + 已绑逻辑属性 → get_logic_properties_values
   - 类级 / 未绑逻辑属性 → query_metric（传 metric_id，可选 condition / analysis_dimensions / time）
   - 指标压根没建模，才用 run_sql 现算

数据层直查（资源未建成对象类、或只想绕本体直查数据时）：
- list_resources 列出账户可见的数据资源（resource_id、name、type、catalog_id），可按 catalog_id / type 过滤。
- describe_resource 取某 resource 的物理列（columns）与 connector_type。
- 然后 run_sql：表名用占位符 {{.<resource_id>}}，列名用 describe_resource 返回的物理列名。
即数据层链路：list_resources → describe_resource → run_sql（无需 search_schema/对象类）。与本体路（search_schema）互补，两者都喂给 run_sql。

提示：聚合类问题（如「每个 X 的 Y 总数/排名」）直接走 run_sql，不要用 query_object_instance 的 sort 近似。

run_sql 语法边界：仅写单条 SELECT；可用同一 catalog 内的 JOIN、WHERE、GROUP BY/HAVING、ORDER BY、LIMIT 与常用聚合函数。不得使用 WITH/CTE、UNION/INTERSECT/EXCEPT、多语句、写入/DDL 或跨 catalog join；子查询和窗口函数不在当前兼容性承诺内。

run_sql 占位符示例（id 必须来自 search_schema / list_resources 的真实返回值，逐表替换，别照抄 'resource_id' 字面量；JOIN 多表 = 多个不同 id）：
  search_schema("进球") → data_source.id = "GOALS_RID"
  search_schema("赛事") → data_source.id = "TOURN_RID"
  run_sql:
    SELECT t.tournament_name, g.family_name, COUNT(*) AS c
    FROM {{.GOALS_RID}} g
    JOIN {{.TOURN_RID}} t ON g.tournament_id = t.tournament_id
    GROUP BY t.tournament_name, g.family_name ORDER BY c DESC
  其中 GOALS_RID / TOURN_RID 是上面两次 search_schema 各自返回的真实 data_source.id（点可选：{{id}} 与 {{.id}} 等价）。`

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
	srv, _ := newMCPServer(lifecycleClient)
	return server.NewStreamableHTTPServer(srv,
		// The incoming ctx already carries the MCP client session; returning
		// r.Context() outright would drop it, and the session-level lifecycle
		// fallback would see every call as sessionless. Carry the gin
		// middleware's values across instead of replacing the whole context.
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return common.CopyRequestScopedValues(r.Context(), ctx)
		}),
		server.WithEndpointPath(endpointPath),
	)
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
	localeBundle := loadMCPLocaleBundle(mcpLocaleFromEnv())
	b := newToolBuilder(localeBundle)

	knSearchService := knsearch.NewKnSearchService()
	b.add(toolKeySearchSchema, handleSearchSchema(knSearchService))

	ontologyQuery := drivenadapters.NewOntologyQueryAccess()
	b.add(toolKeyQueryObjectInstance, handleQueryObjectInstance(ontologyQuery))

	knQuerySubgraphService := logicsKqs.NewKnQuerySubgraphService()
	b.add(toolKeyQueryInstanceSubgraph, handleQueryInstanceSubgraph(knQuerySubgraphService))

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

	// 技能面：find_skills 只回 id/名/描述，下面三条才是拿到 id 之后能走的路。
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
	return mcpServer, b
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
