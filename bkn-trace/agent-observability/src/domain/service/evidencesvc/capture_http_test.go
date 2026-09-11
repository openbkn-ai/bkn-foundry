// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestCaptureThroughBoundedHTTPStorePreservesWideIntegerAndTotalBudget(t *testing.T) {
	a := captureFixture("a", "material result")
	a.AccountID = "owner"
	a.AccountType = "user"
	bytes, _ := json.Marshal(a)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(bytes, &doc); err != nil {
		t.Fatal(err)
	}
	doc["content_json"], _ = json.Marshal(string(doc["content"]))
	delete(doc, "content")
	body, err := json.Marshal(map[string]any{"_source": doc})
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.New()
	if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveInteraction(sessionvo.Interaction{ID: "int-a", ConversationID: "conv-a", RowVersion: 1, ClosureManifest: &sessionvo.ClosureManifest{AnswerArtifactRef: "artifact:a"}})
		for _, receipt := range captureSnapshot("artifact:a", "artifact:b", "artifact:c").Receipts {
			tx.SaveReceipt(receipt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("capture wrote to store: %s", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/a") {
			// A late update occurs after the session snapshot and before content is returned.
			if err := sessions.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				interaction, _ := tx.PeekInteraction("int-a")
				interaction.RowVersion = 2
				interaction.ClosureManifest = &sessionvo.ClosureManifest{AnswerArtifactRef: "artifact:late"}
				tx.SaveInteraction(interaction)
				return nil
			}); err != nil {
				t.Errorf("late update: %v", err)
			}
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("x", 4096))
	}))
	defer server.Close()
	artifacts := opensearchevidencestore.New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "evidence")
	limits := captureLimits()
	limits.MaxResponseBytes = int64(len(body))
	limits.MaxReadBytes = int64(len(body) + 11)
	got, found, err := NewCaptureService(sessions, artifacts).Capture(context.Background(), "int-a", evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, limits)
	if err != nil || !found {
		t.Fatalf("capture: %v %v", found, err)
	}
	if got.Snapshot.Interaction.RowVersion != 1 || got.Snapshot.Interaction.ClosureManifest.AnswerArtifactRef != "artifact:a" {
		t.Fatal("capture silently adopted a late interaction revision")
	}
	if len(paths) != 2 || paths[0] != "GET /evidence-artifacts/_doc/a" || got.ReadBytes != limits.MaxReadBytes {
		t.Fatalf("unbounded request budget: %v %+v", paths, got)
	}
	if got.Artifacts[0].Content.State != evidencevo.ArtifactContentCaptured || !strings.Contains(string(got.Artifacts[0].Content.CanonicalJSON), "9223372036854775807") {
		t.Fatalf("precision/hash failure: %+v", got.Artifacts[0])
	}
	if got.Artifacts[1].Content.Reason != "response_bytes_limit" || got.Artifacts[2].Content.Reason != "read_bytes_limit" {
		t.Fatalf("partial states: %+v", got.Artifacts)
	}
}

func TestCaptureHTTPIdentityMismatchIsRejectedAndAccounted(t *testing.T) {
	body := `{"_source":{"artifact_id":"unexpected","content_json":"{\"private_data\":240}"}}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.WriteString(w, body) }))
	defer server.Close()
	store := opensearchevidencestore.New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "evidence")
	limits := captureLimits()
	limits.MaxResponseBytes = 1000
	result, _, err := NewCaptureService(&captureSnapshotReader{snapshot: captureSnapshot()}, store).Capture(context.Background(), "int-a", evidencevo.QueryScope{}, limits)
	if err != nil || calls != 1 || result.ReadBytes != int64(len(body)) || result.Artifacts[0].Content.State != evidencevo.ArtifactContentRejected || result.Artifacts[0].Content.Reason != "artifact_id_mismatch" || len(result.Artifacts[0].Content.CanonicalJSON) != 0 {
		t.Fatalf("identity rejection became availability error: %+v %v", result, err)
	}
}
