// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

// This crosses a real loopback HTTP socket, production handler/service, and real
// MariaDB. Trusted identity headers are supplied by the harness; ingress/OAuth,
// OpenSearch, EE aggregation and browser rendering are intentionally not covered.
func capacityHTTPSummaries(t *testing.T, store *Store) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", httphandler.NewSessionHandler(sessionsvc.New(store, sessionsvc.Options{})))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 30 * time.Second
	get := func(path, identity string) (int, []byte, float64) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if identity != "" {
			req.Header.Set("X-BKN-Application-Principal-ID", "capacity-app")
			req.Header.Set("X-BKN-Effective-Subject-Type", "service")
			req.Header.Set("X-BKN-Effective-Subject-ID", identity)
		}
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, raw, float64(time.Since(started).Microseconds()) / 1000
	}
	result := map[string]any{}
	for _, n := range []int{84, 112} {
		path := fmt.Sprintf("/api/agent-observability/v1/interactions/capacity-large-%d/operations", n)
		samples := []float64{}
		size := 0
		for i := 0; i < 43; i++ {
			status, raw, elapsed := get(path+"?view=summary", "capacity-owner")
			if status != 200 {
				t.Fatalf("HTTP summary: %d %s", status, raw)
			}
			var body struct {
				Entries []json.RawMessage `json:"entries"`
				Total   int               `json:"total"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Entries) != n || body.Total != n || len(raw) >= 1024*1024 {
				t.Fatalf("summary n=%d total=%d entries=%d bytes=%d", n, body.Total, len(body.Entries), len(raw))
			}
			for _, field := range []string{`"input"`, `"output"`, `"error"`, `"capability_profile"`} {
				if strings.Contains(string(raw), field) {
					t.Fatalf("HTTP summary includes %s", field)
				}
			}
			size = len(raw)
			if i >= 3 {
				samples = append(samples, elapsed)
			}
		}
		sort.Float64s(samples)
		fullSizes := map[string]int{}
		for _, view := range []string{"", "?view=full"} {
			status, raw, _ := get(path+view, "capacity-owner")
			if status != 200 {
				t.Fatalf("full HTTP status=%d", status)
			}
			var body struct {
				Entries []struct {
					Input struct {
						Inline json.RawMessage `json:"inline"`
					} `json:"input"`
					CapabilityProfile json.RawMessage `json:"capability_profile"`
				} `json:"entries"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if body.Total != n || len(body.Entries) != n || len(body.Entries[0].Input.Inline) != 256*1024+2 || len(body.Entries[0].CapabilityProfile) == 0 {
				t.Fatalf("full payload contract n=%d bytes=%d", n, len(raw))
			}
			fullSizes[view] = len(raw)
		}
		permissions := map[string]int{}
		for _, identity := range []string{"", "foreign-owner"} {
			expected := 401
			if identity != "" {
				expected = 404
			}
			for _, view := range []string{"", "?view=full", "?view=summary"} {
				status, raw, _ := get(path+view, identity)
				if status != expected {
					t.Fatalf("permission identity=%q view=%q status=%d body=%s", identity, view, status, raw)
				}
				if strings.Contains(string(raw), "capacity-large") || strings.Contains(string(raw), "synthetic-profile") {
					t.Fatal("denied response leaks fixture")
				}
				permissions[identity+view] = status
			}
		}
		result[fmt.Sprint(n)] = map[string]any{"summary_bytes": size, "p50_ms": samples[19], "p95_ms": samples[37], "samples_ms": samples, "full_response_bytes": fullSizes, "permissions": permissions, "transport": "real loopback HTTP + production SessionHandler/Service + isolated MariaDB; no OpenSearch or gateway"}
	}
	return result
}
