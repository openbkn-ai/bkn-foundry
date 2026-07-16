// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package bkn_agent provides bkn-agent access for semantic-understanding tasks.
package bkn_agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	baAccessOnce sync.Once
	baAccess     interfaces.BknAgentAccess
)

type bknAgentAccess struct {
	appSetting *common.AppSetting
	httpClient rest.HTTPClient
	baseURL    string
}

func NewBknAgentAccess(appSetting *common.AppSetting) interfaces.BknAgentAccess {
	baAccessOnce.Do(func() {
		baAccess = &bknAgentAccess{
			appSetting: appSetting,
			httpClient: common.NewHTTPClient(),
			baseURL:    strings.TrimRight(appSetting.BknAgentUrl, "/"),
		}
	})
	return baAccess
}

func (baa *bknAgentAccess) Run(ctx context.Context, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentRunResponse, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Run bkn-agent task")
	defer span.End()

	if baa.baseURL == "" {
		return nil, fmt.Errorf("bkn-agent url is not configured")
	}

	url := fmt.Sprintf("%s/api/bkn-agent/v1/run", baa.baseURL)
	respCode, respBody, err := baa.httpClient.PostNoUnmarshal(ctx, url, jsonHeaders(), req)
	if err != nil {
		span.SetStatus(codes.Error, "Run bkn-agent task failed")
		otellog.LogError(ctx, "Run bkn-agent task failed", err)
		return nil, err
	}
	if respCode != http.StatusOK && respCode != http.StatusCreated && respCode != http.StatusAccepted {
		return nil, fmt.Errorf("run bkn-agent task failed with status code: %d, %s", respCode, respBody)
	}

	var resp interfaces.BknAgentRunResponse
	if err := sonic.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal bkn-agent run response failed: %w", err)
	}
	if resp.TaskID == "" {
		var fallback struct {
			ID string `json:"id"`
		}
		if err := sonic.Unmarshal(respBody, &fallback); err != nil {
			return nil, fmt.Errorf("unmarshal bkn-agent run response fallback id failed: %w", err)
		}
		resp.TaskID = fallback.ID
	}
	if resp.TaskID == "" {
		return nil, fmt.Errorf("bkn-agent run response missing task_id")
	}

	span.SetStatus(codes.Ok, "")
	return &resp, nil
}

func (baa *bknAgentAccess) GetTask(ctx context.Context, taskID string) (*interfaces.BknAgentTask, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "Get bkn-agent task")
	defer span.End()

	if baa.baseURL == "" {
		return nil, fmt.Errorf("bkn-agent url is not configured")
	}
	if taskID == "" {
		return nil, fmt.Errorf("agent task id is required")
	}

	url := fmt.Sprintf("%s/api/bkn-agent/v1/tasks/%s", baa.baseURL, taskID)
	respCode, respBody, err := baa.httpClient.GetNoUnmarshal(ctx, url, nil, jsonHeaders())
	if err != nil {
		span.SetStatus(codes.Error, "Get bkn-agent task failed")
		otellog.LogError(ctx, "Get bkn-agent task failed", err)
		return nil, err
	}
	if respCode != http.StatusOK {
		return nil, fmt.Errorf("get bkn-agent task failed with status code: %d, %s", respCode, respBody)
	}

	var task interfaces.BknAgentTask
	if err := sonic.Unmarshal(respBody, &task); err != nil {
		return nil, fmt.Errorf("unmarshal bkn-agent task response failed: %w", err)
	}
	if task.TaskID == "" {
		task.TaskID = task.ID
	}
	if len(task.Result) == 0 && len(task.ResultJSON) > 0 {
		task.Result = task.ResultJSON
	}

	span.SetStatus(codes.Ok, "")
	return &task, nil
}

func jsonHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}
