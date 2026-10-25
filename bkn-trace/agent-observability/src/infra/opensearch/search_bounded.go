// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const MaxBoundedSearchBytes = 4 << 20
const maxConfigurableSearchBytes = 16 << 20

var ErrSearchResponseBudget = errors.New("opensearch bounded search response exceeds budget")
var ErrInvalidSearchBudget = errors.New("opensearch search budget must be positive and no larger than 16 MiB")

// SearchBounded keeps the existing 4 MiB default. A caller may explicitly choose
// one positive limit up to 16 MiB; an oversized response is never returned.
func (c *Client) SearchBounded(ctx context.Context, index string, query []byte, maxBytes ...int) ([]byte, error) {
	limit := MaxBoundedSearchBytes
	if len(maxBytes) > 1 {
		return nil, ErrInvalidSearchBudget
	}
	if len(maxBytes) == 1 {
		limit = maxBytes[0]
	}
	if limit <= 0 || limit > maxConfigurableSearchBytes {
		return nil, ErrInvalidSearchBudget
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s/_search", c.baseURL, strings.TrimLeft(index, "/")), bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("opensearch bounded search failed with status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if len(b) > limit {
		return nil, ErrSearchResponseBudget
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}
