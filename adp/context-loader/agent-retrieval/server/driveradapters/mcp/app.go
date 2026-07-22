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

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/drivenadapters"
	logicsKar "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knactionrecall"
	logicsFs "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knfindskills"
	logicsKlp "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knlogicpropertyresolver"
	logicsKqs "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knsearch"
)

const (
	serverName                      = "context-loader"
	serverVersion                   = "1.0.0"
	endpointPath                    = "/api/agent-retrieval/v1/mcp"
	toolKeySearchSchema             = "search_schema"
	toolKeyQueryObjectInstance      = "query_object_instance"
	toolKeyQueryInstanceSubgraph    = "query_instance_subgraph"
	toolKeyGetLogicPropertiesValues = "get_logic_properties_values"
	toolKeyGetActionInfo            = "get_action_info"
	toolKeyExecuteAction           = "execute_action"
	toolKeyGetActionExecution      = "get_action_execution"
	toolKeyListActionExecutions    = "list_action_executions"
	toolKeyFindSkills               = "find_skills"
	toolKeyListKnowledgeNetworks    = "list_knowledge_networks"
	toolKeyGetKnDetail              = "get_kn_detail"
	toolKeyGetObjectTypes           = "get_object_types"
	toolKeyGetRelationTypes         = "get_relation_types"
	toolKeyRunSQL                   = "run_sql"
	toolKeyListResources            = "list_resources"
	toolKeyDescribeResource         = "describe_resource"
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
- 聚合 / 统计 / 排名（SUM、COUNT、AVG、GROUP BY、按聚合值排序、跨表 join）→ run_sql（Trino 只读 SQL）；表名用占位符 {{.<data_source.id>}}，<data_source.id> 必须替换成 search_schema 返回的真实 id 值（禁止照抄字面 resource_id；JOIN 多表时每个表用各自不同的 id）；列名用 search_schema 的 data_property.column（物理列，需 include_columns=true 获取），不是 name（逻辑名）。query_object_instance 不支持聚合。
- 沿关系多跳取子图 → query_instance_subgraph。
- 逻辑属性（指标/算子）计算 → get_logic_properties_values。
- 对象可执行行动召回 → get_action_info。

数据层直查（资源未建成对象类、或只想绕本体直查数据时）：
- list_resources 列出账户可见的数据资源（resource_id、name、type、catalog_id），可按 catalog_id / type 过滤。
- describe_resource 取某 resource 的物理列（columns）与 connector_type。
- 然后 run_sql：表名用占位符 {{.<resource_id>}}，列名用 describe_resource 返回的物理列名。
即数据层链路：list_resources → describe_resource → run_sql（无需 search_schema/对象类）。与本体路（search_schema）互补，两者都喂给 run_sql。

提示：聚合类问题（如「每个 X 的 Y 总数/排名」）直接走 run_sql，不要用 query_object_instance 的 sort 近似。

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
func NewMCPHandler() http.Handler {
	localeBundle := loadMCPLocaleBundle(mcpLocaleFromEnv())
	mcpServer := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(localeBundle.ServerInstructions()),
	)

	knSearchService := knsearch.NewKnSearchService()
	searchSchemaName, searchSchemaDesc := localeBundle.ToolMeta(toolKeySearchSchema)
	searchSchemaInput, searchSchemaOutput := localeBundle.ToolSchemas(toolKeySearchSchema)
	mcpServer.AddTool(
		newToolWithSchemas(searchSchemaName, searchSchemaDesc, searchSchemaInput, searchSchemaOutput),
		handleSearchSchema(knSearchService),
	)

	ontologyQuery := drivenadapters.NewOntologyQueryAccess()
	queryObjectInstanceName, queryObjectInstanceDesc := localeBundle.ToolMeta(toolKeyQueryObjectInstance)
	qoiInput, qoiOutput := localeBundle.ToolSchemas(toolKeyQueryObjectInstance)
	mcpServer.AddTool(
		newToolWithSchemas(queryObjectInstanceName, queryObjectInstanceDesc, qoiInput, qoiOutput),
		handleQueryObjectInstance(ontologyQuery),
	)

	knQuerySubgraphService := logicsKqs.NewKnQuerySubgraphService()
	queryInstanceSubgraphName, queryInstanceSubgraphDesc := localeBundle.ToolMeta(toolKeyQueryInstanceSubgraph)
	qisInput, qisOutput := localeBundle.ToolSchemas(toolKeyQueryInstanceSubgraph)
	mcpServer.AddTool(
		newToolWithSchemas(queryInstanceSubgraphName, queryInstanceSubgraphDesc, qisInput, qisOutput),
		handleQueryInstanceSubgraph(knQuerySubgraphService),
	)

	getLogicPropertiesValuesService := logicsKlp.NewKnLogicPropertyResolverService()
	getLogicPropertiesValuesName, getLogicPropertiesValuesDesc := localeBundle.ToolMeta(toolKeyGetLogicPropertiesValues)
	glpvInput, glpvOutput := localeBundle.ToolSchemas(toolKeyGetLogicPropertiesValues)
	mcpServer.AddTool(
		newToolWithSchemas(getLogicPropertiesValuesName, getLogicPropertiesValuesDesc, glpvInput, glpvOutput),
		handleGetLogicPropertiesValues(getLogicPropertiesValuesService),
	)

	getActionInfoService := logicsKar.NewKnActionRecallService()
	getActionInfoName, getActionInfoDesc := localeBundle.ToolMeta(toolKeyGetActionInfo)
	gaiInput, gaiOutput := localeBundle.ToolSchemas(toolKeyGetActionInfo)
	mcpServer.AddTool(
		newToolWithSchemas(getActionInfoName, getActionInfoDesc, gaiInput, gaiOutput),
		handleGetActionInfo(getActionInfoService),
	)

	executeActionName, executeActionDesc := localeBundle.ToolMeta(toolKeyExecuteAction)
	eaInput, eaOutput := localeBundle.ToolSchemas(toolKeyExecuteAction)
	mcpServer.AddTool(
		newToolWithSchemas(executeActionName, executeActionDesc, eaInput, eaOutput),
		handleExecuteAction(getActionInfoService),
	)

	getActionExecutionName, getActionExecutionDesc := localeBundle.ToolMeta(toolKeyGetActionExecution)
	gaeInput, gaeOutput := localeBundle.ToolSchemas(toolKeyGetActionExecution)
	mcpServer.AddTool(
		newToolWithSchemas(getActionExecutionName, getActionExecutionDesc, gaeInput, gaeOutput),
		handleGetActionExecution(getActionInfoService),
	)

	listActionExecutionsName, listActionExecutionsDesc := localeBundle.ToolMeta(toolKeyListActionExecutions)
	laeInput, laeOutput := localeBundle.ToolSchemas(toolKeyListActionExecutions)
	mcpServer.AddTool(
		newToolWithSchemas(listActionExecutionsName, listActionExecutionsDesc, laeInput, laeOutput),
		handleListActionExecutions(getActionInfoService),
	)

	findSkillsService := logicsFs.NewFindSkillsService()
	findSkillsName, findSkillsDesc := localeBundle.ToolMeta(toolKeyFindSkills)
	fsInput, fsOutput := localeBundle.ToolSchemas(toolKeyFindSkills)
	mcpServer.AddTool(
		newToolWithSchemas(findSkillsName, findSkillsDesc, fsInput, fsOutput),
		handleFindSkills(findSkillsService),
	)

	bknBackend := drivenadapters.NewBknBackendAccess()
	listKnName, listKnDesc := localeBundle.ToolMeta(toolKeyListKnowledgeNetworks)
	listKnInput, listKnOutput := localeBundle.ToolSchemas(toolKeyListKnowledgeNetworks)
	mcpServer.AddTool(
		newToolWithSchemas(listKnName, listKnDesc, listKnInput, listKnOutput),
		handleListKnowledgeNetworks(bknBackend),
	)

	getKnDetailName, getKnDetailDesc := localeBundle.ToolMeta(toolKeyGetKnDetail)
	knDetailInput, knDetailOutput := localeBundle.ToolSchemas(toolKeyGetKnDetail)
	mcpServer.AddTool(
		newToolWithSchemas(getKnDetailName, getKnDetailDesc, knDetailInput, knDetailOutput),
		handleGetKnDetail(bknBackend),
	)

	getObjectTypesName, getObjectTypesDesc := loadToolMeta(toolKeyGetObjectTypes)
	objectTypesInput, objectTypesOutput := loadToolSchemas(toolKeyGetObjectTypes)
	mcpServer.AddTool(
		newToolWithSchemas(getObjectTypesName, getObjectTypesDesc, objectTypesInput, objectTypesOutput),
		handleGetObjectTypes(bknBackend),
	)

	getRelationTypesName, getRelationTypesDesc := loadToolMeta(toolKeyGetRelationTypes)
	relationTypesInput, relationTypesOutput := loadToolSchemas(toolKeyGetRelationTypes)
	mcpServer.AddTool(
		newToolWithSchemas(getRelationTypesName, getRelationTypesDesc, relationTypesInput, relationTypesOutput),
		handleGetRelationTypes(bknBackend),
	)

	runSQLService := knrunsql.NewKnRunSQLService()
	runSQLName, runSQLDesc := localeBundle.ToolMeta(toolKeyRunSQL)
	runSQLInput, runSQLOutput := localeBundle.ToolSchemas(toolKeyRunSQL)
	mcpServer.AddTool(
		newToolWithSchemas(runSQLName, runSQLDesc, runSQLInput, runSQLOutput),
		handleRunSQL(runSQLService),
	)

	resourcesService := knresources.NewKnResourcesService()
	listResourcesName, listResourcesDesc := localeBundle.ToolMeta(toolKeyListResources)
	listResourcesInput, listResourcesOutput := localeBundle.ToolSchemas(toolKeyListResources)
	mcpServer.AddTool(
		newToolWithSchemas(listResourcesName, listResourcesDesc, listResourcesInput, listResourcesOutput),
		handleListResources(resourcesService),
	)

	describeResourceName, describeResourceDesc := localeBundle.ToolMeta(toolKeyDescribeResource)
	describeResourceInput, describeResourceOutput := localeBundle.ToolSchemas(toolKeyDescribeResource)
	mcpServer.AddTool(
		newToolWithSchemas(describeResourceName, describeResourceDesc, describeResourceInput, describeResourceOutput),
		handleDescribeResource(resourcesService),
	)

	streamableServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return r.Context()
		}),
		server.WithEndpointPath(endpointPath),
	)

	return streamableServer
}

func newToolWithSchemas(name, description string, input, output json.RawMessage) mcp.Tool {
	tool := mcp.NewToolWithRawSchema(name, description, input)
	tool.RawOutputSchema = output
	return tool
}
