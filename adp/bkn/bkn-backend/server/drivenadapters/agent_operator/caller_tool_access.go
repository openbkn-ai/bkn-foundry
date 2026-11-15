// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package agent_operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var (
	callerToolsOnce sync.Once
	callerTools     interfaces.CallerToolAccess
)

// NewCallerToolAccess returns the client of the execution factory's caller
// routes, which check the account in the headers with its own permissions.
func NewCallerToolAccess(appSetting *common.AppSetting) interfaces.CallerToolAccess {
	callerToolsOnce.Do(func() {
		callerTools = &agentOperatorAccess{
			appSetting:       appSetting,
			agentOperatorURL: appSetting.AgentOperatorUrl,
			httpClient:       common.NewHTTPClient(),
		}
	})
	return callerTools
}

func (aoa *agentOperatorAccess) callerToolURL(boxID, toolID, kind string) string {
	return fmt.Sprintf("%s/caller/tool-box/%s/%s/%s", aoa.agentOperatorURL,
		neturl.PathEscape(boxID), kind, neturl.PathEscape(toolID))
}

// GetCallerTool reads a tool (GET .../caller/tool-box/{box_id}/tool/{tool_id}).
func (aoa *agentOperatorAccess) GetCallerTool(ctx context.Context, boxID, toolID string) (*interfaces.CallerTool, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetCallerTool")
	defer span.End()
	url := aoa.callerToolURL(boxID, toolID, "tool")
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl: url, HttpMethod: http.MethodGet, HttpContentType: rest.ContentTypeJson,
	})
	status, body, err := aoa.httpClient.GetNoUnmarshal(ctx, url, nil, aoa.execFactoryHeaders(ctx))
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, status, "InternalError", "Http get caller tool failed")
		return nil, executionDependencyTransportError("get_caller_tool", status, err)
	}
	if status != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, status, "DependencyError", "Get caller tool failed")
		return nil, interfaces.NewDependencyError("execution-factory", "get_caller_tool",
			executionDependencyKindForStatus(status), status)
	}
	var payload struct {
		ToolID     string `json:"tool_id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		UpdateTime int64  `json:"update_time"`
		Metadata   struct {
			FunctionContent *struct {
				ScriptType  string `json:"script_type"`
				KnID        string `json:"kn_id"`
				ResultShape string `json:"result_shape"`
				Inputs      []struct {
					Name     string `json:"name"`
					Type     string `json:"type"`
					Required bool   `json:"required"`
				} `json:"inputs"`
			} `json:"function_content"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.ToolID != toolID {
		oteltrace.AddHttpAttrs4Error(span, status, "DependencyError", "Caller tool response was invalid")
		return nil, interfaces.NewDependencyError("execution-factory", "get_caller_tool",
			interfaces.DependencyInvalidResponse, status)
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	tool := &interfaces.CallerTool{
		ToolID: payload.ToolID, Name: payload.Name, Status: payload.Status, UpdateTime: payload.UpdateTime,
	}
	if function := payload.Metadata.FunctionContent; function != nil {
		tool.ScriptType, tool.KnID, tool.ResultShape = function.ScriptType, function.KnID, function.ResultShape
		for _, input := range function.Inputs {
			tool.Inputs = append(tool.Inputs, interfaces.CallerToolInput{
				Name: input.Name, Type: input.Type, Required: input.Required,
			})
		}
	}
	return tool, nil
}

// ExecuteCallerTool runs a tool (POST .../caller/tool-box/{box_id}/proxy/{tool_id})
// with the arguments as its body. The execution factory answers 200 for a tool
// that ran, whatever the tool said; what it said is in the reply.
func (aoa *agentOperatorAccess) ExecuteCallerTool(ctx context.Context, boxID, toolID string,
	arguments map[string]any, timeout time.Duration) (*interfaces.CallerToolReply, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "ExecuteCallerTool")
	defer span.End()
	url := aoa.callerToolURL(boxID, toolID, "proxy")
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl: url, HttpMethod: http.MethodPost, HttpContentType: rest.ContentTypeJson,
	})
	if arguments == nil {
		arguments = map[string]any{}
	}
	// Encoded here so that numbers go as they were written.
	request, err := json.Marshal(map[string]any{"timeout": int(timeout / time.Second), "body": arguments})
	if err != nil {
		return nil, fmt.Errorf("encode the arguments of tool %s: %w", toolID, err)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	status, body, err := aoa.httpClient.PostNoUnmarshal(callCtx, url, aoa.execFactoryHeaders(ctx), request)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, status, "InternalError", "Http execute caller tool failed")
		return nil, executionDependencyTransportError("execute_caller_tool", status, err)
	}
	if status != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, status, "DependencyError", "Execute caller tool failed")
		return nil, interfaces.NewDependencyError("execution-factory", "execute_caller_tool",
			executionDependencyKindForStatus(status), status)
	}
	var reply struct {
		StatusCode int             `json:"status_code"`
		Body       json.RawMessage `json:"body"`
		Error      string          `json:"error"`
	}
	if err := json.Unmarshal(body, &reply); err != nil || reply.StatusCode == 0 {
		oteltrace.AddHttpAttrs4Error(span, status, "DependencyError", "Caller tool reply was invalid")
		return nil, interfaces.NewDependencyError("execution-factory", "execute_caller_tool",
			interfaces.DependencyInvalidResponse, status)
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	return &interfaces.CallerToolReply{StatusCode: reply.StatusCode, Body: reply.Body, Error: reply.Error}, nil
}
