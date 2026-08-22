// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import "testing"

func TestOpenSearchConfigUsesDeployedSS4OLogIndexByDefault(t *testing.T) {
	t.Setenv("OPENSEARCH_LOG_INDEX", "")
	config := NewOpenSearchConfig()
	if config.LogIndex != "ss4o_logs-default-namespace" {
		t.Fatalf("unexpected default log index: %q", config.LogIndex)
	}
}

func TestOpenSearchConfigAcceptsExplicitLogIndex(t *testing.T) {
	t.Setenv("OPENSEARCH_LOG_INDEX", "openbkn-logs-prod")
	config := NewOpenSearchConfig()
	if config.LogIndex != "openbkn-logs-prod" {
		t.Fatalf("explicit log index was ignored: %q", config.LogIndex)
	}
}
