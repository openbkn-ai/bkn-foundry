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
