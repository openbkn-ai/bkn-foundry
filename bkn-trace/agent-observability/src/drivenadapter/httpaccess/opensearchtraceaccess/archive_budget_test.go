// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchtraceaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
)

func TestArchiveEnrichRejectsPayloadBudgets(t *testing.T) {
	for _, kind := range []string{"core", "batch", "raw-plus-core", "encoded-escaping", "metadata-plus-core", "enriched-batch"} {
		t.Run(kind, func(t *testing.T) {
			candidates := frozenArchiveCandidate()
			value := completeArchiveSearch()
			doc := value["hits"].(map[string]any)["hits"].([]any)[0].(map[string]any)
			switch kind {
			case "core":
				candidates[0].Payload = []byte(strings.Repeat(" ", archivesvc.MaxTraceArchiveCandidateBytes+1))
			case "batch":
				raw, _ := json.Marshal(map[string]any{"padding": strings.Repeat("a", 6<<20)})
				candidates = []archivesvc.Candidate{{ID: "a", Payload: raw}, {ID: "b", Payload: raw}, {ID: "c", Payload: raw}}
			case "raw-plus-core":
				candidates[0].Payload = []byte(`{"call_facts":[{"trace_id":"trace-a"}],"padding":"` + strings.Repeat("a", 4<<20) + `"}`)
				doc["_source"] = map[string]any{"traceId": "trace-a", "padding": strings.Repeat("b", 5<<20)}
			case "metadata-plus-core":
				candidates[0].Payload = []byte(`{"call_facts":[{"trace_id":"trace-a"}],"padding":"` + strings.Repeat("a", 4<<20) + `"}`)
				doc["_id"] = strings.Repeat("m", 5<<20)
			case "enriched-batch":
				second := candidates[0]
				second.ID = "b"
				third := candidates[0]
				third.ID = "c"
				candidates = append(candidates, second, third)
				doc["_source"] = map[string]any{"traceId": "trace-a", "padding": strings.Repeat("b", 6<<20)}
			case "encoded-escaping":
				doc["_source"] = map[string]any{"traceId": "trace-a", "padding": strings.Repeat("<", (8<<20)/6+1)}
			}
			raw, _ := json.Marshal(value)
			// Upstream JSON may legitimately leave HTML characters unescaped.
			body := strings.ReplaceAll(string(raw), `\u003c`, "<")
			requests := 0
			client := opensearch.NewWithHTTPClient("http://test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if !strings.HasSuffix(r.URL.Path, "/_search") {
					t.Fatal("enrichment performed a destructive request")
				}
				return archiveHTTP(body), nil
			})})
			got, err := NewArchiveStore(client, "traces").Enrich(context.Background(), candidates)
			if !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) || got != nil {
				t.Fatalf("got %d candidates, error %v", len(got), err)
			}
			if (kind == "core" || kind == "batch") && requests != 0 {
				t.Fatalf("oversized input made %d requests", requests)
			}
		})
	}
}

type archiveBudgetBody struct{ remaining, read int }

func (b *archiveBudgetBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = ' '
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (*archiveBudgetBody) Close() error { return nil }
func TestArchiveEnrichBoundsSearchResponse(t *testing.T) {
	body := &archiveBudgetBody{remaining: archivesvc.MaxTraceArchiveResponseBytes + 100}
	client := opensearch.NewWithHTTPClient("http://test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })})
	got, err := NewArchiveStore(client, "traces").Enrich(context.Background(), frozenArchiveCandidate())
	if got != nil || !errors.Is(err, archivesvc.ErrArchivePayloadTooLarge) {
		t.Fatalf("got %d candidates, error %v", len(got), err)
	}
	if body.read > archivesvc.MaxTraceArchiveResponseBytes+1 {
		t.Fatalf("read %d bytes", body.read)
	}
}
func TestArchiveEnrichPreservesMultipleSmallCandidatesAndCancellation(t *testing.T) {
	raw, _ := json.Marshal(completeArchiveSearch())
	client := opensearch.NewWithHTTPClient("http://test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(*http.Request) (*http.Response, error) { return archiveHTTP(string(raw)), nil })})
	candidates := frozenArchiveCandidate()
	second := candidates[0]
	second.ID = "interaction-b"
	candidates = append(candidates, second)
	got, err := NewArchiveStore(client, "traces").Enrich(context.Background(), candidates)
	if err != nil || len(got) != 2 || got[0].ID != candidates[0].ID || got[1].ID != second.ID {
		t.Fatalf("multiple candidates: %v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewArchiveStore(client, "traces").Enrich(ctx, candidates); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestTraceArchiveEnrichmentPreservesFrozenJSONNumbersAndVerdictFields(t *testing.T) {
	response, _ := json.Marshal(completeArchiveSearch())
	client := opensearch.NewWithHTTPClient("http://archive-test", opensearch.AuthConfig{}, &http.Client{Transport: archiveSafetyTransport(func(*http.Request) (*http.Response, error) { return archiveHTTP(string(response)), nil })})
	payload := []byte(`{"interaction":{"interaction_id":"i","record_integrity_version":18446744073709551615,"stored_record_integrity":{"source_version":18446744073709551615,"applicable":false,"future":{"n":18446744073709551615}}},"call_facts":[{"trace_id":"trace-a"}]}`)
	result, err := NewArchiveStore(client, "trace-read").Enrich(context.Background(), []archivesvc.Candidate{{ID: "i", Payload: payload}})
	if err != nil || len(result) != 1 {
		t.Fatalf("enrich: %v", err)
	}
	var after struct {
		Interaction json.RawMessage `json:"interaction"`
	}
	var before struct {
		Interaction json.RawMessage `json:"interaction"`
	}
	if err := json.Unmarshal(result[0].Payload, &after); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &before); err != nil {
		t.Fatal(err)
	}
	if string(after.Interaction) != string(before.Interaction) {
		t.Fatalf("enrichment rewrote frozen SQL metadata: %s", after.Interaction)
	}
}
