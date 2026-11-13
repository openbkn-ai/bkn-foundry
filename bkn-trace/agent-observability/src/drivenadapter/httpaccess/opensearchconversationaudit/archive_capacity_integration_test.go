// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package opensearchconversationaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestLogArchiveFrozenVersionsRealOpenSearch(t *testing.T) {
	endpoint := os.Getenv("BKN_LOG_ARCHIVE_TEST_OS")
	if endpoint == "" {
		t.Skip("isolated loopback OpenSearch endpoint required")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("loopback endpoint required")
	}
	index := fmt.Sprintf("trace2947-logs-%d", time.Now().UnixNano())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request := func(method, path string, body []byte) (int, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	status, _ := request("PUT", "/"+index, []byte(`{"settings":{"number_of_shards":1,"number_of_replicas":0}}`))
	if status != 200 {
		t.Fatal("create dedicated index failed")
	}
	defer func() { _, _ = request("DELETE", "/"+index, nil) }()
	old := time.Now().UTC().AddDate(0, 0, -31)
	raw, _ := json.Marshal(map[string]any{"@timestamp": old.Format(time.RFC3339Nano), "message": "original"})
	changed, _ := json.Marshal(map[string]any{"@timestamp": old.Format(time.RFC3339Nano), "message": "newer"})
	path := "/" + index + "/_doc/log-a?routing=tenant-a&refresh=true"
	if status, _ = request("PUT", path, raw); status != 201 {
		t.Fatal("seed log failed")
	}
	backend := opensearch.NewWithHTTPClient(endpoint, opensearch.AuthConfig{}, client)
	source := NewArchiveSource(backend, index)
	ctx := context.Background()
	window := observabilityvo.ArchiveRange{To: time.Now().UTC()}
	batch, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindLog, window)
	if err != nil || len(batch.Candidates) != 1 || batch.Candidates[0].LogDocument == nil || batch.Candidates[0].LogDocument.Routing != "tenant-a" {
		t.Fatalf("freeze versions: %+v %v", batch, err)
	}
	_, _ = request("PUT", path, changed)
	if err = source.Purge(ctx, observabilityvo.ArchiveKindLog, batch.Candidates); err == nil {
		t.Fatal("changed document was not fenced")
	}
	if status, _ = request("GET", path, nil); status != 200 {
		t.Fatal("changed document deleted")
	}
	fresh, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindLog, window)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Purge(ctx, observabilityvo.ArchiveKindLog, fresh.Candidates); err != nil {
		t.Fatal(err)
	}
	if status, _ = request("GET", path, nil); status != 404 {
		t.Fatal("verified frozen document not removed")
	}
	// Legacy recovery compares archived content before acquiring a current version.
	_, _ = request("PUT", path, raw)
	legacy, err := source.FreezeBatch(ctx, observabilityvo.ArchiveKindLog, window)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Candidates[0].LogDocument = nil
	_, _ = request("PUT", path, changed)
	if err = source.Purge(ctx, observabilityvo.ArchiveKindLog, legacy.Candidates); err == nil {
		t.Fatal("legacy content mismatch ignored")
	}
	if status, _ = request("GET", path, nil); status != 200 {
		t.Fatal("changed legacy source deleted")
	}
	_, _ = request("PUT", path, raw)
	if err = source.Purge(ctx, observabilityvo.ArchiveKindLog, legacy.Candidates); err != nil {
		t.Fatal(err)
	}
	if err = source.Purge(ctx, observabilityvo.ArchiveKindLog, legacy.Candidates); err != nil {
		t.Fatalf("legacy cleanup not idempotent: %v", err)
	}
	// One-record configured pages must still report all remaining eligible logs.
	for _, id := range []string{"a", "b", "c"} {
		_, _ = request("PUT", "/"+index+"/_doc/"+id+"?refresh=true", raw)
	}
	one := archivesvc.WithLogLimits(ctx, archivesvc.LogLimits{MaxCandidates: 1, MaxPayloadBytes: 1024})
	selected, err := source.FreezeBatch(one, observabilityvo.ArchiveKindLog, window)
	if err != nil || len(selected.Candidates) != 1 || selected.EligibleCount != 3 {
		t.Fatalf("single page lost total: %+v %v", selected, err)
	}
	t.Log("real OpenSearch: routing retained, stale version refused, changed legacy body retained, matching legacy recovery idempotent, single-page total preserved")
}
