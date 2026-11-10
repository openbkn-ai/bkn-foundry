// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/go-playground/validator/v10"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
	logicsKqs "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knquerysubgraph"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knresources"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knrunsql"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knsearch"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/objectpermission"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permissionguide"
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
func handleSearchInstance(knSearchService knsearch.KnSearchService,
	guide *permissionguide.Guide) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		authCtx, _ := common.GetAccountAuthContextFromCtx(ctx)

		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
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
		resp.PermissionGuidance = permissionGuidancesForObjectImpacts(ctx, guide,
			instanceReq.ResolvedKnID(), resp.PermissionImpacts)
		resp.PermissionImpacts = nil

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleQueryObjectInstance handles query_object_instance tool calls.
//
// A refusal or a masked / row-filtered result carries permission_guidance so
// the agent can say what was withheld and where to request access.
func handleQueryObjectInstance(ontologyQuery interfaces.DrivenOntologyQuery,
	guide *permissionguide.Guide) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}

		queryReq := &interfaces.QueryObjectInstancesReq{}
		if err := bindPreciseArguments(req, queryReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
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
			return inputValidationToolError(ctx, "kn_id and ot_id are required"), nil
		}
		if err := validator.New().Struct(queryReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}

		resp, err := ontologyQuery.QueryObjectInstances(ctx, queryReq)
		if err != nil {
			return toolErrorWithPermissionGuidance(err,
				guide.ForObjectTypeError(ctx, err, queryReq.KnID, queryReq.OtID)), nil
		}
		bkntrace.EmitQueryObjectInstanceEvents(ctx, nil, queryReq, resp)
		resp.ObjectConcept = nil
		resp.PermissionGuidance = guide.ForObjectQuery(ctx, queryReq.KnID, queryReq.OtID, queryReq.Properties,
			resp.EffectivePermissions, resp.RowFilterApplied)
		resp.RowFilterApplied = false // Restated by the guidance.
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
			return inputValidationToolError(ctx, err.Error()), nil
		}

		subgraphReq := &interfaces.QueryInstanceSubgraphReq{}
		if err := bindPreciseArguments(req, subgraphReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}

		subgraphReq.KnID = getStringArg(req, "kn_id", subgraphReq.KnID)
		if subgraphReq.KnID == "" {
			subgraphReq.KnID = getKnIDFromHeader(req)
		}
		subgraphReq.IncludeLogicParams = req.GetBool("include_logic_params", subgraphReq.IncludeLogicParams)
		if subgraphReq.RelationTypePaths == nil {
			return inputValidationToolError(ctx, "relation_type_paths is required"), nil
		}
		if subgraphReq.KnID == "" {
			return inputValidationToolError(ctx, "kn_id is required"), nil
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
			return inputValidationToolError(ctx, err.Error()), nil
		}

		exploreReq := &interfaces.ExploreSubgraphReq{}
		if err := bindPreciseArguments(req, exploreReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
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
				return inputValidationToolError(ctx, missing.name+" is required"), nil
			}
		}
		// The value range of path_length is controlled downstream (>3 back to 400), but 0 has to be blocked here: it is int.
		// With a zero value, it is unclear whether "no transmission" or "0 was passed", and the downstream does not report an error for 0, but only returns an empty subgraph.
		// Let the caller think "nothing is connected".
		if exploreReq.PathLength <= 0 {
			return inputValidationToolError(ctx, "path_length is required and must be at least 1"), nil
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
func handleGetLogicPropertiesValues(service interfaces.IKnLogicPropertyResolverService,
	guide *permissionguide.Guide) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			return toolErrorWithPermissionGuidance(err,
				guide.ForObjectTypeDenial(ctx, err, resolveReq.KnID, resolveReq.OtID)), nil
		}
		resp.PermissionGuidance = guide.ForObjectQuery(ctx, resolveReq.KnID, resolveReq.OtID,
			resolveReq.Properties, resp.EffectivePermissions, false)
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
			return inputValidationToolError(ctx, err.Error()), nil
		}

		sqlReq := &knrunsql.RunSQLReq{}
		if err := bindArguments(req, sqlReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
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

// handleRunCypher handles run_cypher tool calls.
// Read-only Cypher over the knowledge network's model: bkn-backend compiles the
// query against the object and relation types and runs the SQL it produces.
// A refusal names the construct it refused, so it is returned to the caller
// verbatim rather than folded into a generic failure.
func handleRunCypher(svc kncypher.KnCypherService,
	guide *permissionguide.Guide) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}

		// Precise binding: parameters carry the caller's own values, and a
		// large integer that arrives as a float no longer matches the row it
		// was meant to select.
		cypherReq := &kncypher.RunCypherReq{}
		if err := bindPreciseArguments(req, cypherReq); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		// The tool's schema has no timeout; one an agent sends anyway is
		// dropped so that the statement runs under the configured timeout.
		cypherReq.TimeoutSec = 0

		resp, err := svc.RunCypher(ctx, cypherReq)
		if err != nil {
			return toolErrorWithPermissionGuidances(err, permissionGuidancesForObjectImpacts(ctx, guide,
				cypherReq.KnID, cypherPermissionImpactsFromError(err))), nil
		}
		resp.PermissionGuidance = permissionGuidancesForObjectImpacts(ctx, guide, cypherReq.KnID, resp.PermissionImpacts)
		resp.PermissionImpacts = nil

		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

func permissionGuidancesForObjectImpacts(ctx context.Context, guide *permissionguide.Guide, knID string,
	impacts []interfaces.ObjectPermissionImpact) []*interfaces.PermissionGuidance {
	merged := make(map[string]interfaces.ObjectPermissionImpact, len(impacts))
	order := make([]string, 0, len(impacts))
	for _, impact := range impacts {
		if impact.ObjectTypeID == "" {
			continue
		}
		current, exists := merged[impact.ObjectTypeID]
		if !exists {
			current.ObjectTypeID = impact.ObjectTypeID
			order = append(order, impact.ObjectTypeID)
		}
		current.Properties = append(current.Properties, impact.Properties...)
		current.RowFilterApplied = current.RowFilterApplied || impact.RowFilterApplied
		merged[impact.ObjectTypeID] = current
	}
	guidance := make([]*interfaces.PermissionGuidance, 0, len(merged))
	for _, objectTypeID := range order {
		impact := merged[objectTypeID]
		entry := guide.ForObjectTypeImpact(ctx, knID, objectTypeID,
			impact.Properties, impact.RowFilterApplied)
		if entry != nil {
			guidance = append(guidance, entry)
		}
	}
	return guidance
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
func handleGetKnDetail(bkn interfaces.BknBackendAccess, metrics knmetrics.KnMetricsService, schemaAccess interfaces.ObjectSchemaAccess, knAuthz interfaces.KnowledgeNetworkAuthorizer) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		knID := getStringArg(req, "kn_id", "")
		if knID == "" {
			knID = getKnIDFromHeader(req)
		}
		if knID == "" {
			return inputValidationToolError(ctx, "kn_id is required"), nil
		}

		branch := getStringArg(req, "branch", "main")
		detailLevel := getStringArg(req, "detail_level", interfaces.DetailLevelSummary)
		resp, err := knmetrics.NetworkDetailForBranch(ctx, bkn, knID, branch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		notice := func(reduction interfaces.KnDetailReduction) string {
			return renderKnDetailNotice(ctx, resp, reduction, detailLevel, branch)
		}
		// A network too large to answer with its concept model narrows to its navigation
		// shell, and does so here -- before the two steps below, which each cost a
		// downstream call per object type of a list this answer will not carry (#1877).
		// brief asks for that shell outright.
		if detailLevel == interfaces.DetailLevelBrief || resp.NeedsNavigationShell() {
			resp.ReduceToNavigationShell()
			resp.Notice = notice(interfaces.KnDetailReductionShell)
		} else {
			resp.ObjectTypes, err = objectpermission.FilterObjectTypesBranch(ctx, schemaAccess, knID, branch, resp.ObjectTypes)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Counts only: which object types have metrics worth drilling into, without
			// carrying the metric list itself at this level.
			if err := metrics.AttachRelatedMetricCounts(ctx, knID, resp.ObjectTypes, branch); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		// The mounted Skills and tools, counted the same way — but gated first.
		//
		// The metric counts beside this one inherit their scope: they are counted under object
		// types that already survived FilterObjectTypes. The bindings have no such upstream
		// filter, and they are read over the internal face with this service's identity, which is
		// why every other reader of them authorizes the caller first. Counting them here without
		// that check would answer a question about a network on the strength of the id someone
		// typed.
		//
		// A refusal omits the field rather than failing the call: the concept model is already
		// filtered per caller and is still a correct answer to "what is this network". Absent
		// means unknown either way, which is what it has to mean for an unreadable list too.
		if err := knAuthz.AuthorizeRead(ctx, knID); err != nil {
			log.Printf("WARN: get_kn_detail capability counts withheld for kn %s: %v", knID, err)
		} else if refs, err := bkn.ListKNCapabilities(ctx, knID, branch, ""); err == nil {
			resp.AttachMountedCapabilities(refs)
		} else {
			// Logged because an absent field and a silently dropped error look identical to
			// whoever reads the answer, and only one of them is worth investigating.
			log.Printf("WARN: get_kn_detail capability bindings unreadable for kn %s: %v", knID, err)
		}
		resp.Slim(detailLevel)
		// Narrowed to what the caller can read in one result, measured as they will read
		// it, and before the snapshot so the evidence is the answer they were given.
		if _, err := resp.FitWithin(interfaces.MaxKnDetailChars, notice, func(d *interfaces.KnowledgeNetworkDetail) (int, error) {
			if detailLevel != interfaces.DetailLevelFull {
				d = objectpermission.WithUnrestrictedPermissionsOmitted(d)
			}
			return toolResultRunes(d, format)
		}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		bkntrace.EmitSchemaSnapshotEvents(ctx, nil, "network", knID, nil, resp, true)
		// After the snapshot, which records permissions as they are: the evidence
		// keeps every entry, only the answer drops the ones that say nothing.
		if detailLevel != interfaces.DetailLevelFull {
			objectpermission.OmitUnrestrictedPermissions(resp.ObjectTypes)
		}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// renderKnDetailNotice is the localized notice for a get_kn_detail answer that carries
// less than the level asked for.
func renderKnDetailNotice(ctx context.Context, detail *interfaces.KnowledgeNetworkDetail, reduction interfaces.KnDetailReduction, detailLevel, branch string) string {
	key, args := detail.KnDetailNotice(reduction, detailLevel, branch)
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail."+key, args)
	if err != nil {
		logger.Errorf("localized message rendering failed: %v", err)
	}
	return text
}

// knDrillArgs are the arguments for the get_object_types / get_relation_types
// drill-down tools: a knowledge-network id plus the type ids to expand.
type knDrillArgs struct {
	KnID string   `json:"kn_id"`
	IDs  []string `json:"ids"`
}

// knObjectTypesArgs is knDrillArgs plus the window a caller walks with when it
// has no ids. The two paging fields are meaningless with ids and are ignored
// there rather than refused: naming ids is already the precise request.
type knObjectTypesArgs struct {
	knDrillArgs
	Branch string `json:"branch"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
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
func handleGetObjectTypes(bkn interfaces.BknBackendAccess, metrics knmetrics.KnMetricsService, schemaAccess interfaces.ObjectSchemaAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		args := &knObjectTypesArgs{}
		if err := bindArguments(req, args); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		knID := args.resolveKnID(req)
		if knID == "" {
			return inputValidationToolError(ctx, "kn_id is required"), nil
		}

		// No ids means the caller has none to give. That happens on a network past
		// get_kn_detail's caps, where the concept arrays are withheld and nothing
		// else on this surface enumerates them (#1889), so this reads a page instead
		// of refusing. Named ids stay the primary path and ignore the paging inputs.
		var (
			matched    []*interfaces.ObjectType
			totalCount int64
			nextOffset *int
			offset     int
			notice     string
		)
		if len(args.IDs) == 0 {
			var (
				limit   int
				clamped bool
			)
			offset, limit, clamped = interfaces.ResolveObjectTypePage(args.Offset, args.Limit)
			page, err := knmetrics.ObjectTypesForBranch(ctx, bkn, knID, args.Branch, offset, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			matched, totalCount = page.Entries, page.TotalCount
			nextOffset = interfaces.NextObjectTypeOffset(offset, page.Scanned, totalCount)
			if clamped {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.ObjectTypePageLimitClamped", map[string]any{"Arg1": args.Limit, "Arg2": interfaces.MaxObjectTypePageSize, "Arg3": limit})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				notice = localizedText
			}
		} else {
			// Prioritize the endpoint that retrieves details by id: the export view only lists object types, does not enrich the data source, and does not enrich the data source.
			// condition_operations is always empty, and the caller uses this to determine whether the field can match / knn.
			//
			// Omitted IDs are not reported as missing because an omission may be an
			// authorization filter.
			matched, err = knmetrics.ObjectTypeDetailForBranch(ctx, bkn, knID, args.Branch, args.IDs, true)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		// The index carries no properties, so there is no property plan to apply to
		// it and no operator to trim; and it is an index, so it does not carry the
		// metrics a caller drills for. Running either over it would be a downstream
		// call per object type for a field the answer does not have -- which is what
		// made a page of this cost a second an object type.
		if len(args.IDs) > 0 {
			matched, err = objectpermission.FilterObjectTypesBranch(ctx, schemaAccess, knID, args.Branch, matched)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// The same rule as search_schema: only emit underivable operators. Comparison operators (==/in/like/range…)
			// Determined by the attribute type, repeating each attribute for more than ten times is pure noise - the object type is small and it still occupies the context.
			objectpermission.TrimObjectTypesToIndexBackedOps(matched)

			// Step 2 of the OT-first metric path: a metric that is not bound to a logic
			// property is unreachable from the object type without this.
			if err := metrics.AttachRelatedMetrics(ctx, knID, matched, args.Branch); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		// A listed page is complete for what it asked: nothing was requested by name,
		// so nothing can be missing from it.
		// Property declarations are search_schema's to surface (#2260).
		interfaces.OmitPropertyDeclarations(matched)
		complete := len(args.IDs) == 0 || len(matched) == len(args.IDs)
		bkntrace.EmitSchemaSnapshotEvents(ctx, nil, "object", knID, schemaObjectTypeIDs(matched), matched, complete)
		resp := &interfaces.ObjectTypesResp{KnID: knID, ObjectTypes: matched,
			TotalCount: totalCount, NextOffset: nextOffset, Notice: notice}
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// knRelationTypesArgs is knDrillArgs plus what a caller without ids lists by: the
// object types the relation types should touch, and the window. Like
// knObjectTypesArgs, the listing inputs are ignored once ids are named.
type knRelationTypesArgs struct {
	knDrillArgs
	Branch        string   `json:"branch"`
	ObjectTypeIDs []string `json:"object_type_ids"`
	Offset        int      `json:"offset"`
	Limit         int      `json:"limit"`
}

// handleGetRelationTypes handles get_relation_types tool calls: return the full
// definition (incl. mapping_rules) of the requested relation type ids, or, without
// ids, one page of the relation type index.
func handleGetRelationTypes(bkn interfaces.BknBackendAccess) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		args := &knRelationTypesArgs{}
		if err := bindArguments(req, args); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		knID := args.resolveKnID(req)
		if knID == "" {
			return inputValidationToolError(ctx, "kn_id is required"), nil
		}

		// No ids means the caller has none to give: get_kn_detail answered with its
		// navigation shell, where relation_types is null, and on a branch other than
		// main search_schema cannot recall them either. So this lists instead of
		// refusing, as get_object_types does (#1889).
		var (
			matched    []*interfaces.RelationType
			totalCount int64
			nextOffset *int
			notice     string
			offset     int
		)
		listing := len(args.IDs) == 0
		if listing {
			var limit int
			var clamped bool
			offset, limit, clamped = interfaces.ResolveObjectTypePage(args.Offset, args.Limit)
			page, err := knmetrics.RelationTypesForBranch(ctx, bkn, knID, args.Branch, args.ObjectTypeIDs, offset, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			matched, totalCount = page.Entries, page.TotalCount
			nextOffset = interfaces.NextObjectTypeOffset(offset, page.Scanned, totalCount)
			interfaces.ReduceToRelationTypeIndex(matched)
			if clamped {
				notice = renderDetailNotice(ctx, "agentRetrieval.Detail.RelationTypePageLimitClamped",
					map[string]any{"Arg1": args.Limit, "Arg2": interfaces.MaxObjectTypePageSize, "Arg3": limit})
			}
		} else {
			matched, err = knmetrics.RelationTypeDetailForBranch(ctx, bkn, knID, args.Branch, args.IDs, true)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		// Relation declarations are search_schema's to surface, but for lifecycle and the fallback marking (#2285).
		interfaces.OmitRelationDeclarations(matched)
		resp := &interfaces.RelationTypesResp{KnID: knID, RelationTypes: matched,
			TotalCount: totalCount, NextOffset: nextOffset, Notice: notice}
		if listing {
			// Measured on the text the caller reads, before the snapshot, so the
			// evidence is the page the caller got.
			if _, err := interfaces.FitRelationTypePage(resp, offset, interfaces.MaxRelationTypePageChars,
				func(kept int) string {
					return renderDetailNotice(ctx, "agentRetrieval.Detail.RelationTypePageCharsCut",
						map[string]any{"Arg1": interfaces.MaxRelationTypePageChars, "Arg2": kept})
				},
				func(r *interfaces.RelationTypesResp) (int, error) { return toolTextRunes(r, format) },
			); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		// A listed page is complete for what it asked: nothing was requested by name.
		complete := listing || len(resp.RelationTypes) == len(args.IDs)
		bkntrace.EmitSchemaSnapshotEvents(ctx, nil, "relation", knID, schemaRelationTypeIDs(resp.RelationTypes), resp.RelationTypes, complete)
		result, err := BuildMCPToolResult(resp, format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// renderDetailNotice renders a localized notice, logging rather than failing when
// the message cannot be rendered.
func renderDetailNotice(ctx context.Context, key string, args map[string]any) string {
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), key, args)
	if err != nil {
		logger.Errorf("localized message rendering failed: %v", err)
	}
	return text
}

// toolTextRunes is how many characters the text of resp's tool result carries.
func toolTextRunes(resp any, format rest.ResponseFormat) (int, error) {
	result, err := BuildMCPToolResult(resp, format)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			n += utf8.RuneCountInString(text.Text)
		}
	}
	return n, nil
}

func schemaObjectTypeIDs(items []*interfaces.ObjectType) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func schemaRelationTypeIDs(items []*interfaces.RelationType) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
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
			return inputValidationToolError(ctx, err.Error()), nil
		}

		args := &interfaces.QueryMetricReq{}
		if err := bindPreciseArguments(req, args); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		if args.KnID == "" {
			args.KnID = getKnIDFromHeader(req)
		}

		resp, err := service.QueryMetric(ctx, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		bkntrace.EmitQueryMetricEvents(ctx, nil, args, resp)
		result, err := BuildMCPToolResult(metricResultWithExactValues(resp), format)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

// handleDrillThroughMetric pages participants of a server-issued metric bucket.
func handleDrillThroughMetric(service knmetrics.KnMetricsService) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		format, err := GetResponseFormatFromRequest(req)
		if err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		args := &interfaces.MetricDrillThroughReq{}
		if err := bindPreciseArguments(req, args); err != nil {
			return inputValidationToolError(ctx, err.Error()), nil
		}
		if args.KnID == "" {
			args.KnID = getKnIDFromHeader(req)
		}
		resp, err := service.DrillThroughMetric(ctx, args)
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
//
//nolint:unused // Retained for object-type validation extensions.
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

// inputValidationToolError preserves the public MCP result while retaining a
// producer-owned classification for the managed attempt's terminal fact.
func inputValidationToolError(ctx context.Context, message string) *mcp.CallToolResult {
	bkntrace.RecordToolFailure(ctx, "invalid_arguments", "input_validation")
	return mcp.NewToolResultError(message)
}
