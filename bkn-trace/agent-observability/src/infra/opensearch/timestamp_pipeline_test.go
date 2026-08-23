// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnsureTraceTimestampPipelineRepairsOnlyZeroSpanTimestamps(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/_ingest/pipeline/bkn-trace-span-timestamp-v1":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			content := string(body)
			if !strings.Contains(content, "startTime") || !strings.Contains(content, "0001-01-01T00:00:00Z") {
				t.Fatalf("pipeline must repair only zero trace timestamps: %s", content)
			}
			if !strings.Contains(content, "ctx.containsKey('traceId')") {
				t.Fatalf("pipeline must not alter log records: %s", content)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/_index_template/bkn-trace-timestamp-default":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			content := string(body)
			if !strings.Contains(content, `"index_patterns":["ss4o_traces-default-namespace"]`) {
				t.Fatalf("template must target the trace index: %s", content)
			}
			if !strings.Contains(content, `"index.default_pipeline":"bkn-trace-span-timestamp-v1"`) {
				t.Fatalf("template must configure the timestamp repair pipeline: %s", content)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/ss4o_traces-default-namespace/_settings":
			w.WriteHeader(http.StatusNotFound)
			return
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, AuthConfig{}, time.Second)
	if err := client.EnsureTraceTimestampPipeline(t.Context(), "bkn-trace-span-timestamp-v1", "ss4o_traces-default-namespace"); err != nil {
		t.Fatalf("ensure timestamp pipeline: %v", err)
	}
	if requests != 3 {
		t.Fatalf("expected pipeline, template and index settings requests, got %d", requests)
	}
}

func TestEnsureTraceTimestampPipelineUpdatesExistingTraceIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ingest/pipeline/bkn-trace-span-timestamp-v1", "/_index_template/bkn-trace-timestamp-default":
			w.WriteHeader(http.StatusOK)
		case "/ss4o_traces-default-namespace/_settings":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"index.default_pipeline":"bkn-trace-span-timestamp-v1"`) {
				t.Fatalf("existing trace index must use the timestamp repair pipeline: %s", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(server.URL, AuthConfig{}, time.Second)
	if err := client.EnsureTraceTimestampPipeline(t.Context(), "bkn-trace-span-timestamp-v1", "ss4o_traces-default-namespace"); err != nil {
		t.Fatalf("ensure timestamp pipeline: %v", err)
	}
}
