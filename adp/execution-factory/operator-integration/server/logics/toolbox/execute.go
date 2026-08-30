package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// DebugTool tool debugging.
func (s *ToolServiceImpl) DebugTool(ctx context.Context, req *interfaces.ExecuteToolReq) (resp *interfaces.HTTPResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"tool_id": req.ToolID,
		"user_id": req.UserID,
	})

	// Permission verification.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckExecutePermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed	, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Check if the tool exists.
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
		return
	}
	resp, err = s.executeTool(ctx, req, tool, toolBox.ServerURL)
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[ExecuteTool] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationExecute,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectTool,
				Name: toolBox.Name,
				ID:   toolBox.BoxID,
			},
			Detils: &metric.AuditLogToolDetils{
				Infos: []metric.AuditLogToolDetil{
					{
						ToolID:   tool.ToolID,
						ToolName: tool.Name,
					},
				},
				OperationCode: metric.DebugTool,
			},
		})
	}()
	return
}

// ExecuteTool tool execution.
func (s *ToolServiceImpl) ExecuteTool(ctx context.Context, req *interfaces.ExecuteToolReq) (resp *interfaces.HTTPResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer func() {
		telemetry.SetSpanAttributes(ctx, actionExecutionSpanAttrs(ctx, "action.execute", err, map[string]interface{}{
			"box_id":      req.BoxID,
			"tool_id":     req.ToolID,
			"user_id":     req.UserID,
			"bkn.tool.id": req.ToolID,
		}))
		oteltrace.EndSpan(ctx, err)
	}()
	action, actionEnabled := bkntrace.ParseAction(req.Headers, req.BoxID, req.ToolID, req.UserID)
	actionEvents := []bkntrace.Event{}
	actionApproved := false
	actionClaimed := false
	actionFinished := false
	emitAction := func() error {
		if actionEnabled && s.ActionEvidence != nil {
			return s.ActionEvidence.Emit(ctx, action, actionEvents)
		}
		return nil
	}
	defer func() {
		if actionEnabled && actionApproved && !actionFinished {
			result, _ := json.Marshal(resp)
			if actionClaimed && s.ActionExecutions != nil {
				if gateErr := s.ActionExecutions.Complete(ctx, action, result, err != nil); gateErr != nil {
					err = gateErr
				}
			}
			actionEvents = append(actionEvents, action.AfterExecution(result, err)...)
			if emitErr := emitAction(); emitErr != nil {
				if err == nil {
					err = emitErr
				} else {
					s.Logger.WithContext(ctx).Errorf("bkn trace terminal evidence emit failed: %T", emitErr)
				}
			}
		}
	}()
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		if actionEnabled {
			decision, _ := action.AfterPermission(err)
			actionEvents = append(actionEvents, decision...)
			if emitErr := emitAction(); emitErr != nil {
				s.Logger.WithContext(ctx).Errorf("bkn trace rejected evidence emit failed: %T", emitErr)
			}
		}
		return
	}
	err = s.AuthService.CheckExecutePermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		if actionEnabled {
			decision, _ := action.AfterPermission(err)
			actionEvents = append(actionEvents, decision...)
			if emitErr := emitAction(); emitErr != nil {
				s.Logger.WithContext(ctx).Errorf("bkn trace rejected evidence emit failed: %T", emitErr)
			}
		}
		return
	}
	if actionEnabled {
		decision, _ := action.AfterPermission(nil)
		actionEvents = append(actionEvents, decision...)
		actionApproved = true
		if emitErr := emitAction(); emitErr != nil {
			err = emitErr
			actionFinished = true
			return
		}
		actionEvents = nil
		if s.ActionExecutions == nil {
			err = bkntrace.ErrActionExecutionStore
			actionFinished = true
			return
		}
		state, gateErr := s.ActionExecutions.Acquire(ctx, action)
		if gateErr != nil {
			err = gateErr
			actionFinished = true
			return
		}
		if state.Completed {
			actionFinished = true
			var replayErr error
			if state.Failed {
				replayErr = bkntrace.ErrActionReplayFailed
			}
			actionEvents = append(actionEvents, action.AfterExecution(state.Result, replayErr)...)
			if emitErr := emitAction(); emitErr != nil {
				err = emitErr
				return
			}
			if len(state.Result) > 0 {
				_ = json.Unmarshal(state.Result, &resp)
			}
			err = replayErr
			return
		}
		actionClaimed = state.Acquired
	}
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Check if the tool exists.
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
		return
	}
	// Check if the tool is available.
	if tool.Status != string(interfaces.ToolStatusTypeEnabled) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotAvailable,
			"tool not available", tool.Name)
		return
	}
	resp, err = s.executeTool(ctx, req, tool, toolBox.ServerURL)
	if actionEnabled {
		result, _ := json.Marshal(resp)
		if gateErr := s.ActionExecutions.Complete(ctx, action, result, err != nil); gateErr != nil {
			err = gateErr
		}
		actionEvents = append(actionEvents, action.AfterExecution(result, err)...)
		actionFinished = true
		if emitErr := emitAction(); emitErr != nil && err == nil {
			err = emitErr
		}
	}
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[ExecuteTool] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationExecute,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectTool,
				Name: toolBox.Name,
				ID:   toolBox.BoxID,
			},
			Detils: &metric.AuditLogToolDetils{
				Infos: []metric.AuditLogToolDetil{
					{
						ToolID:   tool.ToolID,
						ToolName: tool.Name,
					},
				},
				OperationCode: metric.ExecuteTool,
			},
		})
	}()
	return resp, nil
}

// ExecuteToolCore executes the core logic of the tool (excluding permission verification and audit logs)
func (s *ToolServiceImpl) ExecuteToolCore(ctx context.Context, req *interfaces.ExecuteToolReq) (resp *interfaces.HTTPResponse, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer func() {
		telemetry.SetSpanAttributes(ctx, actionExecutionSpanAttrs(ctx, "action.execute", err, map[string]interface{}{
			"box_id":      req.BoxID,
			"tool_id":     req.ToolID,
			"user_id":     req.UserID,
			"bkn.tool.id": req.ToolID,
		}))
		oteltrace.EndSpan(ctx, err)
	}()
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Check if the tool exists.
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
		return
	}
	// Check if the tool is available.
	if tool.Status != string(interfaces.ToolStatusTypeEnabled) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotAvailable,
			"tool not available", tool.Name)
		return
	}
	resp, err = s.executeTool(ctx, req, tool, toolBox.ServerURL)
	return
}

func (s *ToolServiceImpl) executeTool(ctx context.Context, req *interfaces.ExecuteToolReq, tool *model.ToolDB, toolBoxURL string) (resp *interfaces.HTTPResponse, err error) {
	// Get metadata.
	exist, metadata, err := s.MetadataService.GetMetadataBySource(ctx, tool.SourceID, tool.SourceType)
	if err != nil {
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtMetadataNotFound,
			fmt.Sprintf("metadata type: %s id: %s not found", tool.SourceType, tool.SourceID))
		return
	}
	var url string
	switch tool.SourceType {
	case model.SourceTypeOpenAPI:
		if toolBoxURL == "" {
			toolBoxURL = metadata.GetServerURL()
		}
		url = fmt.Sprintf("%s%s", toolBoxURL, metadata.GetPath())
	case model.SourceTypeOperator:
		url = fmt.Sprintf("%s%s", metadata.GetServerURL(), metadata.GetPath())
	case model.SourceTypeFunction:
		url = fmt.Sprintf("%s%s", metadata.GetServerURL(), metadata.GetPath())
	}
	params := req.HTTPRequestParams
	if tool.SourceType == model.SourceTypeFunction {
		params = functionRuntimeHeaders(params, req, url)
	}
	proxyReq := &interfaces.HTTPRequest{
		ClientID: req.ToolID,
		HTTPRouter: interfaces.HTTPRouter{
			URL:    url,
			Method: metadata.GetMethod(),
		},
		HTTPRequestParams: params,
		Timeout:           time.Duration(req.Timeout) * time.Second,
	}
	resp, err = s.Proxy.HandlerRequest(ctx, proxyReq)
	return
}

// functionRuntimeHeaders adds server-captured identity and lifecycle headers only for the
// platform-owned Function Runtime. Imported Function metadata can contain arbitrary server URLs,
// so sending these headers to any other endpoint would disclose the caller credential.
func functionRuntimeHeaders(params interfaces.HTTPRequestParams, req *interfaces.ExecuteToolReq, targetURL string) interfaces.HTTPRequestParams {
	if !isTrustedFunctionRuntime(targetURL) || req == nil || req.RequestAuthorization == "" || req.BKNConversationID == "" || req.BKNInteractionID == "" {
		return params
	}
	headers := make(map[string]any, len(params.Headers)+3)
	for key, value := range params.Headers {
		headers[key] = value
	}
	headers["Authorization"] = req.RequestAuthorization
	headers["bkn-conversation-id"] = req.BKNConversationID
	headers["bkn-interaction-id"] = req.BKNInteractionID
	params.Headers = headers
	return params
}

func isTrustedFunctionRuntime(targetURL string) bool {
	target, err := url.Parse(targetURL)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return false
	}
	trusted, err := url.Parse(interfaces.AOIServerURL)
	if err != nil || target.Scheme != trusted.Scheme || target.Host != trusted.Host {
		return false
	}
	expectedPathPrefix := strings.TrimRight(interfaces.AOPInternalV1Prefix, "/") + "/function/exec/"
	version := strings.TrimPrefix(target.EscapedPath(), expectedPathPrefix)
	return version != target.EscapedPath() && version != "" && !strings.Contains(version, "/")
}

func actionExecutionSpanAttrs(ctx context.Context, operation string, err error, refs map[string]interface{}) map[string]interface{} {
	attrs := map[string]interface{}{
		"bkn.module.name":          "action-execution",
		"bkn.operation.name":       operation,
		"bkn.trace.schema.version": "1.0.0",
		"bkn.status":               "ok",
	}
	if err != nil {
		attrs["bkn.status"] = "error"
	}
	if traceContext, ok := common.GetTraceContextFromCtx(ctx); ok {
		attrs["bkn.request.id"] = traceContext.RequestID
	}
	for key, value := range refs {
		if value != "" && value != nil {
			attrs[key] = value
		}
	}
	return attrs
}
