// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knlogicpropertyresolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// A caller allowed to view an object type may have its tool-backed logic properties computed,
// whether or not it holds a grant on the tool box behind them: ontology-query runs the tool as the
// knowledge network's managed proxy. Generating the tool's parameters needs the tool's schema, and
// reading it only as the caller meant a caller without a tool box grant — the usual case once
// authorization follows the network — had its parameters guessed from no schema at all, with
// nothing but a warning in the log.
//
// The direct read still answers for callers that hold the grant. For the rest, the definition is
// read as the proxy, the same principal that runs the tool, and only after two checks, each
// fail-closed — the same two get_action_info makes for an action's tool (#1548):
//
//  1. the caller holds view_detail on the object type, checked here against Safe rather than
//     inferred from the object type having been read;
//  2. BKN confirms the tool box is this logic property's published binding and returns the proxy
//     account, so a box the property does not name cannot be reached.
//
// Only the tool's name, description and schemas come back.

// callerLacksToolGrant reports whether the direct read was refused for want of a grant on the tool
// itself. Only that refusal is replaced; every other outcome, a 401 included, stands.
func callerLacksToolGrant(err error) bool {
	status, ok := infraErr.HTTPStatus(err)
	return ok && status == http.StatusForbidden
}

// logicPropertyBindingID is the child id BKN derives for a logic property's proxy grant source:
// the object type and property name, scoped to the network. It must match bkn-backend's
// stableProxySourceID and ontology-query's logicPropertyBindingID byte for byte.
func logicPropertyBindingID(knID, otID, propertyName string) string {
	propertyKey := strings.Join([]string{otID, propertyName}, "\x00")
	digest := sha256.Sum256([]byte(strings.Join([]string{
		knID, interfaces.KNProxyChildTypeLogicProperty, propertyKey,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

// readBoundToolDefinition reads the definition of the tool a logic property is computed by: as the
// caller first, and as the network's proxy when the caller is refused for want of a tool grant.
func (s *knLogicPropertyResolverService) readBoundToolDefinition(ctx context.Context, knID, otID,
	propertyName, boxID, toolID string) (*interfaces.GetToolDetailResponse, error) {
	if s.toolReader == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	detail, err := s.toolReader.GetToolDetail(ctx, &interfaces.GetToolDetailRequest{BoxID: boxID, ToolID: toolID})
	if !callerLacksToolGrant(err) {
		return detail, err
	}
	// View on the object type covers its logic properties; the tool box grant is not needed.
	proxy, err := s.resolveLogicPropertyProxy(ctx, knID, otID, propertyName, boxID)
	if err != nil {
		return nil, err
	}
	detail, err = s.toolReaderAs.GetToolDetailAs(ctx, proxy, &interfaces.GetToolDetailRequest{
		BoxID: boxID, ToolID: toolID,
	})
	s.logProxiedRead(ctx, knID, otID, propertyName, proxy, boxID, toolID, err)
	return detail, err
}

// resolveLogicPropertyProxy runs the caller check and the binding check, in that order: the
// caller's own grant decides whether the proxy is asked for at all.
func (s *knLogicPropertyResolverService) resolveLogicPropertyProxy(ctx context.Context, knID, otID,
	propertyName, boxID string) (interfaces.AccountIdentity, error) {
	if s.objectTypeAuthz == nil || s.proxyResolver == nil || s.toolReaderAs == nil {
		return interfaces.AccountIdentity{}, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			infraErr.LocalizedDetail(ctx, "ToolAuthorizationUnavailable"))
	}
	if err := s.objectTypeAuthz.AuthorizeObjectTypeView(ctx, knID, otID); err != nil {
		return interfaces.AccountIdentity{}, err
	}
	mapping, err := s.proxyResolver.ResolveKNProxyBinding(ctx, interfaces.KNProxyBinding{
		KNID:      knID,
		ChildType: interfaces.KNProxyChildTypeLogicProperty,
		ChildID:   logicPropertyBindingID(knID, otID, propertyName),
		// The target type is left out: BKN records the tool box's own kind, which the logic
		// property does not carry. Box, child and operation still pin the binding.
		TargetID:  boxID,
		Operation: interfaces.KNProxyOperationExecute,
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
func (s *knLogicPropertyResolverService) logProxiedRead(ctx context.Context, knID, otID, propertyName string,
	proxy interfaces.AccountIdentity, boxID, toolID string, err error) {
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
	s.logger.WithContext(ctx).Infof("[KnLogicPropertyResolver] tool definition read as proxy: "+
		"caller=%s kn_id=%s ot_id=%s property=%s proxy=%s tool_box=%s tool=%s result=%s",
		callerID, knID, otID, propertyName, proxy.ID, boxID, toolID, result)
}
