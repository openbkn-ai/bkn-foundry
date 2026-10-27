// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

// Exercise the real detail entry and real HTTP adapters. Core receipts already
// supply the traces; legacy evidence is discarded and supporting bodies are not
// used for summary text. Their failure must not hide readable authorized facts.
func TestInteractionSummaryAvoidsDiscardedLegacyAndSupportingBodyDependencies(t *testing.T) {
	for _, failure := range []string{"discarded legacy unavailable", "supporting body unavailable", "discarded legacy timeout"} {
		t.Run(failure, func(t *testing.T) {
			question, answer := "Can current stock cover the customer order?", "Inventory can cover part of the request."
			documents := map[string]map[string]any{}
			for _, kind := range []string{"question", "result", "supporting", "foreign"} {
				content, artifactType := question, kind
				if kind == "result" {
					content = answer
				}
				if kind == "supporting" || kind == "foreign" {
					artifactType = "data_result"
					content = "supporting result body must remain unread"
				}
				raw, _ := json.Marshal(content)
				document := map[string]any{"artifact_id": kind, "artifact_type": artifactType, "interaction_id": "int-1", "bkn.request.id": "req-lifecycle", "bkn.account.id": "producer-other", "bkn.account.type": "app", "observed_at": "2026-09-14T10:00:00Z", "content_hash": "hash-" + kind, "content_json": string(raw)}
				if kind == "supporting" {
					document["operation_id"] = "op-1"
					document["bkn.request.id"] = "req-1"
					document["trace_id"] = "trace-1"
					document["business_refs"] = []string{"resource:inventory"}
				}
				if kind == "foreign" {
					document["interaction_id"] = "int-foreign"
					document["bkn.account.id"] = "foreign-user"
				}
				documents[kind] = document
			}
			legacySearches, fullBodySearches, metadataSearches, terminalGets := 0, 0, 0, 0
			legacyStarted := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodHead:
					w.WriteHeader(http.StatusNotFound)
				case r.Method == http.MethodPut:
					_, _ = io.WriteString(w, `{"acknowledged":true}`)
				case r.URL.Path == "/core/_search":
					response := strings.Replace(summaryReceiptResponse, `}]}}`, `},{"_source":{"receipt_id":"foreign","owner":{"effective_subject_type":"user","effective_subject_id":"foreign-user"},"conversation_id":"conv-foreign","interaction_id":"int-foreign","operation_id":"op-foreign","request_id":"req-1","trace_id":"trace-foreign","receipt_status":"completed","issued_at":"2026-09-14T10:00:00Z"}}]}}`, 1)
					_, _ = io.WriteString(w, response)
				case r.URL.Path == "/legacy/_search":
					legacySearches++
					if failure == "discarded legacy timeout" {
						// Let the real client's existing deadline cancel the
						// outstanding HTTP read; no synthetic caller cancel.
						_, _ = io.Copy(io.Discard, r.Body)
						legacyStarted <- struct{}{}
						<-r.Context().Done()
						return
					}
					if failure == "discarded legacy unavailable" {
						http.Error(w, "discarded legacy evidence unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = io.WriteString(w, `{"hits":{"hits":[]}}`)
				case r.URL.Path == "/legacy-artifacts/_search":
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), `"excludes":["content_json"]`) {
						fullBodySearches++
						http.Error(w, "supporting content unavailable", http.StatusServiceUnavailable)
						return
					}
					metadataSearches++
					if !strings.Contains(string(raw), `"interaction_id":["int-1"]`) || strings.Contains(string(raw), `"trace_id"`) {
						t.Errorf("artifact read must retain authorized interaction selector: %s", raw)
					}
					hits := []any{}
					for _, kind := range []string{"question", "result", "supporting", "foreign"} {
						metadata := map[string]any{}
						for k, v := range documents[kind] {
							if k != "content_json" {
								metadata[k] = v
							}
						}
						hits = append(hits, map[string]any{"_source": metadata, "sort": []any{metadata["observed_at"], kind}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
				case r.URL.Path == "/legacy-artifacts/_mget":
					terminalGets++
					var request struct {
						IDs []string `json:"ids"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					ids := append([]string(nil), request.IDs...)
					slices.Sort(ids)
					if !slices.Equal(ids, []string{"question", "result"}) {
						t.Errorf("only authorized terminal bodies may be fetched, got %v", ids)
					}
					docs := []any{}
					for _, id := range request.IDs {
						docs = append(docs, map[string]any{"_id": id, "found": true, "_source": documents[id]})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"docs": docs})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second)
			legacy := opensearchevidencestore.New(client, "legacy")
			source := opensearchcoreprojection.New(client, "core", legacy)
			scope := evidencevo.QueryScope{AccountID: "user-1", AccountType: "user"}
			// Positive control proves the existing narrow HTTP path is sufficient,
			// including supporting metadata and isolation of a foreign interaction.
			control, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{Scope: scope, InteractionID: "int-1", SummaryOnly: true, Limit: 20})
			if err != nil || len(control.Traces) != 1 || len(control.Artifacts) != 3 {
				t.Fatalf("narrow source control failed: err=%v traces=%d artifacts=%d", err, len(control.Traces), len(control.Artifacts))
			}
			if legacySearches != 0 || fullBodySearches != 0 || metadataSearches != 1 || terminalGets != 1 {
				t.Fatalf("narrow control made unnecessary reads: legacy=%d full_body=%d metadata=%d terminal_gets=%d", legacySearches, fullBodySearches, metadataSearches, terminalGets)
			}
			for _, artifact := range control.Artifacts {
				if artifact.InteractionID != "int-1" {
					t.Fatal("foreign interaction artifact leaked")
				}
				if artifact.ArtifactID == "question" && artifact.Content != question || artifact.ArtifactID == "result" && artifact.Content != answer {
					t.Fatal("narrow control lost terminal original text")
				}
				if artifact.ArtifactID == "supporting" && (artifact.Content != nil || !slices.Contains(artifact.BusinessRefs, "resource:inventory")) {
					t.Fatal("supporting metadata lost or body unexpectedly read")
				}
			}
			legacySearches, fullBodySearches, metadataSearches, terminalGets = 0, 0, 0, 0
			service := evidencesvc.New(legacy, evidencesvc.WithProjectionSource(source))
			summary, found, err := service.GetInteractionSummary(context.Background(), "int-1", scope)
			if failure == "discarded legacy timeout" && err != nil {
				// Synchronize the handler's counter before reporting RED.
				select {
				case <-legacyStarted:
				default:
					t.Fatalf("timeout failure did not reach discarded legacy query: %v", err)
				}
			}
			if err != nil || !found {
				t.Fatalf("detail summary depended on discarded data: err=%v found=%v legacy=%d full_body=%d metadata=%d terminal_gets=%d", err, found, legacySearches, fullBodySearches, metadataSearches, terminalGets)
			}
			if legacySearches != 0 || fullBodySearches != 0 || metadataSearches != 1 || terminalGets != 1 {
				t.Fatalf("unnecessary detail dependency: legacy=%d full_body=%d metadata=%d terminal_gets=%d", legacySearches, fullBodySearches, metadataSearches, terminalGets)
			}
			if summary.InteractionQuestion != question || summary.InteractionResult != answer || summary.QuestionArtifactRef != "artifact:question" || summary.ResultArtifactRef != "artifact:result" {
				t.Fatalf("terminal text/references lost: %+v", summary)
			}
			if len(summary.Requests) != 1 || len(summary.Traces) != 1 || summary.Requests[0].RequestID != "req-1" || !slices.Contains(summary.Requests[0].BusinessRefs, "resource:inventory") {
				t.Fatalf("authorized metadata lost or foreign request included: %+v", summary)
			}
		})
	}
}
