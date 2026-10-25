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
	"net/url"
	"strings"
)

// FrozenDocument identifies one concrete document version, never an alias-wide query.
type FrozenDocument struct {
	Index       string `json:"index"`
	ID          string `json:"id"`
	SeqNo       *int64 `json:"seq_no"`
	PrimaryTerm *int64 `json:"primary_term"`
	Routing     string `json:"routing,omitempty"`
}

const frozenDeleteBatchSize = 500
const frozenDeleteResponseBytes = 4 << 20

// DeleteFrozenDocuments deletes only frozen versions. Earlier successful items
// may remain deleted after an error; retrying the same identities is safe.
func (c *Client) DeleteFrozenDocuments(ctx context.Context, documents []FrozenDocument) error {
	// Validate the entire selection before sending even its first batch.
	for _, d := range documents {
		if strings.TrimSpace(d.Index) == "" || strings.TrimSpace(d.ID) == "" || d.SeqNo == nil || *d.SeqNo < 0 || d.PrimaryTerm == nil || *d.PrimaryTerm <= 0 {
			return errors.New("frozen delete requires complete document version metadata")
		}
	}
	for start := 0; start < len(documents); start += frozenDeleteBatchSize {
		end := start + frozenDeleteBatchSize
		if end > len(documents) {
			end = len(documents)
		}
		if err := c.deleteFrozenBatch(ctx, documents[start:end]); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) deleteFrozenBatch(ctx context.Context, documents []FrozenDocument) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, d := range documents {
		meta := map[string]any{"_index": d.Index, "_id": d.ID, "if_seq_no": *d.SeqNo, "if_primary_term": *d.PrimaryTerm}
		if d.Routing != "" {
			meta["routing"] = d.Routing
		}
		if err := encoder.Encode(map[string]any{"delete": meta}); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/_bulk?refresh=true", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("frozen delete HTTP status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, frozenDeleteResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > frozenDeleteResponseBytes {
		return errors.New("frozen delete response exceeds budget")
	}
	var result struct {
		Errors *bool `json:"errors"`
		Items  []map[string]struct {
			Index  string          `json:"_index"`
			ID     string          `json:"_id"`
			Status int             `json:"status"`
			Result string          `json:"result"`
			Error  json.RawMessage `json:"error"`
			Shards *struct {
				Failed *int `json:"failed"`
			} `json:"_shards"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Errors == nil || len(result.Items) != len(documents) {
		return errors.New("frozen delete response is incomplete or failed")
	}
	confirmedAbsent := false
	for i, item := range result.Items {
		d, ok := item["delete"]
		if !ok || len(item) != 1 || d.Index != documents[i].Index || d.ID != documents[i].ID {
			return errors.New("frozen delete item identity or outcome is invalid")
		}
		if d.Shards != nil && (d.Shards.Failed == nil || *d.Shards.Failed != 0) {
			return errors.New("frozen delete shard outcome is invalid")
		}
		if d.Status == http.StatusConflict {
			absent, err := c.frozenDocumentAbsent(ctx, documents[i])
			if err != nil {
				return err
			}
			if absent {
				confirmedAbsent = true
				continue
			}
			return errors.New("frozen delete document version changed")
		}
		if len(d.Error) > 0 && string(d.Error) != "null" || d.Shards == nil || d.Shards.Failed == nil || *d.Shards.Failed != 0 {
			return errors.New("frozen delete item failed")
		}
		validOutcome := d.Status == http.StatusOK && d.Result == "deleted" || d.Status == http.StatusNotFound && d.Result == "not_found"
		if !validOutcome {
			return fmt.Errorf("frozen delete item rejected with status %d", d.Status)
		}
	}
	if *result.Errors && !confirmedAbsent {
		return errors.New("frozen delete response reports unexplained failure")
	}
	return nil
}

// A CAS delete may report conflict for a document already removed by a prior
// attempt. Confirm absence with a realtime, routed GET, never error-message text.
func (c *Client) frozenDocumentAbsent(ctx context.Context, d FrozenDocument) (bool, error) {
	query := url.Values{"realtime": {"true"}, "_source": {"false"}}
	if d.Routing != "" {
		query.Set("routing", d.Routing)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+url.PathEscape(d.Index)+"/_doc/"+url.PathEscape(d.ID)+"?"+query.Encode(), nil)
	if err != nil {
		return false, err
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("frozen delete confirmation HTTP status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, frozenDeleteResponseBytes+1))
	if err != nil {
		return false, err
	}
	if len(raw) > frozenDeleteResponseBytes {
		return false, errors.New("frozen delete confirmation exceeds budget")
	}
	var result struct {
		Index string          `json:"_index"`
		ID    string          `json:"_id"`
		Found *bool           `json:"found"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Index != d.Index || result.ID != d.ID || result.Found == nil || len(result.Error) > 0 && string(result.Error) != "null" {
		return false, errors.New("invalid frozen delete confirmation")
	}
	return response.StatusCode == http.StatusNotFound && !*result.Found, nil
}
