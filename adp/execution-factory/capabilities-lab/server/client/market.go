package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type MarketToolbox struct {
	BoxID      string `json:"box_id"`
	BoxName    string `json:"box_name"`
	BoxDesc    string `json:"box_desc"`
	Status     string `json:"status"`
	UpdateTime int64  `json:"update_time"`
	IsInternal bool   `json:"is_internal"`
	ToolCount  int    `json:"-"`
}

type toolboxMarketResponse struct {
	pageResult
	Data []MarketToolbox `json:"data"`
}

func (c *OperatorIntegrationClient) ListToolboxMarket(
	ctx context.Context,
	businessDomain, keyword string,
	page, pageSize int,
) (*toolboxMarketResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/tool-box/market?%s", query.Encode())
	var resp toolboxMarketResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type MarketMcp struct {
	McpID       string `json:"mcp_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	UpdateTime  int64  `json:"update_time"`
}

type mcpMarketResponse struct {
	pageResult
	Data []MarketMcp `json:"data"`
}

func (c *OperatorIntegrationClient) ListMcpMarket(
	ctx context.Context,
	businessDomain, keyword string,
	page, pageSize int,
) (*mcpMarketResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/mcp/market/list?%s", query.Encode())
	var resp mcpMarketResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type MarketSkill struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	UpdateTime  int64  `json:"update_time"`
}

type skillMarketResponse struct {
	pageResult
	Data []MarketSkill `json:"data"`
}

func (c *OperatorIntegrationClient) ListSkillMarket(
	ctx context.Context,
	businessDomain, keyword string,
	page, pageSize int,
) (*skillMarketResponse, error) {
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", page))
	query.Set("page_size", fmt.Sprintf("%d", pageSize))
	if keyword != "" {
		query.Set("name", keyword)
	}

	path := fmt.Sprintf("/api/agent-operator-integration/v1/skills/market?%s", query.Encode())
	var resp skillMarketResponse
	if err := c.doJSON(ctx, http.MethodGet, path, businessDomain, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OperatorIntegrationClient) DownloadSkillMarketPackage(
	ctx context.Context,
	businessDomain, skillID, userID string,
) ([]byte, string, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/skills/market/%s/management/download",
		url.PathEscape(skillID),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, "", err
	}

	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		req.Header.Set("user_id", userID)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("skill market download failed (%d): %s", res.StatusCode, string(payload))
	}

	filename := skillID + ".zip"
	if cd := res.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(cd, "filename="); idx >= 0 {
			raw := strings.TrimSpace(strings.Trim(cd[idx+len("filename="):], `";`))
			if raw != "" {
				filename = raw
			}
		}
	}

	return payload, filename, nil
}
