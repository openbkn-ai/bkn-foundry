// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

// This is strictly a Core projection PIT contract, not a SQL/OS transaction.
// No SQL, existing index, business endpoint or global PIT deletion is used.
func TestCoreSummarySnapshotIsolation(t *testing.T) {
	endpoint := os.Getenv("BKN_TRACE_CAPACITY_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("explicit isolated OpenSearch URL required")
	}
	if endpoint != "http://127.0.0.1:19274" {
		t.Fatal("dedicated loopback 19274 endpoint required")
	}
	index := fmt.Sprintf("bkn-trace-snapshot-%d", time.Now().UnixNano())
	recorder := &remainingPITRecorder{base: http.DefaultTransport, openPath: "/" + index + "/_search/point_in_time", owned: map[string]bool{}}
	client := &http.Client{Timeout: 30 * time.Second, Transport: recorder}
	send := func(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		return raw, resp.StatusCode, err
	}
	mustSend := func(method, path string, body []byte) []byte {
		t.Helper()
		raw, status, err := send(context.Background(), method, path, body)
		if err != nil || status >= 300 {
			t.Fatalf("isolated snapshot request %s status=%d err=%v", method, status, err)
		}
		return raw
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, status, err := send(ctx, http.MethodDelete, "/"+index+"-v1", nil)
		if err != nil || (status >= 300 && status != http.StatusNotFound) {
			t.Errorf("owned snapshot index cleanup status=%d err=%v", status, err)
		}
	}()
	osClient := opensearch.NewWithHTTPClient(endpoint, opensearch.AuthConfig{}, client)
	if err := opensearchprojection.New(osClient, index).EnsureBootstrap(context.Background(), index+"-v1"); err != nil {
		t.Fatal(err)
	}
	// Cleanup verification is independent of a failed/cancelled scan context.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		raw, status, err := send(ctx, http.MethodGet, "/_search/point_in_time/_all", nil)
		if err != nil || status >= 300 {
			t.Errorf("PIT inventory status=%d err=%v", status, err)
			return
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Error(err)
			return
		}
		present := map[string]bool{}
		var visit func(any)
		visit = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for key, value := range x {
					if key == "pit_id" {
						if id, ok := value.(string); ok {
							present[id] = true
						}
					}
					visit(value)
				}
			case []any:
				for _, value := range x {
					visit(value)
				}
			}
		}
		visit(decoded)
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		if len(recorder.owned) != 1 {
			t.Errorf("expected one owned PIT, got %d", len(recorder.owned))
		}
		for id := range recorder.owned {
			if present[id] {
				t.Error("scanner leaked its owned PIT")
			}
		}
	}()
	owner := sessionvo.Owner{EffectiveSubjectID: "snapshot-owner", EffectiveSubjectType: sessionvo.SubjectUser}
	start := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	makeReceipt := func(i int) sessionvo.Receipt {
		id := fmt.Sprintf("snapshot-%03d", i)
		return sessionvo.Receipt{ID: id, SchemaVersion: "1", Attempt: 1, Owner: owner, OperationID: id, OperationKey: id, RequestID: id, TraceID: id, ToolName: "original", Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityDurable, IssuedAt: start.Add(time.Duration(i) * time.Second), RowVersion: 1}
	}
	encode := func(v any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	appendReceipt := func(buffer *bytes.Buffer, r sessionvo.Receipt) {
		buffer.Write(encode(map[string]any{"index": map[string]string{"_index": index, "_id": r.ID}}))
		buffer.WriteByte('\n')
		buffer.Write(encode(sessionvo.NewReceiptProjectionDocument(r)))
		buffer.WriteByte('\n')
	}
	bulk := func(buffer *bytes.Buffer) {
		t.Helper()
		var result struct {
			Errors bool `json:"errors"`
		}
		if err := json.Unmarshal(mustSend(http.MethodPost, "/_bulk?refresh=true", buffer.Bytes()), &result); err != nil || result.Errors {
			t.Fatalf("snapshot bulk errors=%v decode=%v", result.Errors, err)
		}
	}
	var initial bytes.Buffer
	for i := 0; i < 520; i++ {
		appendReceipt(&initial, makeReceipt(i))
	}
	// Core indexes may contain non-receipt documents sharing trace identities.
	// This oldest, failed noise must not become a root, status event, or grant.
	initial.Write(encode(map[string]any{"index": map[string]string{"_index": index, "_id": "not-a-receipt"}}))
	initial.WriteByte('\n')
	initial.Write(encode(map[string]any{"trace_id": "snapshot-000", "request_id": "snapshot-000", "owner": owner, "issued_at": start.Add(-time.Hour).Format(time.RFC3339Nano), "receipt_status": "failed", "tool_name": "noise-private-tool", "knowledge_network_ids": []string{"noise-private-kn"}}))
	initial.WriteByte('\n')
	bulk(&initial)
	changed, deleted, newID := "snapshot-000", "snapshot-001", "snapshot-999"
	seen := map[string]evidencevo.TraceSummary{}
	roots := map[string]string{}
	callbacks := 0
	source := opensearchcoreprojection.New(osClient, index, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := source.ScanTraceSummaryProjection(ctx, iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: owner.EffectiveSubjectID, AccountType: string(owner.EffectiveSubjectType)}}, func(result iprojectionsource.Result) error {
		callbacks++
		_, summaries := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
		for _, summary := range summaries {
			if _, exists := seen[summary.TraceID]; exists {
				return fmt.Errorf("duplicate trace callback: %s", summary.TraceID)
			}
			seen[summary.TraceID] = summary
			roots[summary.TraceID] = result.CanonicalRoots[summary.TraceID]
		}
		if callbacks == 1 {
			if _, ok := seen[changed]; ok {
				return fmt.Errorf("changed target was already visited")
			}
			if _, ok := seen[deleted]; ok {
				return fmt.Errorf("deleted target was already visited")
			}
			replacement := makeReceipt(0)
			replacement.Status = sessionvo.ReceiptFailed
			replacement.ToolName = "changed-after-PIT"
			replacement.RowVersion = 2
			var mutation bytes.Buffer
			appendReceipt(&mutation, replacement)
			mutation.Write(encode(map[string]any{"delete": map[string]string{"_index": index, "_id": deleted}}))
			mutation.WriteByte('\n')
			appendReceipt(&mutation, makeReceipt(999))
			bulk(&mutation)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 520 || callbacks < 2 {
		t.Fatalf("snapshot traces=%d callbacks=%d", len(seen), callbacks)
	}
	for i := 0; i < 520; i++ {
		id := fmt.Sprintf("snapshot-%03d", i)
		if seen[id].TraceID != id || seen[id].Status != "completed" || roots[id] != "original" {
			t.Fatalf("snapshot changed %s: status=%q root=%q", id, seen[id].Status, roots[id])
		}
	}
	if _, found := seen[newID]; found {
		t.Fatal("post-PIT new trace leaked into snapshot")
	}
	// Prove mutations really happened in the live index, rather than a no-op test.
	var live struct {
		Source struct {
			Tool   string `json:"tool_name"`
			Status string `json:"receipt_status"`
		} `json:"_source"`
	}
	if err := json.Unmarshal(mustSend(http.MethodGet, "/"+index+"/_doc/"+changed, nil), &live); err != nil {
		t.Fatal(err)
	}
	if live.Source.Tool != "changed-after-PIT" || live.Source.Status != string(sessionvo.ReceiptFailed) {
		t.Fatal("live replacement did not take effect")
	}
	_, status, err := send(context.Background(), http.MethodGet, "/"+index+"/_doc/"+deleted, nil)
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("live deletion status=%d err=%v", status, err)
	}
	mustSend(http.MethodGet, "/"+index+"/_doc/"+newID, nil)
	t.Logf("PIT isolation: 520 original traces, %d batches, update/delete/new invisible until next scan; live mutations confirmed; owned PIT/index cleanup verified by deferred checks", callbacks)
}
