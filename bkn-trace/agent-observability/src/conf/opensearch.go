// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import (
	"os"
	"time"
)

type OpenSearchConfig struct {
	Endpoint      string
	TraceIndex    string
	EvidenceIndex string
	LogIndex      string
	Timeout       time.Duration
	Auth          OpenSearchAuthConfig
}

type OpenSearchAuthConfig struct {
	Enabled  bool
	Username string
	Password string
}

func NewOpenSearchConfig() OpenSearchConfig {
	endpoint := os.Getenv("OPENSEARCH_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9200"
	}

	traceIndex := os.Getenv("OPENSEARCH_TRACE_INDEX")
	if traceIndex == "" {
		traceIndex = "ss4o_traces-default-namespace"
	}

	evidenceIndex := os.Getenv("OPENSEARCH_EVIDENCE_INDEX")
	if evidenceIndex == "" {
		evidenceIndex = "bkn-trace-evidence-v2"
	}

	logIndex := os.Getenv("OPENSEARCH_LOG_INDEX")
	if logIndex == "" {
		logIndex = "ss4o_logs-default-namespace"
	}

	return OpenSearchConfig{
		Endpoint:      endpoint,
		TraceIndex:    traceIndex,
		EvidenceIndex: evidenceIndex,
		LogIndex:      logIndex,
		Timeout:       3 * time.Second,
		Auth: OpenSearchAuthConfig{
			Enabled:  os.Getenv("OPENSEARCH_AUTH_ENABLED") == "true",
			Username: os.Getenv("OPENSEARCH_AUTH_USERNAME"),
			Password: os.Getenv("OPENSEARCH_AUTH_PASSWORD"),
		},
	}
}
