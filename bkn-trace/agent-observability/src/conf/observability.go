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
	CursorSigningKey              []byte
	SourceTimeout                 time.Duration
	MaxConcurrentSources          int
	SourceCoverageMetricsEndpoint string
	SourceCoverageSourceID        string
	SourceCoverageDeploymentID    string
	SourceCoverageInterval        time.Duration
	ArchiveObjectStoreURL         string
	ArchiveObjectStorageID        string
	ArchiveObjectPrefix           string
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
		CursorSigningKey:              []byte(os.Getenv("BKN_OBSERVABILITY_CURSOR_SIGNING_KEY")),
		SourceTimeout:                 sourceTimeout,
		MaxConcurrentSources:          maxConcurrentSources,
		SourceCoverageMetricsEndpoint: strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_METRICS_ENDPOINT")),
		SourceCoverageSourceID:        strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_SOURCE_ID")),
		SourceCoverageDeploymentID:    strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_SOURCE_COVERAGE_DEPLOYMENT_ID")),
		SourceCoverageInterval:        coverageInterval,
		ArchiveObjectStoreURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_OSS_GATEWAY_URL")), "/"),
		ArchiveObjectStorageID:        strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_STORAGE_ID")),
		ArchiveObjectPrefix:           strings.Trim(strings.TrimSpace(os.Getenv("BKN_OBSERVABILITY_ARCHIVE_PREFIX")), "/"),
	}
}
