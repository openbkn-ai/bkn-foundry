// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
