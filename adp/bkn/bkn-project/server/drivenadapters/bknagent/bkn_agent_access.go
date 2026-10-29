// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	headerAccountID   = "x-account-id"
	headerAccountType = "x-account-type"
)

var (
	baOnce   sync.Once
	baAccess interfaces.BknAgentAccess
)

type bknAgentAccess struct {
	httpClient rest.HTTPClient
	baseUrl    string
}

func NewBknAgentAccess(appSetting *common.AppSetting) interfaces.BknAgentAccess {
	baOnce.Do(func() {
		baAccess = &bknAgentAccess{httpClient: common.NewHTTPClient(), baseUrl: appSetting.BknAgentUrl}
	})
	return baAccess
}

func headers(account interfaces.BknAgentAccount) map[string]string {
	return map[string]string{
		headerAccountID:     account.ID,
		headerAccountType:   account.Type,
		rest.ContentTypeKey: rest.ContentTypeJson,
	}
}

// agentError reads bkn-agent's error envelope, a flat object with code and description. A body
// that is not one, such as a gateway's page, leaves the code empty.
func agentError(respCode int, respBody []byte) error {
	agentErr := &interfaces.BknAgentError{HTTPCode: respCode}
	_ = json.Unmarshal(respBody, agentErr)
	return agentErr
}

// RunTask calls POST /run. bkn-agent answers 404 BknAgent.NotFound for a missing Agent and 400
// BknAgent.Task.ModeMismatch for one that is not a task Agent.
func (ba *bknAgentAccess) RunTask(ctx context.Context, account interfaces.BknAgentAccount,
	req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "RunBknAgentTask")
	defer span.End()
	span.SetAttributes(attr.Key("agent_id").String(req.AgentID))

	httpUrl := ba.baseUrl + "/run"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	respCode, respBody, err := ba.httpClient.PostNoUnmarshal(ctx, httpUrl, headers(account), req)
	logger.Debugf("RunBknAgentTask finished, agent_id is [%s], response code is [%d]", req.AgentID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http run task failed")
		return nil, fmt.Errorf("run agent %s: %w", req.AgentID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, agentError(respCode, respBody)
	}
	task, err := readTask(respBody)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal task failed")
		return nil, err
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return task, nil
}

// GetTask calls GET /tasks/{id}. bkn-agent answers 404 BknAgent.NotFound for a task the account
// did not start, as for a missing one.
func (ba *bknAgentAccess) GetTask(ctx context.Context, account interfaces.BknAgentAccount,
	taskID string) (*interfaces.BknAgentTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetBknAgentTask")
	defer span.End()
	span.SetAttributes(attr.Key("task_id").String(taskID))

	httpUrl := ba.baseUrl + "/tasks/" + url.PathEscape(taskID)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodGet})

	respCode, respBody, err := ba.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers(account))
	logger.Debugf("GetBknAgentTask finished, task_id is [%s], response code is [%d]", taskID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get task failed")
		return nil, fmt.Errorf("get agent task %s: %w", taskID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, agentError(respCode, respBody)
	}
	task, err := readTask(respBody)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal task failed")
		return nil, err
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return task, nil
}

// readTask reads bkn-agent's TaskOut; output and failure_detail are null until they are set.
func readTask(respBody []byte) (*interfaces.BknAgentTask, error) {
	var out struct {
		TaskID        string  `json:"task_id"`
		AgentID       string  `json:"agent_id"`
		Status        string  `json:"status"`
		Output        *string `json:"output"`
		FailureDetail *string `json:"failure_detail"`
		CreateTime    int64   `json:"create_time"`
		UpdateTime    int64   `json:"update_time"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("unmarshal agent task: %w", err)
	}
	if out.TaskID == "" {
		return nil, fmt.Errorf("agent task reply has no task_id")
	}
	task := &interfaces.BknAgentTask{
		TaskID: out.TaskID, AgentID: out.AgentID, Status: out.Status,
		CreateTime: out.CreateTime, UpdateTime: out.UpdateTime,
	}
	if out.Output != nil {
		task.Output = *out.Output
	}
	if out.FailureDetail != nil {
		task.FailureDetail = *out.FailureDetail
	}
	return task, nil
}

// AnswerOnNetwork calls POST /executions, which streams the answer as server-sent events. bkn-agent
// cancels an answer whose stream is closed, so the reply is read to its end; the terminal event
// says how the answer ended. Refusals come before the stream: 401 BknAgent.Auth.TokenRequired,
// 403 BknAgent.Execution.Forbidden for a network the caller may not use or that does not exist,
// 404 BknAgent.NotFound for an Agent missing or not published, 409 BknAgent.Execution.Conflict
// for a request id still answering or used for another question.
func (ba *bknAgentAccess) AnswerOnNetwork(ctx context.Context, token string,
	req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "AnswerOnNetwork")
	defer span.End()
	span.SetAttributes(attr.Key("agent_id").String(req.AgentID), attr.Key("kn_id").String(req.KNID))

	httpUrl := ba.baseUrl + "/executions"
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{HttpUrl: httpUrl, HttpMethod: http.MethodPost})

	body := map[string]any{
		"agent_id": req.AgentID, "message": req.Message, "client_request_id": req.ClientRequestID,
		"context": map[string]any{"kn_id": req.KNID},
	}
	reqHeaders := map[string]string{
		rest.ContentTypeKey: rest.ContentTypeJson,
		"Authorization":     token,
		"Accept":            "text/event-stream",
	}
	respCode, respBody, err := ba.httpClient.PostNoUnmarshal(ctx, httpUrl, reqHeaders, body)
	logger.Debugf("AnswerOnNetwork finished, agent_id is [%s], kn_id is [%s], response code is [%d]",
		req.AgentID, req.KNID, respCode)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http execution failed")
		return nil, fmt.Errorf("answer on network %s: %w", req.KNID, err)
	}
	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		return nil, agentError(respCode, respBody)
	}
	answer, err := readAnswer(respBody)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Read answer failed")
		return nil, err
	}
	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return answer, nil
}

// readAnswer reads the events of an answer: the tool calls as they finished, the result and the
// terminal event. A stream without a terminal event was cut short.
func readAnswer(respBody []byte) (*interfaces.BknAgentAnswer, error) {
	answer := &interfaces.BknAgentAnswer{}
	ended := false
	text := strings.ReplaceAll(string(respBody), "\r\n", "\n")
	for _, block := range strings.Split(text, "\n\n") {
		var name, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		var event struct {
			Status    string `json:"status"`
			ErrorKind string `json:"error_kind"`
			Content   struct {
				Answer string `json:"answer"`
			} `json:"content"`
			// Error is the tool's output on a failed tool call, and {code, detail} on the terminal event.
			Error json.RawMessage `json:"error"`
		}
		switch name {
		case "tool_finished", "result", "terminal":
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil, fmt.Errorf("unmarshal %s event: %w", name, err)
			}
		default:
			continue
		}
		switch name {
		case "tool_finished":
			answer.ToolCalls++
			if event.Status == "failed" {
				if event.ErrorKind == "unavailable" {
					answer.ToolsUnavailable++
				} else {
					answer.ToolsFailed++
				}
			}
		case "result":
			answer.Answer = event.Content.Answer
		case "terminal":
			var failure struct {
				Code string `json:"code"`
			}
			if len(event.Error) > 0 {
				if err := json.Unmarshal(event.Error, &failure); err != nil {
					return nil, fmt.Errorf("unmarshal terminal error: %w", err)
				}
			}
			answer.Status, answer.ErrorCode, ended = event.Status, failure.Code, true
		}
	}
	if !ended {
		return nil, fmt.Errorf("answer stream ended without its terminal event")
	}
	return answer, nil
}
