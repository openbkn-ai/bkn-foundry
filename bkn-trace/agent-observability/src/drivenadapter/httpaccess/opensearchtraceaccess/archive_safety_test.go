// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

type archiveSafetyTransport func(*http.Request) (*http.Response, error)

func (f archiveSafetyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func archiveHTTP(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}
func completeArchiveSearch() map[string]any {
	return map[string]any{"timed_out": false, "_shards": map[string]any{"total": 1, "successful": 1, "failed": 0}, "hits": map[string]any{"total": map[string]any{"value": 1, "relation": "eq"}, "hits": []any{map[string]any{"_index": "trace-0001", "_id": "frozen", "_seq_no": 0, "_primary_term": 1, "_source": map[string]any{"traceId": "trace-a", "spanId": "span-a"}}}}}
}
func frozenArchiveCandidate() []archivesvc.Candidate {
	return []archivesvc.Candidate{{ID: "interaction-a", Payload: []byte(`{"call_facts":[{"trace_id":"trace-a"}]}`)}}
}
func TestTraceArchiveRejectsIncompleteSearch(t *testing.T) {
	for _, kind := range []string{"timeout", "failed-shard", "lower-bound-total", "missing-shards", "missing-total"} {
		t.Run(kind, func(t *testing.T) {
			value := completeArchiveSearch()
			switch kind {
			case "timeout":
				value["timed_out"] = true
			case "failed-shard":
				value["_shards"] = map[string]any{"total": 2, "successful": 1, "failed": 1}
			case "lower-bound-total":
				value["hits"].(map[string]any)["total"] = map[string]any{"value": 1, "relation": "gte"}
			case "missing-shards":
				delete(value, "_shards")
			case "missing-total":
				delete(value["hits"].(map[string]any), "total")
			}
			raw, _ := json.Marshal(value)
			client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(*http.Request) (*http.Response, error) { return archiveHTTP(string(raw)), nil })})
			if _, err := NewArchiveStore(client, "trace-read").Enrich(context.Background(), frozenArchiveCandidate()); err == nil {
				t.Fatal("incomplete source must not become a verified candidate")
			}
		})
	}
}
func TestTraceArchiveCleanupDoesNotDeleteLateSpan(t *testing.T) {
	documents := map[string]bool{"frozen": true}
	raw, _ := json.Marshal(completeArchiveSearch())
	client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/trace-read/_search":
			return archiveHTTP(string(raw)), nil
		case "/trace-read/_delete_by_query":
			count := len(documents)
			clear(documents)
			return archiveHTTP(fmt.Sprintf(`{"timed_out":false,"total":%d,"deleted":%d,"version_conflicts":0,"failures":[]}`, count, count)), nil
		case "/_bulk":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "late") {
				t.Fatal("cleanup selection included an unfrozen document")
			}
			delete(documents, "frozen")
			return archiveHTTP(`{"errors":false,"items":[{"delete":{"_index":"trace-0001","_id":"frozen","status":200,"result":"deleted","_shards":{"failed":0}}}]}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
	})})
	store := NewArchiveStore(client, "trace-read")
	candidates, err := store.Enrich(context.Background(), frozenArchiveCandidate())
	if err != nil {
		t.Fatal(err)
	}
	// A distinct document with the same Trace ID arrives after archive freezing.
	documents["late"] = true
	if err := store.Purge(context.Background(), candidates); err != nil {
		t.Fatal(err)
	}
	if !documents["late"] || documents["frozen"] {
		t.Fatalf("cleanup changed the wrong frozen selection: %v", documents)
	}
}

func TestTraceArchiveRejectsUnverifiableDocument(t *testing.T) {
	for _, kind := range []string{"missing-id", "missing-version", "foreign-trace", "duplicate-document"} {
		t.Run(kind, func(t *testing.T) {
			value := completeArchiveSearch()
			hits := value["hits"].(map[string]any)
			docs := hits["hits"].([]any)
			doc := docs[0].(map[string]any)
			switch kind {
			case "missing-id":
				delete(doc, "_id")
			case "missing-version":
				delete(doc, "_seq_no")
			case "foreign-trace":
				doc["_source"] = map[string]any{"traceId": "not-selected"}
			case "duplicate-document":
				hits["hits"] = append(docs, doc)
				hits["total"] = map[string]any{"value": 2, "relation": "eq"}
			}
			raw, _ := json.Marshal(value)
			client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(*http.Request) (*http.Response, error) { return archiveHTTP(string(raw)), nil })})
			if _, err := NewArchiveStore(client, "trace-read").Enrich(context.Background(), frozenArchiveCandidate()); err == nil {
				t.Fatal("unverifiable source must not be frozen")
			}
		})
	}
}

func TestTraceArchiveValidatesAllPersistedCleanupMetadataBeforeDeleting(t *testing.T) {
	requests := 0
	raw, _ := json.Marshal(completeArchiveSearch())
	client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Path != "/trace-read/_search" {
			t.Fatal("unverified selection sent a destructive request")
		}
		return archiveHTTP(string(raw)), nil
	})})
	store := NewArchiveStore(client, "trace-read")
	candidates, err := store.Enrich(context.Background(), frozenArchiveCandidate())
	if err != nil {
		t.Fatal(err)
	}
	// Persistence/restart must retain the concrete index, version and zero seq_no.
	encoded, _ := json.Marshal(candidates)
	var restored []archivesvc.Candidate
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored[0].Payload), `"seq_no":0`) || !strings.Contains(string(restored[0].Payload), `"index":"trace-0001"`) {
		t.Fatal("cleanup identity was not durable")
	}
	restored = append(restored, frozenArchiveCandidate()[0]) // legacy package lacks document identities
	if err := store.Purge(context.Background(), restored); err == nil {
		t.Fatal("legacy package must not widen cleanup to a Trace ID")
	}
	if requests != 1 {
		t.Fatalf("unexpected requests: %d", requests)
	}
}

func TestTraceArchiveProvenEmptySnapshotDoesNotCatchLaterDocuments(t *testing.T) {
	value := completeArchiveSearch()
	hits := value["hits"].(map[string]any)
	hits["total"] = map[string]any{"value": 0, "relation": "eq"}
	hits["hits"] = []any{}
	raw, _ := json.Marshal(value)
	requests := 0
	client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Path != "/trace-read/_search" {
			t.Fatal("empty frozen selection must never issue a delete")
		}
		return archiveHTTP(string(raw)), nil
	})})
	store := NewArchiveStore(client, "trace-read")
	candidates, err := store.Enrich(context.Background(), frozenArchiveCandidate())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(candidates[0].Payload), `"technical_trace_records":[]`) {
		t.Fatal("empty proof must remain distinguishable from legacy absence")
	}
	if err := store.Purge(context.Background(), candidates); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("unexpected requests: %d", requests)
	}
}
