// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package otelcolmetrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type metricsRoundTripper func(*http.Request) (*http.Response, error)

func (f metricsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSumsCollectorLogCoverageMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`# TYPE otelcol_receiver_refused_log_records_total counter
otelcol_receiver_refused_log_records_total{receiver="otlp"} 2
otelcol_receiver_refused_log_records_total{receiver="http"} 1
otelcol_exporter_send_failed_log_records_total{exporter="opensearch"} 4
otelcol_exporter_queue_size{exporter="opensearch"} 6
otelcol_exporter_queue_capacity{exporter="opensearch"} 512
`))
	}))
	defer server.Close()

	snapshot, err := New(server.URL, server.Client()).Read(context.Background())
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if snapshot.RefusedLogs != 3 || snapshot.FailedLogs != 4 || snapshot.QueueSize != 6 || snapshot.QueueCapacity != 512 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestClientReadsQueueUtilization(t *testing.T) {
	client := New("http://otelcol.test/metrics", &http.Client{Transport: metricsRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("otelcol_exporter_queue_size{exporter=\"opensearch\"} 64\notelcol_exporter_queue_capacity{exporter=\"opensearch\"} 128\n")), Header: make(http.Header)}, nil
	})})
	sample, err := client.ReadQueueSample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sample.Utilization != 0.5 || sample.SampledAt.IsZero() {
		t.Fatalf("unexpected queue sample: %+v", sample)
	}
}

func TestClientRejectsMissingQueueCapacity(t *testing.T) {
	client := New("http://otelcol.test/metrics", &http.Client{Transport: metricsRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("otelcol_exporter_queue_size 0\n")), Header: make(http.Header)}, nil
	})})
	if _, err := client.ReadQueueSample(context.Background()); err == nil {
		t.Fatal("missing queue capacity must fail closed")
	}
}
