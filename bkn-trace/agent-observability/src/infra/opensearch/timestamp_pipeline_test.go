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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/_ingest/pipeline/bkn-trace-span-timestamp-v1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
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
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, AuthConfig{}, time.Second)
	if err := client.EnsureTraceTimestampPipeline(t.Context(), "bkn-trace-span-timestamp-v1"); err != nil {
		t.Fatalf("ensure timestamp pipeline: %v", err)
	}
}
