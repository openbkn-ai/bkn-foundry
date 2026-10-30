// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiptSnapshotPreservesRejectedStatusAndSanitizedType(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"circuit_breaking_exception","reason":"secret-query-and-document"},"status":429}`,
		`{"error":{"type":"unsafe secret type","reason":"secret-query-and-document"}}`,
		`not-json`,
		strings.Repeat("x", maxPointInTimeResponseBytes+1),
	} {
		client := NewWithHTTPClient("http://local", AuthConfig{}, &http.Client{Transport: spanBoundedTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		_, err := client.OpenPointInTime(context.Background(), "core")
		var status *StatusError
		if !errors.As(err, &status) || status.StatusCode != 429 || status.Body != "" || strings.Contains(err.Error(), "secret") {
			t.Fatalf("lost status or leaked response body: %v", err)
		}
		if strings.Contains(body, `"type":"circuit_breaking_exception"`) && status.Type != "circuit_breaking_exception" {
			t.Fatalf("lost exception type: %+v", status)
		}
	}
}

func TestReceiptSnapshotLifecycleAndPartialResponseRejection(t *testing.T) {
	partial := false
	closed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			var body struct {
				IDs []string `json:"pit_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.IDs) != 1 || body.IDs[0] != "snapshot" {
				t.Error("must close only the owned snapshot")
			}
			closed = true
			_, _ = w.Write([]byte(`{"pits":[{"successful":true}]}`))
			return
		}
		if r.URL.Path == "/core/_search/point_in_time" {
			if r.URL.Query().Get("allow_partial_pit_creation") != "false" {
				t.Error("partial creation must be disabled")
			}
			_, _ = w.Write([]byte(`{"pit_id":"snapshot","_shards":{"failed":0}}`))
			return
		}
		if r.URL.Path != "/_search" || r.URL.Query().Get("allow_partial_search_results") != "false" {
			t.Error("incorrect root PIT search endpoint")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, bad := body["allow_partial_search_results"]; bad {
			t.Error("partial result option is a URL parameter, not search DSL")
		}
		if _, ok := body["pit"]; !ok {
			t.Error("snapshot missing")
		}
		if partial {
			_, _ = w.Write([]byte(`{"timed_out":false,"_shards":{"failed":1},"hits":{"hits":[]}}`))
		} else {
			_, _ = w.Write([]byte(`{"timed_out":false,"_shards":{"failed":0},"hits":{"hits":[]}}`))
		}
	}))
	defer server.Close()
	client := NewWithHTTPClient(server.URL, AuthConfig{}, server.Client())
	id, err := client.OpenPointInTime(context.Background(), "core")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.SearchPointInTime(context.Background(), id, []byte(`{"size":256}`)); err != nil {
		t.Fatal(err)
	}
	partial = true
	if _, err = client.SearchPointInTime(context.Background(), id, []byte(`{"size":256}`)); err == nil {
		t.Fatal("partial search accepted as complete")
	}
	if err = client.ClosePointInTime(context.Background(), id); err != nil || !closed {
		t.Fatal("owned snapshot not released", err)
	}
}

func TestReceiptSnapshotCloseRequiresAcknowledgment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"pits":[{"successful":false}]}`))
	}))
	defer server.Close()
	client := NewWithHTTPClient(server.URL, AuthConfig{}, server.Client())
	if err := client.ClosePointInTime(context.Background(), "snapshot"); err == nil {
		t.Fatal("failed close treated as success")
	}
}
