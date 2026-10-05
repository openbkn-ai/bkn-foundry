// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func TestSummaryProjectionPreservesFactsWithoutLoadingSupportingContent(t *testing.T) {
	responseBytes := map[bool]int{}
	for _, summaryOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "summary"}[summaryOnly], func(t *testing.T) {
			question := normalizedOpenSearchArtifact(t)
			question.ArtifactID, question.ArtifactType = "question", evidencevo.ArtifactTypeQuestion
			question.Content = map[string]any{"text": "库存是多少？"}
			result := question
			result.ArtifactID, result.ArtifactType = "result", evidencevo.ArtifactTypeResult
			result.Content = map[string]any{"answer": "库存 230。"}
			data := question
			data.ArtifactID, data.ArtifactType = "data", evidencevo.ArtifactTypeDataResult
			data.Content = map[string]any{"rows": strings.Repeat("large-result", 100000)}
			data.BusinessRefs = []string{"supply/product"}
			foreign := question
			foreign.ArtifactID, foreign.AccountID = "foreign", "another-account"
			artifacts := []evidencevo.EvidenceArtifact{question, result, data, foreign}
			queries, gets := 0, 0
			client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPut {
					return jsonResponse(`{"acknowledged":true}`), nil
				}
				body, _ := io.ReadAll(r.Body)
				if strings.HasSuffix(r.URL.Path, "/_search") {
					queries++
					trimmed := strings.Contains(string(body), `"excludes":["content_json"]`)
					if trimmed != summaryOnly {
						t.Errorf("summary=%v source filtering=%s", summaryOnly, body)
					}
					hits := []any{}
					for _, a := range artifacts {
						doc, err := toArtifactDocument(a)
						if err != nil {
							t.Fatal(err)
						}
						if trimmed {
							doc.ContentJSON = ""
						}
						hits = append(hits, map[string]any{"_source": doc, "sort": []any{a.ObservedAt, a.ArtifactID}})
					}
					response, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
					responseBytes[summaryOnly] += len(response)
					return jsonResponse(string(response)), nil
				}
				if strings.HasSuffix(r.URL.Path, "/_mget") {
					gets++
					var request struct {
						IDs []string `json:"ids"`
					}
					if err := json.Unmarshal(body, &request); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(request.IDs, []string{"question", "result"}) {
						t.Fatalf("must fetch only authorized terminal IDs: %s", body)
					}
					docs := []any{}
					for _, a := range artifacts[:2] {
						doc, _ := toArtifactDocument(a)
						docs = append(docs, map[string]any{"_id": a.ArtifactID, "found": true, "_source": doc})
					}
					response, _ := json.Marshal(map[string]any{"docs": docs})
					responseBytes[summaryOnly] += len(response)
					return jsonResponse(string(response)), nil
				}
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				return nil, nil
			})
			store := New(client, "summary-test")
			page, err := store.LoadArtifactProjection(context.Background(), iprojectionsource.Query{
				Scope: evidencevo.QueryScope{AccountID: question.AccountID, AccountType: question.AccountType},
				Limit: 20, SummaryOnly: summaryOnly,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Artifacts) != 3 || page.Truncated {
				t.Fatalf("page=%+v", page)
			}
			byID := map[string]evidencevo.EvidenceArtifact{}
			for _, a := range page.Artifacts {
				byID[a.ArtifactID] = a
			}
			if !reflect.DeepEqual(byID["question"].Content, question.Content) || !reflect.DeepEqual(byID["result"].Content, result.Content) {
				t.Fatal("question/result content changed")
			}
			if !reflect.DeepEqual(byID["data"].BusinessRefs, data.BusinessRefs) || byID["data"].ContentHash != data.ContentHash {
				t.Fatal("supporting metadata changed")
			}
			if summaryOnly && byID["data"].Content != nil {
				t.Fatal("summary loaded large supporting content")
			}
			if !summaryOnly && !reflect.DeepEqual(byID["data"].Content, data.Content) {
				t.Fatal("full path lost content")
			}
			if queries != 1 || gets != map[bool]int{false: 0, true: 1}[summaryOnly] {
				t.Fatalf("queries=%d gets=%d", queries, gets)
			}
		})
	}
	if responseBytes[true]*100 >= responseBytes[false] {
		t.Fatalf("summary must remove supporting body even including terminal batch: full=%d summary=%d", responseBytes[false], responseBytes[true])
	}
	t.Logf("response bytes including terminal batch: full=%d summary=%d", responseBytes[false], responseBytes[true])
}

func TestSummaryTerminalContentRejectsMissingOrChangedAuthorizedDocument(t *testing.T) {
	for _, failure := range []string{"missing", "foreign", "type", "hash", "transport"} {
		t.Run(failure, func(t *testing.T) {
			selected := normalizedOpenSearchArtifact(t)
			selected.ArtifactType = evidencevo.ArtifactTypeQuestion
			selected.Content = nil
			client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.Path, "/_mget") {
					t.Fatalf("unexpected %s", r.URL.Path)
				}
				if failure == "transport" {
					return nil, io.ErrUnexpectedEOF
				}
				if failure == "missing" {
					return jsonResponse(`{"docs":[]}`), nil
				}
				loaded := selected
				loaded.Content = "answer"
				switch failure {
				case "foreign":
					loaded.AccountID = "another-account"
				case "type":
					loaded.ArtifactType = evidencevo.ArtifactTypeDataResult
				case "hash":
					loaded.ContentHash = "sha256:changed"
				}
				document, _ := toArtifactDocument(loaded)
				body, _ := json.Marshal(map[string]any{"docs": []any{map[string]any{"_id": selected.ArtifactID, "found": true, "_source": document}}})
				return jsonResponse(string(body)), nil
			})
			artifacts := []evidencevo.EvidenceArtifact{selected}
			err := New(client, "summary-test").loadSummaryTerminalContent(context.Background(), artifacts, iprojectionsource.Query{
				Scope: evidencevo.QueryScope{AccountID: selected.AccountID, AccountType: selected.AccountType},
			})
			if err == nil || artifacts[0].Content != nil {
				t.Fatalf("must reject %s without exposing content: err=%v", failure, err)
			}
		})
	}
}

func TestSummarySupportingMetadataDoesNotFetchContent(t *testing.T) {
	artifact := normalizedOpenSearchArtifact(t)
	artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("supporting metadata must not fetch %s", r.URL.Path)
		return nil, nil
	})
	if err := New(client, "summary-test").loadSummaryTerminalContent(context.Background(), []evidencevo.EvidenceArtifact{artifact}, iprojectionsource.Query{}); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryProjectionDoesNotHydrateTruncatedSentinel(t *testing.T) {
	question := normalizedOpenSearchArtifact(t)
	question.ArtifactID, question.ArtifactType = "selected", evidencevo.ArtifactTypeQuestion
	question.InteractionID, question.AccountID = "authorized-interaction", "another-account"
	question.Content = "question"
	sentinel := question
	sentinel.ArtifactID = "sentinel"
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			return jsonResponse(`{"acknowledged":true}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_search") {
			hits := []any{}
			for _, a := range []evidencevo.EvidenceArtifact{question, sentinel} {
				doc, _ := toArtifactDocument(a)
				doc.ContentJSON = ""
				hits = append(hits, map[string]any{"_source": doc, "sort": []any{a.ObservedAt, a.ArtifactID}})
			}
			body, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
			return jsonResponse(string(body)), nil
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(request.IDs, []string{"selected"}) {
			t.Fatalf("sentinel was fetched: %s", body)
		}
		doc, _ := toArtifactDocument(question)
		response, _ := json.Marshal(map[string]any{"docs": []any{map[string]any{"_id": question.ArtifactID, "found": true, "_source": doc}}})
		return jsonResponse(string(response)), nil
	})
	page, err := New(client, "summary-test").LoadArtifactProjection(context.Background(), iprojectionsource.Query{
		Scope: evidencevo.QueryScope{AccountID: "caller", AccountType: "app"}, Limit: 1, SummaryOnly: true,
		AuthorizedInteractionIDs: []string{question.InteractionID},
	})
	if err != nil || !page.Truncated || len(page.Artifacts) != 1 || page.Artifacts[0].Content != "question" {
		t.Fatalf("authorized handoff and original cap must survive: page=%+v err=%v", page, err)
	}
}

func TestSummaryTerminalContentUsesBoundedBatches(t *testing.T) {
	seed := normalizedOpenSearchArtifact(t)
	seed.ArtifactType, seed.Content = evidencevo.ArtifactTypeQuestion, nil
	artifacts := make([]evidencevo.EvidenceArtifact, 501)
	byID := map[string]evidencevo.EvidenceArtifact{}
	for index := range artifacts {
		artifacts[index] = seed
		artifacts[index].ArtifactID = fmt.Sprintf("q-%03d", index)
		byID[artifacts[index].ArtifactID] = artifacts[index]
	}
	batchSizes := []int{}
	client := newFakeOpenSearchClient(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		batchSizes = append(batchSizes, len(request.IDs))
		docs := []any{}
		for _, id := range request.IDs {
			a, ok := byID[id]
			if !ok {
				t.Fatalf("unselected ID %s", id)
			}
			a.Content = "question"
			doc, _ := toArtifactDocument(a)
			docs = append(docs, map[string]any{"_id": id, "found": true, "_source": doc})
		}
		response, _ := json.Marshal(map[string]any{"docs": docs})
		return jsonResponse(string(response)), nil
	})
	err := New(client, "summary-test").loadSummaryTerminalContent(context.Background(), artifacts, iprojectionsource.Query{
		Scope: evidencevo.QueryScope{AccountID: seed.AccountID, AccountType: seed.AccountType},
	})
	if err != nil || !reflect.DeepEqual(batchSizes, []int{500, 1}) {
		t.Fatalf("batchSizes=%v err=%v", batchSizes, err)
	}
	for _, a := range artifacts {
		if a.Content != "question" {
			t.Fatalf("lost terminal %s", a.ArtifactID)
		}
	}
}
