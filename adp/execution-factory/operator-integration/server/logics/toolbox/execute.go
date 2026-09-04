package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
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
	proxyReq := &interfaces.HTTPRequest{
		ClientID: req.ToolID,
		HTTPRouter: interfaces.HTTPRouter{
			URL:    url,
			Method: metadata.GetMethod(),
		},
		HTTPRequestParams: req.HTTPRequestParams,
		Timeout:           time.Duration(req.Timeout) * time.Second,
	}
	proxyReq.Headers = utils.SanitizeThirdPartyHeaders(proxyReq.Headers)
	// After sanitizing, never before: the sanitizer strips exactly this header
	// family on the way to a third party, and this deployment's own function
	// runtime is not one.
	if tool.SourceType == model.SourceTypeFunction {
		if isPlatformFunctionTarget(url) {
			proxyReq.Headers = functionRuntimeHeaders(proxyReq.Headers, req)
		} else {
			s.logWithheldManagedContext(ctx, req, url)
		}
	}
	resp, err = s.Proxy.HandlerRequest(ctx, proxyReq)
	return
}

// logWithheldManagedContext records the one case an operator cannot otherwise
// diagnose: a managed call whose Function does not resolve to this deployment's
// runtime, so the credential was withheld on purpose.
//
// Inside the sandbox this surfaces only as sandbox_sdk.bkn reporting "not
// configured", which reads identically to an unmanaged call. Without this line
// the address is the one thing nobody can see.
//
// Host and tool identity only: the credential and the Interaction ids are the
// values being withheld, and logging them here would put them in the log
// instead.
func (s *ToolServiceImpl) logWithheldManagedContext(ctx context.Context, req *interfaces.ExecuteToolReq, rawURL string) {
	if s.Logger == nil || req == nil ||
		req.RequestAuthorization == "" || req.BKNConversationID == "" || req.BKNInteractionID == "" {
		return
	}
	host := "unparsable"
	if target, err := neturl.Parse(rawURL); err == nil {
		host = target.Scheme + "://" + target.Host
	}
	s.Logger.WithContext(ctx).Warnf(
		"managed context withheld from function tool %s in box %s: target %s is not this deployment's function runtime (%s)",
		req.ToolID, req.BoxID, host, interfaces.AOIServerURL)
}

// isPlatformFunctionTarget reports whether a Function tool actually resolves to
// this deployment's own function runtime.
//
// SourceTypeFunction is not by itself proof of that. Registration pins the
// address to AOIServerURL, but import takes metadata.server_url from the
// payload verbatim (see impex.go), so a toolbox can be imported with a Function
// whose address is any host the importer chose. Forwarding the caller's live
// credential on that basis would hand an arbitrary external endpoint the token
// of whoever invoked the tool — a strictly worse outcome than the accepted one,
// where the token stays inside this deployment's sandbox.
//
// Scheme and host must match the configured runtime, and the path must be the
// internal function-exec route. A tool that fails the check still executes; it
// simply receives no credential and no Interaction, exactly like an unmanaged
// call.
func isPlatformFunctionTarget(rawURL string) bool {
	target, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	runtime, err := neturl.Parse(interfaces.AOIServerURL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(target.Scheme, runtime.Scheme) || !strings.EqualFold(target.Host, runtime.Host) {
		return false
	}
	// GetAOIFuncExecPath ends in the :version placeholder; the prefix before it
	// is what every registered version shares.
	prefix := strings.TrimSuffix(interfaces.GetAOIFuncExecPath(), ":version")
	return strings.HasPrefix(target.Path, prefix)
}

// functionRuntimeHeaders forwards the authenticated caller and its managed
// Interaction to a Function tool, so the code it runs can read BKN as the
// principal that invoked it, inside the Interaction that invoked it.
//
// Only for Function tools resolving to this deployment's own runtime: every
// other address is a third party, and the sanitizer above is what keeps
// platform identity away from it.
//
// All three values must be present. A partial context means the call did not
// come through a managed Interaction, and a credential without the lifecycle
// guard it belongs to is exactly what must not reach a pooled sandbox.
//
// Server-captured values win over anything in the body: a Tool that could state
// them would be stating whose credential it runs under.
func functionRuntimeHeaders(headers map[string]any, req *interfaces.ExecuteToolReq) map[string]any {
	if req == nil || req.RequestAuthorization == "" ||
		req.BKNConversationID == "" || req.BKNInteractionID == "" {
		return headers
	}
	forwarded := make(map[string]any, len(headers)+3)
	for key, value := range headers {
		forwarded[key] = value
	}
	forwarded["Authorization"] = req.RequestAuthorization
	forwarded[string(interfaces.HeaderBKNConversationID)] = req.BKNConversationID
	forwarded[string(interfaces.HeaderBKNInteractionID)] = req.BKNInteractionID
	return forwarded
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
