// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditpublisherstatus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const validBody = `{"schema_version":"1.0","instance_id":"safe-a","observed_at":"2026-10-26T00:00:00Z","status":"healthy","reason":"delivery_observed","dropped_records":0,"last_delivered_at":"2026-10-26T00:00:00Z","last_failure_at":null}`

func TestReadBoundedJSONAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"valid", validBody, 200, true},
		{"missing_count", strings.Replace(validBody, `"dropped_records":0,`, "", 1), 200, false},
		{"null_count", strings.Replace(validBody, `"dropped_records":0`, `"dropped_records":null`, 1), 200, false},
		{"negative_count", strings.Replace(validBody, `"dropped_records":0`, `"dropped_records":-1`, 1), 200, false},
		{"overflow_count", strings.Replace(validBody, `"dropped_records":0`, `"dropped_records":18446744073709551616`, 1), 200, false},
		{"trailing_JSON", validBody + `{}`, 200, false},
		{"invalid_JSON", `{"secret":"private raw response"`, 200, false},
		{"oversized", validBody + strings.Repeat(" ", maxResponseBytes), 200, false},
		{"redirect", "private redirect body", 302, false},
		{"HTTP_error", "private backend response", 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New("http://safe.internal/internal/audit/publish-status")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			r.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("Accept") != "application/json" {
					t.Fatal("unexpected status request")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"http://other.invalid/private"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			got, err := r.Read(context.Background())
			if (err == nil) != tc.valid {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if err != nil && err != ErrStatusRead {
				t.Fatal("raw error escaped")
			}
			if calls != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestReaderEndpointTimeoutAndCancellation(t *testing.T) {
	for _, url := range []string{"", "file:///internal/audit/publish-status", "http://user:secret@safe/internal/audit/publish-status", "http://safe/other", "http://safe/internal/audit/publish-status?secret=x", "http://safe/internal/audit/publish-status#fragment"} {
		if _, err := New(url); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("endpoint accepted or leaked: %v", err)
		}
	}
	r, err := New("https://safe.internal/internal/audit/publish-status")
	if err != nil {
		t.Fatal(err)
	}
	if r.client.Timeout != 3*time.Second || r.client.CheckRedirect == nil {
		t.Fatal("missing bounded transport options")
	}
	r.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, errors.New("secret upstream error")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx); err != ErrStatusRead {
		t.Fatalf("unsafe cancellation error: %v", err)
	}
}
