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

func (c *Client) IndexDocument(ctx context.Context, index string, documentID string, body []byte) ([]byte, error) {
	requestURL := fmt.Sprintf("%s/%s/_doc/%s", c.baseURL, strings.TrimLeft(index, "/"), strings.TrimLeft(documentID, "/"))
	return c.putDocument(ctx, requestURL, body)
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
		return nil, fmt.Errorf("opensearch index failed with status %d: %s", resp.StatusCode, string(respBody))
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
