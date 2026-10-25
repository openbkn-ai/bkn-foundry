// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Small isolated integration for discovery/list consistency. The collision
// case reproduced the existing root-service mismatch before the scoped SQL fix.
func TestRootServiceOptionsIntegration(t *testing.T) {
	dsn := os.Getenv("BKN_ROOT_SERVICES_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	endpoint := os.Getenv("BKN_ROOT_SERVICES_TEST_OS")
	if (cfg.Addr != "127.0.0.1:13340" || endpoint != "http://127.0.0.1:19240") && (cfg.Addr != "127.0.0.1:3306" || endpoint != "http://127.0.0.1:9200") {
		t.Fatal("dedicated local or CI endpoints required")
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("bkn_trace_tool_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName, cfg.ParseTime = name, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pitRecorder := &remainingPITRecorder{base: http.DefaultTransport, owned: map[string]bool{}}
	counter := &rootFacetTransport{base: pitRecorder}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: counter}
	send := func(method, path string, body []byte) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode >= 300 {
			t.Fatalf("isolated OS %s %s status=%d err=%v", method, path, resp.StatusCode, err)
		}
		return raw
	}
	index := fmt.Sprintf("bkn-trace-remaining-%d", time.Now().UnixNano())
	deleteOwned := func(ownedIndex string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint+"/"+ownedIndex, nil)
		if err != nil {
			t.Error(err)
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
			t.Errorf("delete owned index %s status=%d", ownedIndex, resp.StatusCode)
		}
	}
	defer deleteOwned(index + "-v1")
	pitRecorder.openPath = "/" + index + "/_search/point_in_time"
	osClient := opensearch.NewWithHTTPClient(endpoint, opensearch.AuthConfig{}, httpClient)
	if err := opensearchprojection.New(osClient, index).EnsureBootstrap(ctx, index+"-v1"); err != nil {
		t.Fatal(err)
	}
	artifactIndex := index + "-evidence"
	defer deleteOwned(artifactIndex)
	defer deleteOwned(artifactIndex + "-artifacts")
	artifacts := opensearchevidencestore.New(osClient, artifactIndex)
	// Initialize the real narrow artifact adapter with an actual terminal result.
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	artifact, validation := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{
		ArtifactID: "remaining-artifact", ArtifactType: evidencevo.ArtifactTypeResult,
		RequestID: "remaining-00001", TraceID: "remaining-00001", InteractionID: "int-remaining-00001",
		ContentType: "application/json", SchemaVersion: evidencevo.ArtifactContractVersion,
		ObservedAt: now.Add(time.Second).Format(time.RFC3339Nano), Content: map[string]any{"text": "completed artifact must not erase failure"},
		AccountID: "remaining-owner", AccountType: "service", EffectiveSubjectID: "remaining-owner", ApplicationPrincipalID: "remaining-app",
	})
	if len(validation) != 0 {
		t.Fatalf("artifact validation: %+v", validation)
	}
	if _, err := artifacts.StoreArtifact(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	send("POST", "/"+artifactIndex+"-artifacts/_refresh", nil)
	owner := sessionvo.Owner{EffectiveSubjectID: "remaining-owner", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: "remaining-app"}
	scope := evidencevo.QueryScope{AccountID: owner.EffectiveSubjectID, AccountType: string(owner.EffectiveSubjectType)}
	var receipts []sessionvo.Receipt
	interactions := map[string]bool{}
	ordinal := uint64(0)
	payload := func(value any) *sessionvo.PayloadEnvelope {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		p, err := sessionvo.InlineJSONPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &p
	}
	save := func(tx isessionstore.Transaction, id, trace, interaction, conversation, tool string, at time.Time, who sessionvo.Owner, failed bool, errorPayload *sessionvo.PayloadEnvelope, networks ...string) {
		if !interactions[interaction] {
			ordinal++
			tx.SaveInteraction(sessionvo.Interaction{ID: interaction, ConversationID: conversation, Ordinal: ordinal, ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidenceComplete, StartIdempotencyKey: interaction, LeaseToken: interaction, LeaseExpiresAt: now.Add(24 * time.Hour), RowVersion: 1, CreatedAt: at, UpdatedAt: at})
			interactions[interaction] = true
		}
		status, receiptStatus := sessionvo.AttemptCompleted, sessionvo.ReceiptCompleted
		if failed {
			status, receiptStatus = sessionvo.AttemptFailed, sessionvo.ReceiptFailed
		}
		tx.SaveOperation(sessionvo.Operation{ID: id, ConversationID: conversation, InteractionID: interaction, OperationKey: id, ToolName: tool, AttemptStatus: status, RowVersion: 1, CreatedAt: at, UpdatedAt: at})
		fact := sessionvo.OperationCallFact{OperationID: id, Attempt: 1, ConversationID: conversation, InteractionID: interaction, TraceID: trace, RequestID: trace, ToolName: tool, Protocol: sessionvo.ProtocolMCP, SourceModule: "remaining-service", StartedAt: at, Status: status, Error: errorPayload}
		if id == "mixed-foreign-root" || id == "matrix-collision" {
			fact.SourceModule = "foreign-private-service"
		}
		if trace == "remaining-00006" {
			fact.Input = *payload("input-output-only-marker")
			fact.Output = payload("input-output-only-marker")
		}
		tx.SaveOperationCallFact(fact)
		refs := []sessionvo.BusinessRef{}
		for _, network := range networks {
			refs = append(refs, sessionvo.BusinessRef{RefType: sessionvo.BusinessRefKnowledgeNetwork, RefID: "kn:" + network})
		}
		r := sessionvo.Receipt{ID: id, SchemaVersion: "1", Attempt: 1, Owner: who, ConversationID: conversation, InteractionID: interaction, OperationID: id, OperationKey: id, ToolName: tool, Status: receiptStatus, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: trace, TraceID: trace, RowVersion: 1, IssuedAt: at, BusinessRefs: refs}
		tx.SaveReceipt(r)
		receipts = append(receipts, r)
	}
	seed := func(fn func(isessionstore.Transaction)) {
		t.Helper()
		if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error { fn(tx); return nil }); err != nil {
			t.Fatal(err)
		}
	}

	foreign := sessionvo.Owner{EffectiveSubjectID: "foreign", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: "foreign-app"}
	seed(func(tx isessionstore.Transaction) {
		for id, who := range map[string]sessionvo.Owner{"remaining-c": owner, "foreign-c": foreign} {
			tx.SaveConversation(sessionvo.Conversation{ID: id, Owner: who, ExternalConversationKey: id, Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
		}
		save(tx, "owned", "remaining-00001", "int-remaining-00001", "remaining-c", "root-tool", now, owner, true, payload("visible-failure"))
		save(tx, "mixed-foreign-root", "remaining-00001", "foreign-int", "foreign-c", "foreign-root", now.Add(-time.Hour), foreign, false, nil)
		save(tx, "shared", "shared-trace", "shared-int", "foreign-c", "shared-tool", now.Add(time.Second), foreign, false, nil, "kn-shared")
	})
	var bulk bytes.Buffer
	for _, r := range receipts {
		meta, _ := json.Marshal(map[string]any{"index": map[string]string{"_index": index, "_id": r.ID}})
		raw, e := json.Marshal(sessionvo.NewReceiptProjectionDocument(r))
		if e != nil {
			t.Fatal(e)
		}
		bulk.Write(meta)
		bulk.WriteByte('\n')
		bulk.Write(raw)
		bulk.WriteByte('\n')
	}
	var bulkResult struct {
		Errors bool `json:"errors"`
	}
	if e := json.Unmarshal(send("POST", "/_bulk?refresh=true", bulk.Bytes()), &bulkResult); e != nil || bulkResult.Errors {
		t.Fatal("bulk failed", e)
	}
	source := opensearchcoreprojection.New(osClient, index, artifacts)
	spans := &rootFacetSpans{}
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithProjectionSource(source), evidencesvc.WithTraceStatsSource(spans))
	global := evidencevo.QueryScope{AccountID: "admin", AccountType: "service", AccessProfile: &evidencevo.AccessProfile{EffectiveSubjectID: "admin", AccountActive: true, Roles: []string{"admin"}}}
	shared := evidencevo.QueryScope{AccountID: "reader", AccountType: "service", View: evidencevo.AccessViewBusiness, AccessProfile: &evidencevo.AccessProfile{EffectiveSubjectID: "reader", AccountActive: true, Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn-shared"}}}
	cases := []struct {
		name string
		o    evidencevo.SummaryQueryOptions
		want []string
	}{
		{"owner", evidencevo.SummaryQueryOptions{Scope: scope}, []string{"remaining-service"}},
		{"shared", evidencevo.SummaryQueryOptions{Scope: shared, Status: "completed"}, []string{"remaining-service"}},
		{"global", evidencevo.SummaryQueryOptions{Scope: global}, []string{"foreign-private-service", "remaining-service"}},
		{"filters", evidencevo.SummaryQueryOptions{Scope: scope, TraceID: "remaining-00001", From: now, To: now.Add(time.Second), Tool: "root-tool", Status: "error", ErrorKeyword: "visible-failure", Service: "ignored-selected"}, []string{"remaining-service"}},
		{"time-excludes", evidencevo.SummaryQueryOptions{Scope: scope, From: now.Add(time.Hour)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := counter.mgets
			spanBefore := spans.calls
			options, e := svc.ListTraceRootServices(ctx, tc.o, "", 100)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Join(options.Entries, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("options=%v want=%v", options.Entries, tc.want)
			}
			if counter.mgets != before || spans.calls != spanBefore {
				t.Fatal("facet loaded terminal bodies or Span stats")
			}
			for _, name := range options.Entries {
				o := tc.o
				o.Service = name
				o.Limit = 20
				page, e := svc.ListTraceExecutions(ctx, o)
				if e != nil || len(page.Entries) == 0 {
					t.Fatalf("selected service absent from list: %s %v", name, e)
				}
				for _, entry := range page.Entries {
					if entry.RootService != name {
						t.Fatal("service exact match changed")
					}
				}
			}
		})
	}
	if _, err := svc.ListTraceExecutions(ctx, evidencevo.SummaryQueryOptions{Scope: scope, Status: "error", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if counter.mgets == 0 {
		t.Fatal("ordinary list terminal content path was not exercised")
	}

	limited, err := svc.ListTraceRootServices(ctx, evidencevo.SummaryQueryOptions{Scope: global}, "", 1)
	if err != nil || len(limited.Entries) != 1 || !limited.HasMore {
		t.Fatalf("bounded options=%+v err=%v", limited, err)
	}
	searched, err := svc.ListTraceRootServices(ctx, evidencevo.SummaryQueryOptions{Scope: global}, "REMAINING", 10)
	if err != nil || strings.Join(searched.Entries, ",") != "remaining-service" {
		t.Fatalf("search=%+v err=%v", searched, err)
	}
	accent, err := svc.ListTraceRootServices(ctx, evidencevo.SummaryQueryOptions{Scope: global}, "rémaining", 10)
	if err != nil || len(accent.Entries) != 0 {
		t.Fatalf("accent folded unexpectedly: %+v %v", accent, err)
	}
	// Keep the OpenSearch projection but remove hot SQL membership, as archival does.
	if _, err = db.ExecContext(ctx, "DELETE FROM bkn_trace_receipts WHERE receipt_id=?", "owned"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.ListTraceRootServices(ctx, evidencevo.SummaryQueryOptions{Scope: scope}, "", 100)
	if err != nil || len(after.Entries) != 0 {
		t.Fatalf("deleted hot receipt remained discoverable: %+v err=%v", after, err)
	}
	t.Logf("3 receipt fixtures; owner/shared/global options match lists; facet no content mget/Span count; ordinary list mget calls=%d", counter.mgets)
}

type rootFacetTransport struct {
	base  http.RoundTripper
	mgets int
}

func (t *rootFacetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/_mget") {
		t.mgets++
	}
	return t.base.RoundTrip(r)
}

type rootFacetSpans struct{ calls int }

func (s *rootFacetSpans) CountSpansByTraceIDs(context.Context, []string) (map[string]int, error) {
	s.calls++
	return map[string]int{}, nil
}
