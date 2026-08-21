// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"github.com/bytedance/sonic"
	"github.com/creasty/defaults"
	validator "github.com/go-playground/validator/v10"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	logicsKqs "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knsearch"
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
		// The MCP surface only issues underivable operators, and comparison operators are determined by the attribute type.
		schemaReq.IndexOpsOnly = true

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

	// MCP (LLM) scenario defaults to simplified Schema: use brief when schema_brief is not explicitly passed.
	// Smaller and preserved data_source.id / attribute name/type/condition_operations;
	// Explicitly pass schema_brief=false when the complete schema of attribute comments/primary keys/labels is required.
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

// handleSearchInstance returns a tool handler for search_instance.
func handleSearchInstance(knSearchService knsearch.KnSearchService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, _ := common.GetAccountAuthContextFromCtx(ctx)

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		instanceReq := &interfaces.SearchInstanceReq{}
		_ = bindArguments(req, instanceReq)
		// The MCP surface only emits non-derivable operators, and the comparison operator is determined by the attribute type - the same posture as search_schema.
		instanceReq.IndexOpsOnly = true
		instanceReq.XKnID = getKnIDFromHeader(req)
		if authCtx != nil {
			instanceReq.XAccountID = authCtx.AccountID
			instanceReq.XAccountType = string(authCtx.AccountType)
		}

		resp, err := knSearchService.SearchInstance(ctx, instanceReq)
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

// handleQueryObjectInstance handles query_object_instance tool calls.
func handleQueryObjectInstance(ontologyQuery interfaces.DrivenOntologyQuery) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		queryReq := &interfaces.QueryObjectInstancesReq{}
		if err := bindPreciseArguments(req, queryReq); err != nil {
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
		bkntrace.EmitQueryObjectInstanceEvents(ctx, nil, queryReq, resp)
		resp.ObjectConcept = nil
		// Pure structured filtering has no relevance score; strip the constant _score to avoid misleading callers.
		// Keep real relevance scores from knn/match (#236).
		if !queryReq.HasScoringOperator() {
			resp.StripInstanceScores()
		}
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
		if err := bindPreciseArguments(req, subgraphReq); err != nil {
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

// handleExploreSubgraph handles explore_subgraph tool calls.
func handleExploreSubgraph(service logicsKqs.KnQuerySubgraphService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		exploreReq := &interfaces.ExploreSubgraphReq{}
		if err := bindPreciseArguments(req, exploreReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		exploreReq.KnID = getStringArg(req, "kn_id", exploreReq.KnID)
		if exploreReq.KnID == "" {
			exploreReq.KnID = getKnIDFromHeader(req)
		}
		exploreReq.IncludeLogicParams = req.GetBool("include_logic_params", exploreReq.IncludeLogicParams)
		if exploreReq.Limit == 0 {
			exploreReq.Limit = 10
		}
		// The three required fields are reported separately, and the combined sentence "required" will allow the model to guess which one is missing.
		for _, missing := range []struct {
			empty bool
			name  string
		}{
			{exploreReq.KnID == "", "kn_id"},
			{exploreReq.SourceObjectTypeID == "", "source_object_type_id"},
			{exploreReq.Direction == "", "direction"},
		} {
			if missing.empty {
				return mcp.NewToolResultError(missing.name + " is required"), nil
			}
		}
		// The value range of path_length is controlled downstream (>3 back to 400), but 0 has to be blocked here: it is int.
		// With a zero value, it is unclear whether "no transmission" or "0 was passed", and the downstream does not report an error for 0, but only returns an empty subgraph.
		// Let the caller think "nothing is connected".
		if exploreReq.PathLength <= 0 {
			return mcp.NewToolResultError("path_length is required and must be at least 1"), nil
		}

		resp, err := service.ExploreSubgraph(ctx, exploreReq)
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
		if err := bindPreciseArguments(req, resolveReq); err != nil {
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
		if err := bindPreciseArguments(req, actionReq); err != nil {
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
		// get_action_info always returns JSON: action tool definition needs to be machine consumable, response_format is ignored (TOON will destroy the structure).
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleExecuteAction handles execute_action tool calls.
// Paired with get_action_info: Agent first uses get_action_info to get dynamic_params schema,
// Then use this tool to fill in the real dynamic parameter values to trigger execution (asynchronously, return execution_id).
func handleExecuteAction(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		execReq := &interfaces.KnActionExecuteRequest{}
		if err := bindPreciseArguments(req, execReq); err != nil {
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
		// execute_action always returns JSON: execution_id, etc. need to be consumed by the machine.
		result, err := BuildMCPToolResult(resp, rest.FormatJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleGetActionExecution handles get_action_execution tool calls.
// Paired with execute_action: Use the execution_id returned by execute_action to query the status and results of the execution.
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
// List action execution history (can be filtered and paginated by action type/status/trigger method).
func handleListActionExecutions(service interfaces.IKnActionRecallService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			return mcp.NewToolResultError("authentication required"), nil
		}

		listReq := &interfaces.KnListActionExecutionsRequest{}
		if err := bindPreciseArguments(req, listReq); err != nil {
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
// Used to let external agents discover available kn_id (prefix of other query tools).
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
// Execute read-only SQL (mandatory SELECT-only) on the data resources mounted on the knowledge network, and use the shared knrunsql service at the bottom.
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
// Data layer "Resource Direct Inspection" entrance (separated from the ontology): List the data resources that the account has the right to view, with describe_resource + run_sql.
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
// Get the physical schema (column name + connector type) of a single resource for writing run_sql.
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
// Pack the knowledge network details (concept group/object type/relation type/action class) of bkn-backend and press.
// detail_level does progressive cropping: summary (default) returns the skeleton + attribute name, full returns the full amount.
func handleGetKnDetail(bkn interfaces.BknBackendAccess, metrics knmetrics.KnMetricsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		// Counts only: which object types have metrics worth drilling into, without
		// carrying the metric list itself at this level.
		metrics.AttachRelatedMetricCounts(ctx, knID, resp.ObjectTypes)
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
// ids, plus the metrics scoped to them. Pairs with get_kn_detail summary, which
// omits that heavy detail.
func handleGetObjectTypes(bkn interfaces.BknBackendAccess, metrics knmetrics.KnMetricsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		// Prioritize the endpoint that retrieves details by id: the export view only lists object types, does not enrich the data source, and does not enrich the data source.
		// condition_operations is always empty, and the caller uses this to determine whether the field can match / knn.
		//
		// But this endpoint requires all ids to be hit. If an invalid id is mixed in, the whole batch will be 404. Return to the export view at this time:
		// It would be better to have fewer operators in this batch than to throw away the remaining valid object types because of an invalid id——.
		// Exported views also support fallback matching by name, which is also existing behavior.
		matched, err := bkn.GetObjectTypeDetail(ctx, knID, args.IDs, true)
		var missing []string
		if err != nil || len(matched) < len(args.IDs) {
			detail, detailErr := bkn.GetKnowledgeNetworkDetail(ctx, knID)
			if detailErr != nil {
				return mcp.NewToolResultError(detailErr.Error()), nil
			}
			matched, missing = detail.FilterObjectTypes(args.IDs)
		} else {
			missing = missingObjectTypeIDs(args.IDs, matched)
		}
		// The same rule as search_schema: only emit underivable operators. Comparison operators (==/in/like/range…)
		// Determined by the attribute type, repeating each attribute for more than ten times is pure noise - the object type is small and it still occupies the context.
		trimObjectTypesToIndexBackedOps(matched)

		// Step 2 of the OT-first metric path: a metric that is not bound to a logic
		// property is unreachable from the object type without this.
		metrics.AttachRelatedMetrics(ctx, knID, matched)
		bkntrace.EmitSchemaDefinitionEvents(ctx, nil, "object", knID, args.IDs, len(matched))
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
		bkntrace.EmitSchemaDefinitionEvents(ctx, nil, "relation", knID, args.IDs, len(matched))
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
	data, err := sonic.Marshal(raw)
	if err != nil {
		return err
	}
	return sonic.Unmarshal(data, target)
}

// bindPreciseArguments is reserved for tool inputs that carry dynamic business
// values, such as instance identities, conditions, parameters, or cursors.
func bindPreciseArguments(req mcp.CallToolRequest, target any) error {
	raw := req.GetRawArguments()
	if raw == nil {
		return nil
	}
	data, err := sonic.Marshal(raw)
	if err != nil {
		return err
	}
	return common.UnmarshalPreciseJSON(data, target)
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

// handleQueryMetric handles query_metric tool calls: compute one modelled metric
// by its own definition.
//
// This is step 3 of the OT-first metric path and the reason run_sql is not the
// answer for a metric: the calculation formula lives in the MetricDefinition, so
// the caller names the metric instead of re-deriving it in SQL.
func handleQueryMetric(service knmetrics.KnMetricsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		args := &interfaces.QueryMetricReq{}
		if err := bindPreciseArguments(req, args); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if args.KnID == "" {
			args.KnID = getKnIDFromHeader(req)
		}

		resp, err := service.QueryMetric(ctx, args)
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

// missingObjectTypeIDs returns the object type IDs that were requested but not obtained, maintaining the same semantics as when filtering exported views.
func missingObjectTypeIDs(requested []string, matched []*interfaces.ObjectType) []string {
	found := make(map[string]struct{}, len(matched))
	for _, ot := range matched {
		if ot != nil {
			found[ot.ID] = struct{}{}
		}
	}

	var missing []string
	for _, id := range requested {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// trimObjectTypesToIndexBackedOps converges the operator to those brought by the index.
//
// The rules are consistent with search_schema: condition_operations only registers capabilities that cannot be inferred from the attribute type.
// (match / multi_match / knn, depending on whether the underlying index is built or not). The comparison operator is judged by type, and the server side.
// There is no amount of information being distributed one by one. This only affects the MCP side; Studio directly connects to BKN and still gets the full amount.
func trimObjectTypesToIndexBackedOps(objectTypes []*interfaces.ObjectType) {
	for _, ot := range objectTypes {
		if ot == nil {
			continue
		}
		for _, p := range ot.DataProperties {
			if p == nil {
				continue
			}
			p.ConditionOperations = indexBackedConditionOperations(p.ConditionOperations)
		}
	}
}

// indexBackedConditionOperations only retains the operators brought by the index.
func indexBackedConditionOperations(ops []interfaces.KnOperationType) []interfaces.KnOperationType {
	var out []interfaces.KnOperationType
	for _, op := range ops {
		switch op {
		case interfaces.KnOperationTypeMatch, interfaces.KnOperationTypeMultiMatch, interfaces.KnOperationTypeKnn:
			out = append(out, op)
		}
	}
	return out
}
