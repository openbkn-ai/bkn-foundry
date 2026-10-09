// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"testing"
	"time"
)

func TestObservabilityConfigReadsCursorSigningKeyWithoutTransformingIt(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY", "local-test-signing-key")
	config := NewObservabilityConfig()
	if string(config.CursorSigningKey) != "local-test-signing-key" {
		t.Fatalf("unexpected cursor signing key")
	}
}

func TestObservabilityConfigUsesBoundedSourceQueryDefaults(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_SOURCE_TIMEOUT", "")
	t.Setenv("BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES", "")
	config := NewObservabilityConfig()
	if config.SourceTimeout != 3*time.Second {
		t.Fatalf("unexpected source timeout: %s", config.SourceTimeout)
	}
	if config.MaxConcurrentSources != 4 {
		t.Fatalf("unexpected source concurrency: %d", config.MaxConcurrentSources)
	}
}

func TestObservabilityConfigReadsSourceQueryLimits(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_SOURCE_TIMEOUT", "750ms")
	t.Setenv("BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES", "2")
	config := NewObservabilityConfig()
	if config.SourceTimeout != 750*time.Millisecond {
		t.Fatalf("unexpected source timeout: %s", config.SourceTimeout)
	}
	if config.MaxConcurrentSources != 2 {
		t.Fatalf("unexpected source concurrency: %d", config.MaxConcurrentSources)
	}
}

func TestObservabilityConfigRejectsInvalidSourceQueryLimits(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_SOURCE_TIMEOUT", "forever")
	t.Setenv("BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES", "0")
	config := NewObservabilityConfig()
	if config.SourceTimeout != 3*time.Second || config.MaxConcurrentSources != 4 {
		t.Fatalf("invalid values must fall back to defaults: %+v", config)
	}
}

func TestObservabilityConfigReadsSourceCoverageMonitor(t *testing.T) {
	t.Setenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_METRICS_ENDPOINT", "http://otelcol:8888/metrics")
	t.Setenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_SOURCE_ID", "otel-runtime")
	t.Setenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_DEPLOYMENT_ID", "observability/otelcol-contrib")
	t.Setenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_INTERVAL", "15s")

	config := NewObservabilityConfig()
	if config.SourceCoverageMetricsEndpoint != "http://otelcol:8888/metrics" ||
		config.SourceCoverageSourceID != "otel-runtime" ||
		config.SourceCoverageDeploymentID != "observability/otelcol-contrib" ||
		config.SourceCoverageInterval != 15*time.Second {
		t.Fatalf("unexpected source coverage monitor config: %+v", config)
	}
}

func TestObservabilityConfigReadsAdmissionBudgetProfileAndThresholds(t *testing.T) {
	t.Setenv("BKN_TRACE_ADMISSION_BUDGET_PROFILE", "production")
	t.Setenv("BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD", "0.8")
	t.Setenv("BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD", "0.75")
	t.Setenv("BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD", "0.7")
	t.Setenv("BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD", "0.65")

	config := NewObservabilityConfig()
	if config.AdmissionBudgetProfile != "production" || config.AdmissionBudgetThresholds.OpenSearchCapacity != 0.8 || config.AdmissionBudgetThresholds.OpenSearchHeap != 0.75 || config.AdmissionBudgetThresholds.CollectorQueue != 0.7 || config.AdmissionBudgetThresholds.StoragePool != 0.65 {
		t.Fatalf("unexpected admission budget config: %+v", config)
	}
}

func TestObservabilityConfigLeavesMissingAdmissionThresholdUnavailable(t *testing.T) {
	for _, name := range []string{
		"BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD",
		"BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD",
		"BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD",
		"BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD",
	} {
		t.Setenv(name, "")
	}
	config := NewObservabilityConfig()
	if config.AdmissionBudgetThresholds.OpenSearchCapacity != 0 || config.AdmissionBudgetThresholds.OpenSearchHeap != 0 || config.AdmissionBudgetThresholds.CollectorQueue != 0 || config.AdmissionBudgetThresholds.StoragePool != 0 {
		t.Fatalf("missing thresholds must remain unavailable: %+v", config.AdmissionBudgetThresholds)
	}
}

func TestAdmissionCollectorEndpointSelection(t *testing.T) {
	for _, tc := range []struct {
		name, dedicated, fallback, want string
		invalid                         bool
	}{
		{name: "dedicated", dedicated: " https://collector.example/custom-metrics ", fallback: "http://legacy/metrics", want: "https://collector.example/custom-metrics"},
		{name: "fallback", fallback: "http://legacy/metrics", want: "http://legacy/metrics"},
		{name: "missing", invalid: true},
		{name: "missing host", dedicated: "http:///metrics", invalid: true},
		{name: "metrics at root", dedicated: "https://collector.example/", want: "https://collector.example/"},
		{name: "unsupported scheme", dedicated: "file:///metrics", invalid: true},
		{name: "invalid dedicated does not fall back", dedicated: ":bad", fallback: "http://legacy/metrics", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ObservabilityConfig{AdmissionBudgetMetricsEndpoint: tc.dedicated, SourceCoverageMetricsEndpoint: tc.fallback}
			got, err := config.AdmissionCollectorMetricsEndpoint()
			if (err != nil) != tc.invalid {
				t.Fatalf("endpoint=%q error=%v", got, err)
			}
			if !tc.invalid && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdmissionThresholdRejectsNonFiniteValues(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf", "0", "1.01", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD", value)
			if got := NewObservabilityConfig().AdmissionBudgetThresholds.CollectorQueue; got != 0 {
				t.Fatalf("invalid threshold retained: %v", got)
			}
		})
	}
}
