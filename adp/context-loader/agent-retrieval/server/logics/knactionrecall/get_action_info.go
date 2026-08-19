// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knactionrecall

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// GetActionInfo retrieves action information for action recall.
func (s *knActionRecallServiceImpl) GetActionInfo(ctx context.Context, req *interfaces.KnActionRecallRequest) (*interfaces.KnActionRecallResponse, error) {
	// 1. Prefer _instance_identities and fall back to _instance_identity as an array.
	instanceIdentities := make([]map[string]any, 0)
	if len(req.InstanceIdentities) > 0 {
		for _, id := range req.InstanceIdentities {
			if len(id) > 0 {
				instanceIdentities = append(instanceIdentities, id)
			}
		}
	} else if len(req.InstanceIdentity) > 0 {
		instanceIdentities = append(instanceIdentities, req.InstanceIdentity)
	}

	// 2. Query actions.
	actionsReq := &interfaces.QueryActionsRequest{
		KnID:               req.KnID,
		AtID:               req.AtID,
		InstanceIdentities: instanceIdentities,
		IncludeTypeInfo:    false, // No type information required.
	}

	actionsResp, err := s.ontologyQuery.QueryActions(ctx, actionsReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionInfo] QueryActions failed, err: %v", err)
		return nil, err
	}

	// 3. Validate the response.
	if actionsResp.ActionSource == nil {
		s.logger.WithContext(ctx).Warnf("[KnActionRecall#GetActionInfo] ActionSource is nil")
		return &interfaces.KnActionRecallResponse{
			DynamicTools: []interfaces.KnDynamicTool{},
		}, nil
	}

	if len(actionsResp.Actions) == 0 {
		s.logger.WithContext(ctx).Warnf("[KnActionRecall#GetActionInfo] Actions is empty")
		return &interfaces.KnActionRecallResponse{
			DynamicTools: []interfaces.KnDynamicTool{},
		}, nil
	}

	// 4. Validate action_source.type.
	if actionsResp.ActionSource.Type != interfaces.ActionSourceTypeTool && actionsResp.ActionSource.Type != interfaces.ActionSourceTypeMCP {
		s.logger.WithContext(ctx).Warnf("[KnActionRecall#GetActionInfo] Unsupported action_source type: %s", actionsResp.ActionSource.Type)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest,
			infraErr.LocalizedDetail(ctx, "ActionSourceTypeUnsupported"))
	}

	// 5. Process actions[0] only.
	firstAction := actionsResp.Actions[0]

	// 6. Build the action driver API URL.
	apiURL := s.buildActionDriverAPIURL(req.KnID, req.AtID)

	// 7. Build action driver fixed_params.
	fixedParams := interfaces.ActionDriverFixedParams{
		DynamicParams:      firstAction.Parameters,
		InstanceIdentities: instanceIdentities,
	}

	var dynamicTool interfaces.KnDynamicTool

	if actionsResp.ActionSource.Type == interfaces.ActionSourceTypeTool {
		// 8a. Tool source: retrieve tool details.
		toolDetailReq := &interfaces.GetToolDetailRequest{
			BoxID:  actionsResp.ActionSource.BoxID,
			ToolID: actionsResp.ActionSource.ToolID,
		}

		toolDetail, err := s.operatorIntegration.GetToolDetail(ctx, toolDetailReq)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionInfo] GetToolDetail failed, err: %v", err)
			return nil, err
		}

		// 9a. Convert the tool schema to action driver parameters.
		parameters, err := s.convertToolSchemaToActionDriver(ctx, toolDetail.Metadata.APISpec)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionInfo] ConvertToolSchemaToActionDriver failed, err: %v", err)
			return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
				infraErr.LocalizedDetail(ctx, "ToolSchemaConversionFailed"))
		}

		// 10a. Build KnDynamicTool.
		dynamicTool = interfaces.KnDynamicTool{
			Name:            toolDetail.Name,
			Description:     toolDetail.Description,
			Parameters:      parameters,
			APIURL:          apiURL,
			FixedParams:     fixedParams,
			APICallStrategy: interfaces.ResultProcessStrategyKnActionRecall,
		}
	} else {
		// 8b. MCP source: retrieve MCP tool details.
		mcpReq := &interfaces.GetMCPToolDetailRequest{
			McpID:    actionsResp.ActionSource.McpID,
			ToolName: actionsResp.ActionSource.ToolName,
		}

		toolDetail, err := s.operatorIntegration.GetMCPToolDetail(ctx, mcpReq)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionInfo] GetMCPToolDetail failed, err: %v", err)
			return nil, err
		}

		// 9b. Convert the MCP schema to action driver parameters.
		parameters, err := s.convertMCPSchemaToActionDriver(ctx, toolDetail.InputSchema)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("[KnActionRecall#GetActionInfo] ConvertMCPSchemaToActionDriver failed, err: %v", err)
			return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
				infraErr.LocalizedDetail(ctx, "MCPSchemaConversionFailed"))
		}

		// 10b. Build KnDynamicTool.
		dynamicTool = interfaces.KnDynamicTool{
			Name:            toolDetail.Name,
			Description:     toolDetail.Description,
			Parameters:      parameters,
			APIURL:          apiURL,
			FixedParams:     fixedParams,
			APICallStrategy: interfaces.ResultProcessStrategyKnActionRecall,
		}
	}

	// 11. Build request headers.
	headers := common.GetHeaderForChildOperation(ctx, "ontology.action.execute", 1)

	return &interfaces.KnActionRecallResponse{
		Headers:      headers,
		DynamicTools: []interfaces.KnDynamicTool{dynamicTool},
	}, nil
}

// buildActionDriverAPIURL builds the internal action driver execution endpoint.
// Tool and MCP sources use the same api_url format.
func (s *knActionRecallServiceImpl) buildActionDriverAPIURL(knID, atID string) string {
	servicePath := fmt.Sprintf("/api/ontology-query/in/v1/knowledge-networks/%s/action-types/%s/execute", knID, atID)
	return s.config.OntologyQuery.BuildURL(servicePath)
}
