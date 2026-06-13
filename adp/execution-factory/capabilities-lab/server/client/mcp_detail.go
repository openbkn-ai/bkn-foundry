package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type McpDetail struct {
	McpID       string `json:"mcp_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	Mode        string `json:"mode"`
	UpdateTime  int64  `json:"update_time"`
}

func (c *OperatorIntegrationClient) GetMcp(
	ctx context.Context,
	businessDomain, mcpID string,
) (*McpDetail, error) {
	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/%s", url.PathEscape(mcpID))

	var resp McpDetail
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}
