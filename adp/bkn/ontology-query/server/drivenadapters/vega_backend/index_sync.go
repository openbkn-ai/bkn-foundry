// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega_backend

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

var _ interfaces.ActionIndexSyncBackend = (*vegaBackendAccess)(nil)

func NewIndexSyncAccess(setting *common.AppSetting) interfaces.ActionIndexSyncBackend {
	return &vegaBackendAccess{appSetting: setting, httpClient: common.NewHTTPClient(), baseURL: setting.VegaBackendUrl}
}

func (v *vegaBackendAccess) CreateKeyBuild(ctx context.Context, resourceID string, keys []map[string]any) (string, error) {
	headers, err := callerHeaders(ctx)
	if err != nil {
		return "", err
	}
	code, body, err := v.httpClient.PostNoUnmarshal(ctx, v.baseURL+"/build-tasks", headers, map[string]any{
		"resource_id": resourceID, "mode": "batch", "execute_type": "keys", "keys": keys,
	})
	if err != nil {
		return "", fmt.Errorf("create keys build request failed: %w", err)
	}
	if code != http.StatusCreated {
		return "", interfaces.NewVegaDownstreamError(code, "")
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := common.UnmarshalJSON(body, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", fmt.Errorf("keys build response is missing its task id")
	}
	return response.ID, nil
}

func (v *vegaBackendAccess) GetKeyBuild(ctx context.Context, taskID string) (string, error) {
	headers, err := callerHeaders(ctx)
	if err != nil {
		return "", err
	}
	code, body, err := v.httpClient.GetNoUnmarshal(ctx, v.baseURL+"/build-tasks/"+url.PathEscape(taskID), nil, headers)
	if err != nil {
		return "", fmt.Errorf("read keys build request failed: %w", err)
	}
	if code != http.StatusOK {
		return "", interfaces.NewVegaDownstreamError(code, "")
	}
	var response struct {
		Status      string `json:"status"`
		ExecuteType string `json:"execute_type"`
	}
	if err := common.UnmarshalJSON(body, &response); err != nil {
		return "", err
	}
	if response.ExecuteType != "keys" {
		return "", fmt.Errorf("build task is not a keys task")
	}
	return response.Status, nil
}

func (v *vegaBackendAccess) RetryKeyBuild(ctx context.Context, taskID string) error {
	headers, err := callerHeaders(ctx)
	if err != nil {
		return err
	}
	code, _, err := v.httpClient.PostNoUnmarshal(ctx, v.baseURL+"/build-tasks/"+url.PathEscape(taskID)+"/start", headers, map[string]any{"reset": false})
	if err != nil {
		return fmt.Errorf("retry keys build request failed: %w", err)
	}
	if code != http.StatusAccepted {
		return interfaces.NewVegaDownstreamError(code, "")
	}
	return nil
}
