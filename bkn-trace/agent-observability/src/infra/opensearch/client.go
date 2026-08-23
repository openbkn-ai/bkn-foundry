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
	"strconv"
	"strings"
	"time"
)

var (
	ErrDocumentNotFound = errors.New("opensearch document not found")
	ErrVersionConflict  = errors.New("opensearch version conflict")
)

type Document struct {
	Source      []byte
	SeqNo       int64
	PrimaryTerm int64
}

type MultiGetDocument struct {
	ID     string
	Found  bool
	Source []byte
}

type StatusError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf(
		"opensearch %s failed with status %d: %s",
		e.Operation, e.StatusCode, e.Body,
	)
}

func IsPermanentStatusError(err error) bool {
	var statusError *StatusError
	if !errors.As(err, &statusError) {
		return false
	}
	switch statusError.StatusCode {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly,
		http.StatusTooManyRequests:
		return false
	default:
		return statusError.StatusCode >= http.StatusBadRequest &&
			statusError.StatusCode < http.StatusInternalServerError
	}
}

type Client struct {
	baseURL    string
	auth       AuthConfig
	httpClient *http.Client
}

type AuthConfig struct {
	Enabled  bool
	Username string
	Password string
}

func New(baseURL string, auth AuthConfig, timeout time.Duration) *Client {
	return NewWithHTTPClient(baseURL, auth, &http.Client{
		Timeout: timeout,
	})
}

func NewWithHTTPClient(baseURL string, auth AuthConfig, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		auth:       auth,
		httpClient: httpClient,
	}
}

// EnsureTraceTimestampPipeline installs the OpenSearch-side compatibility
// repair for the Collector's SS4O trace encoder. The affected upstream encoder
// serializes @timestamp as Go's zero time while retaining span startTime. The
// condition deliberately requires a traceId, so logs never enter this path.
func (c *Client) EnsureTraceTimestampPipeline(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"description": "Repair zero @timestamp on OpenTelemetry SS4O trace spans from startTime.",
		"processors": []map[string]any{{
			"script": map[string]any{
				"lang":   "painless",
				"if":     "ctx.containsKey('traceId') && ctx.containsKey('startTime') && ctx.startTime != null && (!ctx.containsKey('@timestamp') || ctx['@timestamp'] == null || ctx['@timestamp'] == '0001-01-01T00:00:00Z')",
				"source": "ctx['@timestamp'] = ctx.startTime",
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("encode trace timestamp pipeline: %w", err)
	}
	requestURL := fmt.Sprintf("%s/_ingest/pipeline/%s", c.baseURL, url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create trace timestamp pipeline request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute trace timestamp pipeline request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read trace timestamp pipeline response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("opensearch trace timestamp pipeline failed with status %d: %s", resp.StatusCode, string(responseBody))
	}
	return nil
}

func (c *Client) Search(ctx context.Context, index string, query []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/_search", c.baseURL, strings.TrimLeft(index, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("create opensearch search request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute opensearch search request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read opensearch search response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("opensearch search failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// DeleteByQuery is used only after an archive bundle has been verified. The
// caller provides a frozen identity filter; it must never be a moving cutoff.
func (c *Client) DeleteByQuery(ctx context.Context, index string, query []byte) error {
	requestURL := fmt.Sprintf("%s/%s/_delete_by_query?conflicts=proceed", c.baseURL, strings.TrimLeft(index, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(query))
	if err != nil {
		return fmt.Errorf("create opensearch delete-by-query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute opensearch delete-by-query request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read opensearch delete-by-query response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("opensearch delete-by-query failed with status %d: %s", response.StatusCode, string(body))
	}
	return nil
}

func (c *Client) IndexDocument(ctx context.Context, index string, documentID string, body []byte) ([]byte, error) {
	requestURL := fmt.Sprintf("%s/%s/_doc/%s", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(strings.TrimLeft(documentID, "/")))
	return c.putDocument(ctx, requestURL, body)
}

func (c *Client) IndexDocumentVersion(
	ctx context.Context,
	index string,
	documentID string,
	version uint64,
	body []byte,
) ([]byte, error) {
	requestURL := fmt.Sprintf(
		"%s/%s/_doc/%s?version=%d&version_type=external",
		c.baseURL,
		strings.TrimLeft(index, "/"),
		url.PathEscape(strings.TrimLeft(documentID, "/")),
		version,
	)
	return c.putDocument(ctx, requestURL, body)
}

func (c *Client) MultiGetDocuments(
	ctx context.Context,
	index string,
	documentIDs []string,
) ([]MultiGetDocument, error) {
	body, err := json.Marshal(map[string]any{"ids": documentIDs})
	if err != nil {
		return nil, fmt.Errorf("encode opensearch multi-get request: %w", err)
	}
	requestURL := fmt.Sprintf(
		"%s/%s/_mget", c.baseURL, strings.TrimLeft(index, "/"),
	)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, requestURL, bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create opensearch multi-get request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute opensearch multi-get request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read opensearch multi-get response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"opensearch multi-get failed with status %d: %s",
			resp.StatusCode, string(responseBody),
		)
	}
	var response struct {
		Documents []struct {
			ID     string          `json:"_id"`
			Found  bool            `json:"found"`
			Source json.RawMessage `json:"_source"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode opensearch multi-get response: %w", err)
	}
	documents := make([]MultiGetDocument, 0, len(response.Documents))
	for _, document := range response.Documents {
		documents = append(documents, MultiGetDocument{
			ID: document.ID, Found: document.Found, Source: document.Source,
		})
	}
	return documents, nil
}

func (c *Client) CreateDocument(ctx context.Context, index string, documentID string, body []byte) ([]byte, error) {
	requestURL := fmt.Sprintf("%s/%s/_doc/%s?op_type=create", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(strings.TrimLeft(documentID, "/")))
	return c.putDocument(ctx, requestURL, body)
}

func (c *Client) UpdateDocument(ctx context.Context, index string, documentID string, body []byte, seqNo, primaryTerm int64) ([]byte, error) {
	requestURL := fmt.Sprintf("%s/%s/_doc/%s?if_seq_no=%s&if_primary_term=%s", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(strings.TrimLeft(documentID, "/")), strconv.FormatInt(seqNo, 10), strconv.FormatInt(primaryTerm, 10))
	return c.putDocument(ctx, requestURL, body)
}

func (c *Client) GetDocument(ctx context.Context, index string, documentID string) (Document, error) {
	requestURL := fmt.Sprintf("%s/%s/_doc/%s", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(strings.TrimLeft(documentID, "/")))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Document{}, fmt.Errorf("create opensearch get request: %w", err)
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Document{}, fmt.Errorf("execute opensearch get request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Document{}, fmt.Errorf("read opensearch get response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return Document{}, ErrDocumentNotFound
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Document{}, fmt.Errorf("opensearch get failed with status %d: %s", resp.StatusCode, string(body))
	}
	var response struct {
		Source      json.RawMessage `json:"_source"`
		SeqNo       int64           `json:"_seq_no"`
		PrimaryTerm int64           `json:"_primary_term"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Document{}, fmt.Errorf("decode opensearch get response: %w", err)
	}
	return Document{Source: response.Source, SeqNo: response.SeqNo, PrimaryTerm: response.PrimaryTerm}, nil
}

func (c *Client) putDocument(ctx context.Context, requestURL string, body []byte) ([]byte, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create opensearch index request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute opensearch index request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read opensearch index response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		return nil, ErrVersionConflict
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &StatusError{
			Operation: "index", StatusCode: resp.StatusCode, Body: string(respBody),
		}
	}

	return respBody, nil
}

func (c *Client) EnsureIndex(ctx context.Context, index string, mapping []byte) error {
	url := fmt.Sprintf("%s/%s", c.baseURL, strings.TrimLeft(index, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(mapping))
	if err != nil {
		return fmt.Errorf("create opensearch create-index request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute opensearch create-index request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read opensearch create-index response: %w", err)
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "resource_already_exists_exception") {
		return c.ensureMapping(ctx, index, mapping)
	}

	return fmt.Errorf("opensearch create-index failed with status %d: %s", resp.StatusCode, string(body))
}

// EnsureMapping additively applies the mapping portion of an index definition
// to an existing index or alias. OpenSearch permits adding fields without
// rewriting existing documents.
func (c *Client) EnsureMapping(ctx context.Context, index string, indexDefinition []byte) error {
	return c.ensureMapping(ctx, index, indexDefinition)
}

func (c *Client) AliasExists(ctx context.Context, alias string) (bool, error) {
	return c.resourceExists(ctx, http.MethodGet, "_alias/"+strings.TrimLeft(alias, "/"))
}

func (c *Client) IndexExists(ctx context.Context, index string) (bool, error) {
	return c.resourceExists(ctx, http.MethodHead, strings.TrimLeft(index, "/"))
}

func (c *Client) resourceExists(ctx context.Context, method, resource string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/"+resource, nil)
	if err != nil {
		return false, fmt.Errorf("create opensearch existence request: %w", err)
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("execute opensearch existence request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read opensearch existence response: %w", err)
	}
	return false, fmt.Errorf("opensearch existence check failed with status %d: %s", resp.StatusCode, string(body))
}

func (c *Client) CountDocuments(ctx context.Context, index string) (uint64, error) {
	body, err := c.Search(ctx, index, []byte(`{"size":0,"track_total_hits":true}`))
	if err != nil {
		return 0, err
	}
	var response struct {
		Hits struct {
			Total struct {
				Value uint64 `json:"value"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode opensearch count response: %w", err)
	}
	return response.Hits.Total.Value, nil
}

func (c *Client) RefreshIndex(ctx context.Context, index string) error {
	requestURL := fmt.Sprintf(
		"%s/%s/_refresh", c.baseURL, strings.TrimLeft(index, "/"),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create opensearch refresh request: %w", err)
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute opensearch refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read opensearch refresh response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"opensearch refresh failed with status %d: %s",
			resp.StatusCode, string(body),
		)
	}
	return nil
}

func (c *Client) SwitchAlias(ctx context.Context, alias, index string) error {
	body, err := json.Marshal(map[string]any{
		"actions": []map[string]any{
			{"remove": map[string]any{"index": "*", "alias": alias, "must_exist": false}},
			{"add": map[string]any{"index": index, "alias": alias}},
		},
	})
	if err != nil {
		return err
	}
	requestURL := c.baseURL + "/_aliases"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create opensearch alias request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute opensearch alias request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read opensearch alias response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("opensearch alias switch failed with status %d: %s", resp.StatusCode, string(responseBody))
	}
	return nil
}

func (c *Client) ensureMapping(ctx context.Context, index string, indexDefinition []byte) error {
	var definition struct {
		Mappings json.RawMessage `json:"mappings"`
	}
	if err := json.Unmarshal(indexDefinition, &definition); err != nil {
		return fmt.Errorf("decode opensearch index mapping: %w", err)
	}
	if len(definition.Mappings) == 0 {
		return fmt.Errorf("opensearch index definition is missing mappings")
	}

	requestURL := fmt.Sprintf("%s/%s/_mapping", c.baseURL, strings.TrimLeft(index, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(definition.Mappings))
	if err != nil {
		return fmt.Errorf("create opensearch update-mapping request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute opensearch update-mapping request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read opensearch update-mapping response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("opensearch update-mapping failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
