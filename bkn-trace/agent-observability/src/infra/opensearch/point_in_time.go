// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxPointInTimeResponseBytes = 32 << 20

var ErrPointInTimeResponseBudgetExceeded = errors.New("receipt snapshot response exceeds resource budget")

func (c *Client) OpenPointInTime(ctx context.Context, index string) (string, error) {
	body, err := c.pointInTimeRequest(ctx, http.MethodPost, "/"+strings.Trim(index, "/")+"/_search/point_in_time?keep_alive=1m&allow_partial_pit_creation=false", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		ID     string `json:"pit_id"`
		Shards struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode receipt snapshot: %w", err)
	}
	if result.ID == "" || result.Shards.Failed != 0 {
		return "", fmt.Errorf("incomplete receipt snapshot")
	}
	return result.ID, nil
}

func (c *Client) SearchPointInTime(ctx context.Context, pitID string, query []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(query, &body); err != nil {
		return nil, fmt.Errorf("decode snapshot query: %w", err)
	}
	body["pit"] = map[string]any{"id": pitID, "keep_alive": "1m"}
	// A failed shard must invalidate the count instead of returning a partial page.
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	response, err := c.pointInTimeRequest(ctx, http.MethodPost, "/_search?allow_partial_search_results=false", raw)
	if err != nil {
		return nil, err
	}
	var state struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if err := json.Unmarshal(response, &state); err != nil {
		return nil, fmt.Errorf("decode snapshot search state: %w", err)
	}
	if state.TimedOut || state.Shards.Failed != 0 {
		return nil, fmt.Errorf("receipt snapshot search incomplete")
	}
	return response, nil
}

func (c *Client) ClosePointInTime(ctx context.Context, pitID string) error {
	raw, err := json.Marshal(map[string]any{"pit_id": []string{pitID}})
	if err != nil {
		return err
	}
	body, err := c.pointInTimeRequest(ctx, http.MethodDelete, "/_search/point_in_time", raw)
	if err != nil {
		return err
	}
	var result struct {
		PITs []struct {
			Successful bool `json:"successful"`
		} `json:"pits"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode snapshot close: %w", err)
	}
	if len(result.PITs) != 1 || !result.PITs[0].Successful {
		return fmt.Errorf("receipt snapshot close was not acknowledged")
	}
	return nil
}

func (c *Client) pointInTimeRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create receipt snapshot request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute receipt snapshot request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxPointInTimeResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read receipt snapshot response: %w", err)
	}
	if len(raw) > maxPointInTimeResponseBytes {
		return nil, ErrPointInTimeResponseBudgetExceeded
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("receipt snapshot request failed with HTTP %d", response.StatusCode)
	}
	return raw, nil
}
