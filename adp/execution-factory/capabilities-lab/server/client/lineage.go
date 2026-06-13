package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type ToolSourceLineage struct {
	SourceID   string
	SourceType string
}

type impexExportResponse struct {
	Toolbox *struct {
		Configs []struct {
			BoxID string `json:"box_id"`
			Tools []struct {
				ToolID     string `json:"tool_id"`
				SourceID   string `json:"source_id"`
				SourceType string `json:"source_type"`
			} `json:"tools"`
		} `json:"configs"`
	} `json:"toolbox"`
}

func (c *OperatorIntegrationClient) GetToolSourceLineage(
	ctx context.Context,
	businessDomain, boxID, toolID, userID string,
) (*ToolSourceLineage, error) {
	path := fmt.Sprintf(
		"/api/agent-operator-integration/v1/impex/export/toolbox/%s",
		url.PathEscape(boxID),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-business-domain", businessDomain)
	if userID != "" {
		req.Header.Set("user_id", userID)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("impex export failed (%d)", res.StatusCode)
	}

	var payload impexExportResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode impex export: %w", err)
	}

	if payload.Toolbox == nil {
		return nil, fmt.Errorf("toolbox export empty")
	}

	for _, box := range payload.Toolbox.Configs {
		if box.BoxID != "" && box.BoxID != boxID {
			continue
		}
		for _, tool := range box.Tools {
			if tool.ToolID == toolID {
				return &ToolSourceLineage{
					SourceID:   tool.SourceID,
					SourceType: tool.SourceType,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("tool %s not found in toolbox export", toolID)
}
