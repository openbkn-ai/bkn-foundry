// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	proxyexecution "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/proxy_execution"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// GetToolDefinitionAsProxy reads the invocation contract of the tool an action
// type is bound to, for that knowledge network's managed proxy (#1548).
//
// A caller allowed to view the action type may learn the parameters it would
// pass without holding a grant on the tool; the proxy's execute grant on the box
// stands in for it. What that grant justifies is bounded here: the tool must be
// one the proxy could run right now — inside the addressed, published box and
// enabled — and only its name, description and schemas leave. Source code and
// service topology stay behind.
func (s *ToolServiceImpl) GetToolDefinitionAsProxy(ctx context.Context,
	req *interfaces.GetToolDefinitionReq) (resp *interfaces.ToolDefinition, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"tool_id": req.ToolID,
	})
	// Authorization comes first, so a proxy that may not read learns nothing,
	// not even whether the box exists.
	if err = proxyexecution.AuthorizeDefinitionRead(ctx, s.ProxyAuthorizer, s.ProxyAudit); err != nil {
		return nil, err
	}

	exist, box, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if !exist {
		return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s not found", req.BoxID))
	}
	// The same lifecycle rule as execution (#1483): a box taken offline serves
	// nothing through the proxy, its contract included.
	if box.Status != string(interfaces.BizStatusPublished) {
		return nil, errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotAvailable,
			"toolbox not published", box.Name)
	}
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	// The grant is on the box, so a tool of any other box is outside it. Say
	// not found rather than where the tool lives.
	if !exist || tool.BoxID != box.BoxID {
		return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
	}
	if tool.Status != string(interfaces.ToolStatusTypeEnabled) {
		return nil, errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotAvailable,
			"tool not available", tool.Name)
	}

	info, err := s.getToolInfo(ctx, tool, box.ServerURL, interfaces.MetadataType(box.MetadataType))
	if err != nil {
		return nil, err
	}
	resp = &interfaces.ToolDefinition{
		BoxID:       box.BoxID,
		ToolID:      info.ToolID,
		Name:        info.Name,
		Description: info.Description,
	}
	if info.Metadata != nil && info.Metadata.APISpec != nil {
		spec := info.Metadata.APISpec
		resp.APISpec = &interfaces.ToolDefinitionSpec{
			Parameters:  spec.Parameters,
			RequestBody: spec.RequestBody,
			Responses:   spec.Responses,
			Components:  spec.Components,
		}
	}
	return resp, nil
}
