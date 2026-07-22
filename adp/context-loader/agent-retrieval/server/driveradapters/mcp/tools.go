// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"

	"github.com/creasty/defaults"
	validator "github.com/go-playground/validator/v10"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	logicsKqs "github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/logics/knsearch"
)

const (
	defaultResolveMaxRepairRounds = 1
	defaultResolveMaxConcurrency  = 4
)

// handleSearchSchema returns a tool handler for search_schema.
func handleSearchSchema(knSearchService knsearch.KnSearchService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, _ := common.GetAccountAuthContextFromCtx(ctx)

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		schemaReq := buildSearchSchemaReqFromMCP(req, authCtx)

		resp, err := knSearchService.SearchSchema(ctx, schemaReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// buildSearchSchemaReqFromMCP populates SearchSchemaReq from MCP transport.
func buildSearchSchemaReqFromMCP(req mcp.CallToolRequest, authCtx *interfaces.AccountAuthContext) *interfaces.SearchSchemaReq {
	schemaReq := &interfaces.SearchSchemaReq{}
	_ = bindArguments(req, schemaReq)

	// MCP（LLM）场景默认精简 Schema：未显式传 schema_brief 时用 brief，
	// 体积更小且已保留 data_source.id / 属性 name/type/condition_operations；
	// 需要属性备注/主键/标签的完整 Schema 时显式传 schema_brief=false。
	if schemaReq.SchemaBrief == nil {
		brief := true
		schemaReq.SchemaBrief = &brief
	}

	schemaReq.XKnID = getKnIDFromHeader(req)
	if authCtx != nil {
		schemaReq.XAccountID = authCtx.AccountID
		schemaReq.XAccountType = string(authCtx.AccountType)
	}
	return schemaReq
}

// handleQueryObjectInstance handles query_object_instance tool calls.
func handleQueryObjectInstance(ontologyQuery interfaces.DrivenOntologyQuery) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		queryReq := &interfaces.QueryObjectInstancesReq{}
		if err := bindArguments(req, queryReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		queryReq.KnID = getStringArg(req, "kn_id", queryReq.KnID)
		if queryReq.KnID == "" {
			queryReq.KnID = getKnIDFromHeader(req)
		}
		queryReq.OtID = getStringArg(req, "ot_id", queryReq.OtID)
		queryReq.IncludeTypeInfo = false
		queryReq.IncludeLogicParams = req.GetBool("include_logic_params", queryReq.IncludeLogicParams)
		if queryReq.Limit == 0 {
			queryReq.Limit = 10
		}
		if queryReq.KnID == "" || queryReq.OtID == "" {
			return mcp.NewToolResultError("kn_id and ot_id are required"), nil
		}
		if err := validator.New().Struct(queryReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := ontologyQuery.QueryObjectInstances(ctx, queryReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resp.ObjectConcept = nil
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleQueryInstanceSubgraph handles query_instance_subgraph tool calls.
func handleQueryInstanceSubgraph(service logicsKqs.KnQuerySubgraphService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		subgraphReq := &interfaces.QueryInstanceSubgraphReq{}
		if err := bindArguments(req, subgraphReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		subgraphReq.KnID = getStringArg(req, "kn_id", subgraphReq.KnID)
		if subgraphReq.KnID == "" {
			subgraphReq.KnID = getKnIDFromHeader(req)
		}
		subgraphReq.IncludeLogicParams = req.GetBool("include_logic_params", subgraphReq.IncludeLogicParams)
		if subgraphReq.RelationTypePaths == nil {
			return mcp.NewToolResultError("relation_type_paths is required"), nil
		}
		if subgraphReq.KnID == "" {
			return mcp.NewToolResultError("kn_id is required"), nil
		}

		resp, err := service.QueryInstanceSubgraph(ctx, subgraphReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetLogicPropertiesValues handles get_logic_properties_values tool calls.
func handleGetLogicPropertiesValues(service interfaces.IKnLogicPropertyResolverService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resolveReq := &interfaces.ResolveLogicPropertiesRequest{}
		if err := bindArguments(req, resolveReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if resolveReq.KnID == "" {
			resolveReq.KnID = getKnIDFromHeader(req)
		}
		resolveReq.AccountID = authCtx.AccountID
		resolveReq.AccountType = string(authCtx.AccountType)

		resolveReq.Options = &interfaces.ResolveOptions{
			ReturnDebug:     false,
			MaxRepairRounds: defaultResolveMaxRepairRounds,
			MaxConcurrency:  defaultResolveMaxConcurrency,
		}
		if err := validator.New().Struct(resolveReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.ResolveLogicProperties(ctx, resolveReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetActionInfo handles get_action_info tool calls.
func handleGetActionInfo(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		actionReq := &interfaces.KnActionRecallRequest{}
		if err := bindArguments(req, actionReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if actionReq.KnID == "" {
			actionReq.KnID = getKnIDFromHeader(req)
		}
		actionReq.AccountID = authCtx.AccountID
		actionReq.AccountType = string(authCtx.AccountType)

		if err := validator.New().Struct(actionReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.GetActionInfo(ctx, actionReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// get_action_info 始终返回 JSON：行动工具定义需机器可消费，忽略 response_format（TOON 会破坏结构）。
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleExecuteAction handles execute_action tool calls.
// 与 get_action_info 配对：Agent 先用 get_action_info 拿到 dynamic_params schema，
// 再用本工具填入真实动态参数值触发执行（异步，返回 execution_id）。
func handleExecuteAction(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		execReq := &interfaces.KnActionExecuteRequest{}
		if err := bindArguments(req, execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if execReq.KnID == "" {
			execReq.KnID = getKnIDFromHeader(req)
		}
		execReq.AccountID = authCtx.AccountID
		execReq.AccountType = string(authCtx.AccountType)

		if err := validator.New().Struct(execReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.ExecuteAction(ctx, execReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// execute_action 始终返回 JSON：execution_id 等需机器可消费。
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetActionExecution handles get_action_execution tool calls.
// 与 execute_action 配对：用 execute_action 返回的 execution_id 查询该次执行的 status 与 results。
func handleGetActionExecution(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		getReq := &interfaces.KnGetActionExecutionRequest{}
		if err := bindArguments(req, getReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if getReq.KnID == "" {
			getReq.KnID = getKnIDFromHeader(req)
		}
		getReq.AccountID = authCtx.AccountID
		getReq.AccountType = string(authCtx.AccountType)

		if err := validator.New().Struct(getReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.GetActionExecution(ctx, getReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleListActionExecutions handles list_action_executions tool calls.
// 列出行动执行历史（可按行动类型/状态/触发方式过滤，分页）。
func handleListActionExecutions(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		listReq := &interfaces.KnListActionExecutionsRequest{}
		if err := bindArguments(req, listReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if listReq.KnID == "" {
			listReq.KnID = getKnIDFromHeader(req)
		}
		listReq.AccountID = authCtx.AccountID
		listReq.AccountType = string(authCtx.AccountType)

		if err := validator.New().Struct(listReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.ListActionExecutions(ctx, listReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleListKnowledgeNetworks handles list_knowledge_networks tool calls.
// 用于让外部 Agent 发现可用的 kn_id（其余查询工具的前置）。
func handleListKnowledgeNetworks(bkn interfaces.BknBackendAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		listReq := &interfaces.ListKnReq{}
		if err := bindArguments(req, listReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if listReq.Limit == 0 {
			listReq.Limit = 20
		}

		resp, err := bkn.ListKnowledgeNetworks(ctx, listReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleRunSQL handles run_sql tool calls.
// 对知识网络挂载的数据资源执行只读 SQL（强制 SELECT-only），底层走共享 knrunsql 服务。
func handleRunSQL(svc knrunsql.KnRunSQLService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		sqlReq := &knrunsql.RunSQLReq{}
		if err := bindArguments(req, sqlReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := svc.RunSQL(ctx, sqlReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleListResources handles list_resources tool calls.
// 数据层「资源直查」入口（脱离本体）：列出账户有权查看的数据资源，配合 describe_resource + run_sql。
func handleListResources(svc knresources.KnResourcesService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		listReq := &knresources.ListResourcesReq{}
		if err := bindArguments(req, listReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := svc.ListResources(ctx, listReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleDescribeResource handles describe_resource tool calls.
// 取单个资源的物理 schema（列名 + 连接器类型），供写 run_sql 用。
func handleDescribeResource(svc knresources.KnResourcesService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resourceID := getStringArg(req, "resource_id", "")
		if resourceID == "" {
			return mcp.NewToolResultError("resource_id is required"), nil
		}

		resp, err := svc.DescribeResource(ctx, resourceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetKnDetail handles get_kn_detail tool calls.
// 包装 bkn-backend 的知识网络详情（概念组 / 对象类 / 关系类 / 行动类），并按
// detail_level 做渐进式裁剪：summary（默认）返回骨架 + 属性名，full 返回全量。
func handleGetKnDetail(bkn interfaces.BknBackendAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		knID := getStringArg(req, "kn_id", "")
		if knID == "" {
			knID = getKnIDFromHeader(req)
		}
		if knID == "" {
			return mcp.NewToolResultError("kn_id is required"), nil
		}

		resp, err := bkn.GetKnowledgeNetworkDetail(ctx, knID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resp.Slim(getStringArg(req, "detail_level", interfaces.DetailLevelSummary))
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// knDrillArgs are the arguments for the get_object_types / get_relation_types
// drill-down tools: a knowledge-network id plus the type ids to expand.
type knDrillArgs struct {
	KnID string   `json:"kn_id"`
	IDs  []string `json:"ids"`
}

func (a *knDrillArgs) resolveKnID(req mcp.CallToolRequest) string {
	if a.KnID != "" {
		return a.KnID
	}
	return getKnIDFromHeader(req)
}

// handleGetObjectTypes handles get_object_types tool calls: return the full
// definition (data/logic properties incl. mappings) of the requested object type
// ids. Pairs with get_kn_detail summary, which omits that heavy detail.
func handleGetObjectTypes(bkn interfaces.BknBackendAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args := &knDrillArgs{}
		if err := bindArguments(req, args); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		knID := args.resolveKnID(req)
		if knID == "" {
			return mcp.NewToolResultError("kn_id is required"), nil
		}
		if len(args.IDs) == 0 {
			return mcp.NewToolResultError("ids is required (object type ids from get_kn_detail)"), nil
		}

		detail, err := bkn.GetKnowledgeNetworkDetail(ctx, knID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		matched, missing := detail.FilterObjectTypes(args.IDs)
		resp := &interfaces.ObjectTypesResp{KnID: knID, ObjectTypes: matched, Missing: missing}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetRelationTypes handles get_relation_types tool calls: return the full
// definition (incl. mapping_rules) of the requested relation type ids.
func handleGetRelationTypes(bkn interfaces.BknBackendAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args := &knDrillArgs{}
		if err := bindArguments(req, args); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		knID := args.resolveKnID(req)
		if knID == "" {
			return mcp.NewToolResultError("kn_id is required"), nil
		}
		if len(args.IDs) == 0 {
			return mcp.NewToolResultError("ids is required (relation type ids from get_kn_detail)"), nil
		}

		detail, err := bkn.GetKnowledgeNetworkDetail(ctx, knID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		matched, missing := detail.FilterRelationTypes(args.IDs)
		resp := &interfaces.RelationTypesResp{KnID: knID, RelationTypes: matched, Missing: missing}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

func getKnIDFromHeader(req mcp.CallToolRequest) string {
	if req.Header == nil {
		return ""
	}
	return req.Header.Get("X-Kn-ID")
}

func getStringArg(req mcp.CallToolRequest, key, fallback string) string {
	if val := req.GetString(key, ""); val != "" {
		return val
	}
	return fallback
}

func bindArguments(req mcp.CallToolRequest, target any) error {
	raw := req.GetRawArguments()
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// handleFindSkills returns a tool handler for find_skills.
func handleFindSkills(service interfaces.IFindSkillsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		knID := getStringArg(req, "kn_id", "")
		if knID == "" {
			knID = getKnIDFromHeader(req)
		}
		if knID == "" {
			return mcp.NewToolResultError(
				"kn_id is required (pass kn_id in body or configure X-Kn-ID header)",
			), nil
		}

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		findReq := &interfaces.FindSkillsReq{}
		if err := bindArguments(req, findReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		findReq.AccountID = authCtx.AccountID
		findReq.AccountType = string(authCtx.AccountType)
		findReq.KnID = knID

		if err := defaults.Set(findReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := validator.New().Struct(findReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := service.FindSkills(ctx, findReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}
