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
