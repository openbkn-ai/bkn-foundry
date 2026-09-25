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

type admissionMetricsRoundTripper func(*http.Request) (*http.Response, error)

func (f admissionMetricsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestEnsureIndexDoesNotCreateAnExistingIndex(t *testing.T) {
	createCalls := 0
	mappingCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/existing-index":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/existing-index/_mapping":
			mappingCalls++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/existing-index":
			createCalls++
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"type":"index_create_block_exception"}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(server.URL, AuthConfig{}, time.Second)
	err := client.EnsureIndex(t.Context(), "existing-index", []byte(`{"mappings":{"properties":{"id":{"type":"keyword"}}}}`))
	if err != nil {
		t.Fatalf("ensure existing index: %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("existing index received %d create requests", createCalls)
	}
	if mappingCalls != 1 {
		t.Fatalf("existing index received %d mapping requests", mappingCalls)
	}
}

func TestReadAdmissionMetricsAggregatesNodeStats(t *testing.T) {
	client := NewWithHTTPClient("http://opensearch.test", AuthConfig{}, &http.Client{Transport: admissionMetricsRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/_nodes/stats/jvm,fs" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"nodes":{"one":{"fs":{"total":{"total_in_bytes":1000,"available_in_bytes":200}},"jvm":{"mem":{"heap_used_in_bytes":300,"heap_max_in_bytes":500}}},"two":{"fs":{"total":{"total_in_bytes":1000,"available_in_bytes":500}},"jvm":{"mem":{"heap_used_in_bytes":100,"heap_max_in_bytes":500}}}}}`)), Header: make(http.Header)}, nil
	})})
	metrics, err := client.ReadAdmissionMetrics(t.Context())
	if err != nil {
		t.Fatalf("read admission metrics: %v", err)
	}
	if metrics.Capacity != 0.65 {
		t.Fatalf("capacity = %v, want 0.65", metrics.Capacity)
	}
	if metrics.Heap != 0.4 {
		t.Fatalf("heap = %v, want 0.4", metrics.Heap)
	}
	if metrics.SampledAt.IsZero() {
		t.Fatal("sample timestamp is required")
	}
}

func TestReadAdmissionMetricsFailsWhenTotalsAreMissing(t *testing.T) {
	client := NewWithHTTPClient("http://opensearch.test", AuthConfig{}, &http.Client{Transport: admissionMetricsRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"nodes":{}}`)), Header: make(http.Header)}, nil
	})})
	if _, err := client.ReadAdmissionMetrics(t.Context()); err == nil {
		t.Fatal("missing node totals must fail closed")
	}
}
