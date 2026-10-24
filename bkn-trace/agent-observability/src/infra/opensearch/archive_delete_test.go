// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
package opensearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArchiveDeleteRequiresCompleteRefreshedResult(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"complete", `{"timed_out":false,"total":2,"deleted":2,"version_conflicts":0,"failures":[]}`, true},
		{"idempotent", `{"timed_out":false,"total":0,"deleted":0,"version_conflicts":0,"failures":[]}`, true},
		{"missing", `{}`, false},
		{"timeout", `{"timed_out":true,"total":2,"deleted":2,"version_conflicts":0,"failures":[]}`, false},
		{"conflict", `{"timed_out":false,"total":2,"deleted":1,"version_conflicts":1,"failures":[]}`, false},
		{"failures", `{"timed_out":false,"total":2,"deleted":2,"version_conflicts":0,"failures":[{}]}`, false},
		{"partial", `{"timed_out":false,"total":2,"deleted":1,"version_conflicts":0,"failures":[]}`, false},
		{"failed", `{"timed_out":false,"total":2,"deleted":2,"version_conflicts":0,"failures":[],"failed":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("refresh") != "true" || r.URL.Query().Get("wait_for_completion") != "true" {
					t.Error("synchronous refresh required")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := NewWithHTTPClient(s.URL, AuthConfig{}, s.Client())
			err := c.DeleteByQuery(context.Background(), "logs", []byte(`{"query":{"ids":{"values":["frozen"]}}}`))
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
