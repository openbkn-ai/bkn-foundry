// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type ObservabilityConfig struct {
	CursorSigningKey               []byte
	SourceTimeout                  time.Duration
	MaxConcurrentSources           int
	SourceCoverageMetricsEndpoint  string
	SourceCoverageSourceID         string
	SourceCoverageDeploymentID     string
	SourceCoverageInterval         time.Duration
	ArchiveObjectStoreURL          string
	ArchiveObjectStorageID         string
	ArchiveObjectPrefix            string
	AdmissionBudgetProfile         string
	AdmissionBudgetMetricsEndpoint string
	AdmissionBudgetThresholds      AdmissionBudgetThresholdsConfig
}

type AdmissionBudgetThresholdsConfig struct {
	OpenSearchCapacity float64
	OpenSearchHeap     float64
	CollectorQueue     float64
	StoragePool        float64
}

func NewObservabilityConfig() ObservabilityConfig {
	sourceTimeout := 3 * time.Second
	if value := strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_TIMEOUT")); value != "" {
		configured, err := time.ParseDuration(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid observability source timeout; using default", "value", value, "default", sourceTimeout)
		} else {
			sourceTimeout = configured
		}
	}
	maxConcurrentSources := 4
	if value := strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES")); value != "" {
		configured, err := strconv.Atoi(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid observability source concurrency; using default", "value", value, "default", maxConcurrentSources)
		} else {
			maxConcurrentSources = configured
		}
	}
	coverageInterval := 30 * time.Second
	if value := strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_INTERVAL")); value != "" {
		configured, err := time.ParseDuration(value)
		if err != nil || configured <= 0 {
			slog.Warn("invalid source coverage interval; using default", "value", value, "default", coverageInterval)
		} else {
			coverageInterval = configured
		}
	}
	return ObservabilityConfig{
		CursorSigningKey:               []byte(os.Getenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY")),
		SourceTimeout:                  sourceTimeout,
		MaxConcurrentSources:           maxConcurrentSources,
		SourceCoverageMetricsEndpoint:  strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_METRICS_ENDPOINT")),
		SourceCoverageSourceID:         strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_SOURCE_ID")),
		SourceCoverageDeploymentID:     strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_DEPLOYMENT_ID")),
		SourceCoverageInterval:         coverageInterval,
		ArchiveObjectStoreURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_OSS_GATEWAY_URL")), "/"),
		ArchiveObjectStorageID:         strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_STORAGE_ID")),
		ArchiveObjectPrefix:            strings.Trim(strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_PREFIX")), "/"),
		AdmissionBudgetProfile:         admissionBudgetProfile(),
		AdmissionBudgetMetricsEndpoint: strings.TrimSpace(os.Getenv("BKN_TRACE_ADMISSION_COLLECTOR_METRICS_ENDPOINT")),
		AdmissionBudgetThresholds: AdmissionBudgetThresholdsConfig{
			OpenSearchCapacity: admissionBudgetThreshold("BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD"),
			OpenSearchHeap:     admissionBudgetThreshold("BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD"),
			CollectorQueue:     admissionBudgetThreshold("BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD"),
			StoragePool:        admissionBudgetThreshold("BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD"),
		},
	}
}

func admissionBudgetProfile() string {
	profile := strings.TrimSpace(os.Getenv("BKN_TRACE_ADMISSION_BUDGET_PROFILE"))
	if profile == "" {
		return "default"
	}
	return profile
}

func admissionBudgetThreshold(name string) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 || parsed > 1 {
		slog.Warn("invalid Trace admission budget threshold; leaving source unavailable", "name", name, "value", value)
		return 0
	}
	return parsed
}
