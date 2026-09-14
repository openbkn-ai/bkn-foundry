// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knactionrecall

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A caller allowed to view an action type may learn the parameters it would pass, whether or not
// it holds a grant on the tool the action runs (#1548). The direct read answers for callers that
// do; for the rest, the definition is read as the knowledge network's managed proxy — the same
// principal that runs the action — and only after two checks, each fail-closed:
//
//  1. the caller holds view_detail on the action type, checked here against Safe rather than
//     inferred from ontology-query having answered;
//  2. BKN confirms the target is this action type's current published binding and returns the
//     proxy account, so a box or MCP Server the action does not name cannot be reached.
//
// The read itself goes through the same Execution Factory route as the direct read, with the proxy
// account as the principal; Execution Factory authorizes it on the proxy's execute grant. Only the
// tool's name, description and schemas come back. A failure at any step is that step's answer;
// nothing retries as the caller or as the service.

// callerLacksToolGrant reports whether the direct read was refused for want of a grant on the tool
// itself. Only that refusal is replaced; every other outcome, a 401 included, stands.
func callerLacksToolGrant(err error) bool {
	status, ok := infraErr.HTTPStatus(err)
	return ok && status == http.StatusForbidden
}

// getBoundToolDefinitionAsProxy reads the bound Function tool's definition as the network's proxy.
func (s *knActionRecallServiceImpl) getBoundToolDefinitionAsProxy(ctx context.Context, knID, atID string,
	source *interfaces.ActionSource) (*interfaces.GetToolDetailResponse, error) {
	proxy, err := s.resolveActionTypeProxy(ctx, knID, atID,
		interfaces.KNProxyTargetTypeToolBox, source.BoxID, source.ToolID)
	if err != nil {
		return nil, err
	}
	detail, err := s.toolReaderAs.GetToolDetailAs(ctx, proxy, &interfaces.GetToolDetailRequest{
		BoxID:  source.BoxID,
		ToolID: source.ToolID,
	})
	s.logProxiedRead(ctx, knID, atID, proxy, "tool_box", source.BoxID, source.ToolID, err)
	return detail, err
}

// getBoundMCPToolDefinitionAsProxy reads the bound MCP tool's definition as the network's proxy.
func (s *knActionRecallServiceImpl) getBoundMCPToolDefinitionAsProxy(ctx context.Context, knID, atID string,
	source *interfaces.ActionSource) (*interfaces.GetMCPToolDetailResponse, error) {
	proxy, err := s.resolveActionTypeProxy(ctx, knID, atID,
		interfaces.KNProxyTargetTypeMCP, source.McpID, source.ToolName)
	if err != nil {
		return nil, err
	}
	detail, err := s.toolReaderAs.GetMCPToolDetailAs(ctx, proxy, &interfaces.GetMCPToolDetailRequest{
		McpID:    source.McpID,
		ToolName: source.ToolName,
	})
	s.logProxiedRead(ctx, knID, atID, proxy, "mcp", source.McpID, source.ToolName, err)
	return detail, err
}

// resolveActionTypeProxy runs the caller check and the binding check, in that order: the caller's
// own grant decides whether the proxy is asked for at all.
func (s *knActionRecallServiceImpl) resolveActionTypeProxy(ctx context.Context, knID, atID,
	targetType, targetID, toolRef string) (interfaces.AccountIdentity, error) {
	if s.actionAuthz == nil || s.proxyResolver == nil || s.toolReaderAs == nil {
		return interfaces.AccountIdentity{}, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.actionAuthz.AuthorizeActionTypeView(ctx, knID, atID); err != nil {
		return interfaces.AccountIdentity{}, err
	}
	// An action source with no target names nothing the proxy could stand in for; the caller's
	// own refusal is the answer.
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(toolRef) == "" {
		return interfaces.AccountIdentity{}, infraErr.DefaultHTTPError(ctx, http.StatusForbidden,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	mapping, err := s.proxyResolver.ResolveKNProxyBinding(ctx, interfaces.KNProxyBinding{
		KNID:       knID,
		ChildType:  interfaces.KNProxyChildTypeActionType,
		ChildID:    atID,
		TargetType: targetType,
		TargetID:   targetID,
		Operation:  interfaces.KNProxyOperationExecute,
	})
	if err != nil {
		return interfaces.AccountIdentity{}, err
	}
	if mapping == nil || strings.TrimSpace(mapping.ProxyAccountID) == "" ||
		mapping.ProxyAccountType != string(interfaces.AccessorTypeApp) {
		return interfaces.AccountIdentity{}, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	return interfaces.AccountIdentity{ID: mapping.ProxyAccountID, Type: interfaces.AccessorTypeApp}, nil
}

// logProxiedRead records each read made as the proxy, with both principals and the target, so a
// definition the caller could not read directly can be traced back to the grant that allowed it.
func (s *knActionRecallServiceImpl) logProxiedRead(ctx context.Context, knID, atID string,
	proxy interfaces.AccountIdentity, targetType, targetID, toolRef string, err error) {
	callerID := ""
	if caller, ok := common.GetAccountAuthContextFromCtx(ctx); ok && caller != nil {
		callerID = caller.AccountID
	}
	result := "ok"
	if err != nil {
		result = "failed"
		if status, ok := infraErr.HTTPStatus(err); ok {
			result = strconv.Itoa(status)
		}
	}
	s.logger.WithContext(ctx).Infof("[KnActionRecall#GetActionInfo] tool definition read as proxy: "+
		"caller=%s kn_id=%s at_id=%s proxy=%s %s=%s tool=%s result=%s",
		callerID, knID, atID, proxy.ID, targetType, targetID, toolRef, result)
}
