// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ossgatewayarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
)

type Config struct {
	BaseURL, StorageID, Prefix string
	Timeout                    time.Duration
}
type Client struct {
	config            Config
	http              *http.Client
	stream            *http.Client
	mu                sync.Mutex
	resolvedStorageID string
}

func New(config Config) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Second
	}
	if config.Prefix == "" {
		config.Prefix = "observability-archive"
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://oss-gateway-backend:8080/api/v1"
	}
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.ResponseHeaderTimeout = config.Timeout
	return &Client{
		config: config,
		http:   &http.Client{Timeout: config.Timeout},
		// Archive bodies can legitimately take longer than the bounded
		// control-plane calls. Response headers stay bounded while the request
		// context remains the cancellation boundary once streaming starts.
		stream: &http.Client{Transport: streamTransport},
	}
}
func (client *Client) Ready() bool {
	return strings.TrimSpace(client.config.BaseURL) != ""
}

// Availability resolves the deployment's enabled default storage when a
// dedicated archive storage ID was not supplied.  This reuses Foundry's
// existing gateway configuration rather than creating a second storage path.
func (client *Client) Availability(ctx context.Context) (bool, error) {
	_, err := client.storageID(ctx)
	return err == nil, err
}

type gatewayResponse struct {
	Data struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"data"`
}

func (client *Client) WriteAndVerify(ctx context.Context, job archivesvc.Job, candidates []archivesvc.Candidate) (string, error) {
	if _, err := client.storageID(ctx); err != nil {
		return "", err
	}
	lines := make([][]byte, 0, len(candidates))
	for _, candidate := range candidates {
		lines = append(lines, append(append([]byte(nil), candidate.Payload...), '\n'))
	}
	content := bytes.Join(lines, nil)
	dataKey := fmt.Sprintf("%s/%s/%s.jsonl", client.config.Prefix, job.TenantID, job.ID)
	if err := client.upload(ctx, dataKey, content); err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	manifest := map[string]any{"archive_job_id": job.ID, "archive_kind": job.Kind, "candidate_count": len(candidates), "data_key": dataKey, "sha256": hex.EncodeToString(digest[:])}
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifestKey := fmt.Sprintf("%s/%s/%s/manifest.json", client.config.Prefix, job.TenantID, job.ID)
	if err := client.upload(ctx, manifestKey, manifestBody); err != nil {
		return "", err
	}
	verified, err := client.download(ctx, dataKey)
	if err != nil {
		return "", err
	}
	actual := sha256.Sum256(verified)
	if actual != digest {
		return "", fmt.Errorf("archive bundle checksum mismatch")
	}
	return manifestKey, nil
}

func (client *Client) upload(ctx context.Context, key string, content []byte) error {
	method, signedURL, headers, err := client.authorize(ctx, "upload", key, http.MethodPut)
	if err != nil {
		return err
	}
	return client.signed(ctx, method, signedURL, headers, content)
}
func (client *Client) download(ctx context.Context, key string) ([]byte, error) {
	content, err := client.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = content.Close() }()
	return io.ReadAll(content)
}
func (client *Client) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	method, signedURL, headers, err := client.authorize(ctx, "download", key, http.MethodGet)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, signedURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := client.stream.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("read archive bundle: %s", response.Status)
	}
	return response.Body, nil
}
func (client *Client) authorize(ctx context.Context, operation, key, method string) (string, string, map[string]string, error) {
	storageID, err := client.storageID(ctx)
	if err != nil {
		return "", "", nil, err
	}
	endpoint := fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.config.BaseURL, "/"), operation, url.PathEscape(storageID), url.PathEscape(key))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", nil, err
	}
	query := request.URL.Query()
	query.Set("request_method", method)
	query.Set("expires", "3600")
	query.Set("internal_request", "true")
	request.URL.RawQuery = query.Encode()
	response, err := client.http.Do(request)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", "", nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", nil, fmt.Errorf("archive storage authorization: %s", response.Status)
	}
	var gateway gatewayResponse
	if err := json.Unmarshal(body, &gateway); err != nil {
		return "", "", nil, err
	}
	if gateway.Data.URL == "" || gateway.Data.Method == "" {
		return "", "", nil, fmt.Errorf("archive storage returned no signed request")
	}
	return gateway.Data.Method, gateway.Data.URL, gateway.Data.Headers, nil
}

func (client *Client) storageID(ctx context.Context) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.resolvedStorageID != "" {
		return client.resolvedStorageID, nil
	}
	if configured := strings.TrimSpace(client.config.StorageID); configured != "" {
		client.resolvedStorageID = configured
		return configured, nil
	}
	endpoint := strings.TrimRight(client.config.BaseURL, "/") + "/storages?enabled=true&is_default=true"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("query default archive storage: %s", response.Status)
	}
	var body struct {
		Data []struct {
			StorageID string `json:"storage_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Data) == 0 || strings.TrimSpace(body.Data[0].StorageID) == "" {
		return "", fmt.Errorf("no enabled default object storage is configured")
	}
	client.resolvedStorageID = strings.TrimSpace(body.Data[0].StorageID)
	return client.resolvedStorageID, nil
}
func (client *Client) signed(ctx context.Context, method, signedURL string, headers map[string]string, content []byte) error {
	request, err := http.NewRequestWithContext(ctx, method, signedURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("write archive bundle: %s", response.Status)
	}
	return nil
}
