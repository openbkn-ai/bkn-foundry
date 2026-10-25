// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditpublisherstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditproducersvc"
)

const maxResponseBytes = 64 << 10

var ErrStatusRead = errors.New("audit publisher status read failed")

type Reader struct {
	endpoint string
	client   *http.Client
}

func New(endpoint string) (*Reader, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Path != "/internal/audit/publish-status" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid audit publisher status endpoint")
	}
	return &Reader{endpoint: u.String(), client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (r *Reader) Read(ctx context.Context) (auditproducersvc.Snapshot, error) {
	var empty auditproducersvc.Snapshot
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return empty, ErrStatusRead
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return empty, ErrStatusRead
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return empty, ErrStatusRead
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return empty, ErrStatusRead
	}
	// A missing absolute count cannot be interpreted as an observed zero.
	var wire struct {
		auditproducersvc.Snapshot
		DroppedRecords *uint64 `json:"dropped_records"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil || wire.DroppedRecords == nil {
		return empty, ErrStatusRead
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return empty, ErrStatusRead
	}
	wire.Snapshot.DroppedRecords = *wire.DroppedRecords
	return wire.Snapshot, nil
}

var _ auditproducersvc.Reader = (*Reader)(nil)
