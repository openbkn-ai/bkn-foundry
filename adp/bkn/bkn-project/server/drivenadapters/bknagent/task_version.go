// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func (ba *bknAgentAccess) CurrentTaskVersion(ctx context.Context, account interfaces.BknAgentAccount, agent string) (int, error) {
	status, body, err := ba.httpClient.GetNoUnmarshal(ctx, ba.baseUrl+"/agents/"+url.PathEscape(agent)+"/task-version", nil, headers(account))
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, agentError(status, body)
	}
	var receipt struct {
		AgentID  string `json:"agent_id"`
		Version  int    `json:"agent_version"`
		Prompt   *int   `json:"prompt_version"`
		ToolFree bool   `json:"tool_free"`
	}
	if json.Unmarshal(body, &receipt) != nil || receipt.AgentID != agent || receipt.Version < 1 || receipt.Prompt == nil || *receipt.Prompt < 1 || !receipt.ToolFree {
		return 0, agentError(http.StatusConflict, nil)
	}
	return receipt.Version, nil
}

var _ interfaces.BknAgentVersionAccess = (*bknAgentAccess)(nil)
