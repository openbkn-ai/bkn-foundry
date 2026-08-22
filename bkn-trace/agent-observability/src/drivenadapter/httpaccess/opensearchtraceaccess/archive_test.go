// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import "testing"

func TestTraceIDsUsesFrozenCallFactsAndPreservesExistingIDs(t *testing.T) {
	ids, err := traceIDs([]byte(`{"call_facts":[{"trace_id":"trace-b"},{"trace_id":"trace-a"},{"trace_id":""}],"technical_trace_ids":["trace-a","trace-c"]}`))
	if err != nil {
		t.Fatalf("traceIDs returned error: %v", err)
	}
	want := []string{"trace-a", "trace-b", "trace-c"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %#v, want %#v", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("ids = %#v, want %#v", ids, want)
		}
	}
}

func TestSearchResultTruncatedPreventsUnarchivedTraceDeletion(t *testing.T) {
	if !searchResultTruncated(10001, 10000) {
		t.Fatal("a partial OpenSearch result must fail the archive before cleanup")
	}
	if searchResultTruncated(10000, 10000) {
		t.Fatal("a complete OpenSearch result must remain archivable")
	}
}
