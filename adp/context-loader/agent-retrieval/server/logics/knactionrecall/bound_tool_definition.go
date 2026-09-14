// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"context"
	"net/http"
	"strings"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A caller allowed to view an action type may learn the parameters it would pass, whether or not
// it holds a grant on the tool the action runs (#1548). The direct read answers for callers that
// do; for the rest, the definition is read through the knowledge network's managed proxy — the
// same principal that runs the action — and only after three checks, each fail-closed:
//
//  1. the caller holds view_detail on the action type, checked here against Safe rather than
//     inferred from ontology-query having answered;
//  2. BKN confirms the target is this action type's current published binding, so a box or MCP
//     server the action does not name cannot be reached;
//  3. Execution Factory rechecks the proxy's grant on the target and returns only the named
//     tool's name, description and schemas.
//
// A failure at any step is that step's answer; nothing retries as the caller or as the service.

// callerLacksToolGrant reports whether the direct read was refused for want of a grant on the tool
// itself. Only that refusal is replaced; every other outcome, a 401 included, stands.
func callerLacksToolGrant(err error) bool {
	status, ok := infraErr.HTTPStatus(err)
	return ok && status == http.StatusForbidden
}

// getBoundToolDefinitionAsProxy reads the bound Function tool's contract as the network's proxy.
func (s *knActionRecallServiceImpl) getBoundToolDefinitionAsProxy(ctx context.Context, knID, atID string,
	source *interfaces.ActionSource) (*interfaces.GetToolDetailResponse, error) {
	proxy, err := s.resolveActionDefinitionProxy(ctx, knID, atID,
		interfaces.KNProxyTargetTypeToolBox, source.BoxID, source.ToolID)
	if err != nil {
		return nil, err
	}
	return s.proxyReader.GetToolDefinitionAsProxy(ctx, &interfaces.GetToolDetailRequest{
		BoxID:  source.BoxID,
		ToolID: source.ToolID,
	}, proxy)
}

// getBoundMCPToolDefinitionAsProxy reads the bound MCP tool's contract as the network's proxy.
func (s *knActionRecallServiceImpl) getBoundMCPToolDefinitionAsProxy(ctx context.Context, knID, atID string,
	source *interfaces.ActionSource) (*interfaces.GetMCPToolDetailResponse, error) {
	proxy, err := s.resolveActionDefinitionProxy(ctx, knID, atID,
		interfaces.KNProxyTargetTypeMCP, source.McpID, source.ToolName)
	if err != nil {
		return nil, err
	}
	return s.proxyReader.GetMCPToolDefinitionAsProxy(ctx, &interfaces.GetMCPToolDetailRequest{
		McpID:    source.McpID,
		ToolName: source.ToolName,
	}, proxy)
}

// resolveActionDefinitionProxy runs the caller check and the binding check, in that order: the
// caller's own grant decides whether the proxy is asked for at all.
func (s *knActionRecallServiceImpl) resolveActionDefinitionProxy(ctx context.Context, knID, atID,
	targetType, targetID, toolRef string) (*interfaces.KNProxyExecution, error) {
	if s.actionAuthz == nil || s.proxyResolver == nil || s.proxyReader == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.actionAuthz.AuthorizeActionTypeView(ctx, knID, atID); err != nil {
		return nil, err
	}
	// An action source with no target names nothing the proxy could stand in for; the caller's
	// own refusal is the answer.
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(toolRef) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	binding := interfaces.KNProxyBinding{
		KNID:       knID,
		ChildType:  interfaces.KNProxyChildTypeActionType,
		ChildID:    atID,
		TargetType: targetType,
		TargetID:   targetID,
		Operation:  interfaces.KNProxyOperationExecute,
	}
	mapping, err := s.proxyResolver.ResolveKNProxyBinding(ctx, binding)
	if err != nil {
		return nil, err
	}
	return &interfaces.KNProxyExecution{Mapping: mapping, Binding: binding}, nil
}
