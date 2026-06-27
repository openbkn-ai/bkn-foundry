// Copyright 2026 openbkn.ai
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
	toolKeyFindSkills               = "find_skills"
	toolKeyListKnowledgeNetworks    = "list_knowledge_networks"
	toolKeyGetKnDetail              = "get_kn_detail"
	toolKeyRunSQL                   = "run_sql"
)

// serverInstructions is returned at MCP initialize. It gives the LLM a
// shared exploration order and query-routing guide once, instead of
// repeating it across every tool description.
const serverInstructions = `ContextLoader 知识网络查询工具集使用指南。

探索顺序：
1. list_knowledge_networks 获取 kn_id（其余工具都需要 kn_id）。
2. search_schema 用自然语言找对象类，返回 ot_id、属性、condition_operations、data_source.id。
3. 按查询类型选工具（见下）。

查询分流：
- 单对象类过滤 + 排序 + 分页（field op value，可 and/or 组合）→ query_object_instance；算子白名单以对象类的 condition_operations 为准。
- 聚合 / 统计 / 排名（SUM、COUNT、AVG、GROUP BY、按聚合值排序、跨表 join）→ run_sql（Trino 只读 SQL）；表名用占位符 {{.<data_source.id>}}，data_source.id 取自 search_schema。query_object_instance 不支持聚合。
- 沿关系多跳取子图 → query_instance_subgraph。
- 逻辑属性（指标/算子）计算 → get_logic_properties_values。
- 对象可执行行动召回 → get_action_info。

提示：聚合类问题（如「每个 X 的 Y 总数/排名」）直接走 run_sql，不要用 query_object_instance 的 sort 近似。`

// NewMCPHandler creates an http.Handler for the MCP Streamable HTTP Server.
// Tool metadata comes from schemas/tools_meta.json; schemas from schemas/*.json.
func NewMCPHandler() http.Handler {
	mcpServer := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	knSearchService := knsearch.NewKnSearchService()
	searchSchemaName, searchSchemaDesc := loadToolMeta(toolKeySearchSchema)
	searchSchemaInput, searchSchemaOutput := loadToolSchemas(toolKeySearchSchema)
	mcpServer.AddTool(
		newToolWithSchemas(searchSchemaName, searchSchemaDesc, searchSchemaInput, searchSchemaOutput),
		handleSearchSchema(knSearchService),
	)

	ontologyQuery := drivenadapters.NewOntologyQueryAccess()
	queryObjectInstanceName, queryObjectInstanceDesc := loadToolMeta(toolKeyQueryObjectInstance)
	qoiInput, qoiOutput := loadToolSchemas(toolKeyQueryObjectInstance)
	mcpServer.AddTool(
		newToolWithSchemas(queryObjectInstanceName, queryObjectInstanceDesc, qoiInput, qoiOutput),
		handleQueryObjectInstance(ontologyQuery),
	)

	knQuerySubgraphService := logicsKqs.NewKnQuerySubgraphService()
	queryInstanceSubgraphName, queryInstanceSubgraphDesc := loadToolMeta(toolKeyQueryInstanceSubgraph)
	qisInput, qisOutput := loadToolSchemas(toolKeyQueryInstanceSubgraph)
	mcpServer.AddTool(
		newToolWithSchemas(queryInstanceSubgraphName, queryInstanceSubgraphDesc, qisInput, qisOutput),
		handleQueryInstanceSubgraph(knQuerySubgraphService),
	)

	getLogicPropertiesValuesService := logicsKlp.NewKnLogicPropertyResolverService()
	getLogicPropertiesValuesName, getLogicPropertiesValuesDesc := loadToolMeta(toolKeyGetLogicPropertiesValues)
	glpvInput, glpvOutput := loadToolSchemas(toolKeyGetLogicPropertiesValues)
	mcpServer.AddTool(
		newToolWithSchemas(getLogicPropertiesValuesName, getLogicPropertiesValuesDesc, glpvInput, glpvOutput),
		handleGetLogicPropertiesValues(getLogicPropertiesValuesService),
	)

	getActionInfoService := logicsKar.NewKnActionRecallService()
	getActionInfoName, getActionInfoDesc := loadToolMeta(toolKeyGetActionInfo)
	gaiInput, gaiOutput := loadToolSchemas(toolKeyGetActionInfo)
	mcpServer.AddTool(
		newToolWithSchemas(getActionInfoName, getActionInfoDesc, gaiInput, gaiOutput),
		handleGetActionInfo(getActionInfoService),
	)

	findSkillsService := logicsFs.NewFindSkillsService()
	findSkillsName, findSkillsDesc := loadToolMeta(toolKeyFindSkills)
	fsInput, fsOutput := loadToolSchemas(toolKeyFindSkills)
	mcpServer.AddTool(
		newToolWithSchemas(findSkillsName, findSkillsDesc, fsInput, fsOutput),
		handleFindSkills(findSkillsService),
	)

	bknBackend := drivenadapters.NewBknBackendAccess()
	listKnName, listKnDesc := loadToolMeta(toolKeyListKnowledgeNetworks)
	listKnInput, listKnOutput := loadToolSchemas(toolKeyListKnowledgeNetworks)
	mcpServer.AddTool(
		newToolWithSchemas(listKnName, listKnDesc, listKnInput, listKnOutput),
		handleListKnowledgeNetworks(bknBackend),
	)

	getKnDetailName, getKnDetailDesc := loadToolMeta(toolKeyGetKnDetail)
	knDetailInput, knDetailOutput := loadToolSchemas(toolKeyGetKnDetail)
	mcpServer.AddTool(
		newToolWithSchemas(getKnDetailName, getKnDetailDesc, knDetailInput, knDetailOutput),
		handleGetKnDetail(bknBackend),
	)

	runSQLService := knrunsql.NewKnRunSQLService()
	runSQLName, runSQLDesc := loadToolMeta(toolKeyRunSQL)
	runSQLInput, runSQLOutput := loadToolSchemas(toolKeyRunSQL)
	mcpServer.AddTool(
		newToolWithSchemas(runSQLName, runSQLDesc, runSQLInput, runSQLOutput),
		handleRunSQL(runSQLService),
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
