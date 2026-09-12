// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

var ErrResponseReadBudget = errors.New("opensearch response read budget exceeded")
var ErrInvalidReadBudget = errors.New("opensearch read budget must be positive and below MaxInt64")

// GetDocumentBounded reads at most maxResponseBytes plus one probe byte, including
// non-success responses. ReadBytes counts decoded HTTP body bytes, not wire bytes
// or peak allocations. It neither creates an index nor drains an oversized body.
func (c *Client) GetDocumentBounded(ctx context.Context, index, documentID string, maxResponseBytes int64) (Document, int64, error) {
	if maxResponseBytes <= 0 || maxResponseBytes == math.MaxInt64 {
		return Document{}, 0, ErrInvalidReadBudget
	}
	requestURL := fmt.Sprintf("%s/%s/_doc/%s", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(documentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Document{}, 0, fmt.Errorf("create bounded opensearch get request: %w", err)
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	// A followed redirect drains its body inside net/http, outside our byte
	// counter. Keep this read on its original response without changing the
	// shared client's redirect policy or the legacy GetDocument behavior.
	readClient := *c.httpClient
	readClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := readClient.Do(req)
	if err != nil {
		return Document{}, 0, fmt.Errorf("execute bounded opensearch get request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	readBytes := int64(len(body))
	if readBytes > maxResponseBytes {
		return Document{}, readBytes, ErrResponseReadBudget
	}
	if err != nil {
		return Document{}, readBytes, fmt.Errorf("read bounded opensearch get response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return Document{}, readBytes, ErrDocumentNotFound
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// The response can contain source data. Status is sufficient for diagnostics.
		return Document{}, readBytes, &StatusError{Operation: "bounded get", StatusCode: resp.StatusCode}
	}
	var response struct {
		Source      json.RawMessage `json:"_source"`
		SeqNo       int64           `json:"_seq_no"`
		PrimaryTerm int64           `json:"_primary_term"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Document{}, readBytes, fmt.Errorf("decode bounded opensearch get response: %w", err)
	}
	return Document{Source: response.Source, SeqNo: response.SeqNo, PrimaryTerm: response.PrimaryTerm}, readBytes, nil
}
