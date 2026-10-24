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
	"sort"
	"strings"
	"sync"
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

// The same public APIs and fixture run against the frozen predecessor for a
// genuine red reproduction; no new optional scan or metadata interface is used.
func TestSummaryRemainingFiltersAcceptance(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_CAPACITY_ADMIN_DSN")
	if dsn == "" {
		t.Skip("explicit isolated DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	endpoint := os.Getenv("BKN_TRACE_CAPACITY_OPENSEARCH_URL")
	if cfg.Addr != "127.0.0.1:13374" || endpoint != "http://127.0.0.1:19274" {
		t.Fatal("dedicated 13374/19274 endpoints required")
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
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: pitRecorder}
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
	foreign := sessionvo.Owner{EffectiveSubjectID: "matrix-foreign", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: "matrix-foreign-app"}
	matrixOwner := sessionvo.Owner{EffectiveSubjectID: "matrix-owner", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: "owner-only-app"}
	matrixApp := sessionvo.Owner{EffectiveSubjectID: "app-only-owner", EffectiveSubjectType: sessionvo.SubjectService, ApplicationPrincipalID: "matrix-client"}
	seed(func(tx isessionstore.Transaction) {
		for id, who := range map[string]sessionvo.Owner{"remaining-c": owner, "foreign-c": foreign, "matrix-owner-c": matrixOwner, "matrix-app-c": matrixApp} {
			tx.SaveConversation(sessionvo.Conversation{ID: id, Owner: who, ExternalConversationKey: id, Generation: 1, Status: sessionvo.ConversationClosed, RowVersion: 1, CreatedAt: now, UpdatedAt: now})
		}
	})
	for batch := 0; batch < 2202; batch += 50 {
		seed(func(tx isessionstore.Transaction) {
			for i := batch; i < min(batch+50, 2202); i++ {
				id := fmt.Sprintf("remaining-%05d", i)
				var failure *sessionvo.PayloadEnvelope
				switch i {
				case 0:
					failure = payload("oldest-visible")
				case 1:
					failure = payload("mixed-visible")
				case 2:
					failure = payload(strings.Repeat("\u754c", 512) + "cut-away-only")
				case 3:
					wrapped, _ := json.Marshal(map[string]any{"exit_code": 1, "stderr": "head-discarded" + strings.Repeat("\u884c", 600) + "tail-visible"})
					failure = payload(string(wrapped))
				case 4:
					failure = payload("First-Visible")
				case 5:
					failure = payload(map[string]any{"message": "priority-visible", "safe_error_summary": "lower-priority-only", "code": "code-only", "debug": "debug-only"})
				}
				save(tx, id+"-root", id, "int-"+id, "remaining-c", "run_sql", now.Add(time.Duration(i)*time.Second), owner, i < 7, failure)
			}
		})
	}
	seed(func(tx isessionstore.Transaction) {
		for i := 0; i < 25; i++ {
			save(tx, fmt.Sprintf("mixed-child-%02d", i), "remaining-00001", "int-remaining-00001", "remaining-c", "child-tool", now.Add(time.Second+time.Duration(i+1)*time.Millisecond), owner, false, nil)
		}
		save(tx, "second-failed", "remaining-00004", "int-remaining-00004", "remaining-c", "child-tool", now.Add(4*time.Second+time.Millisecond), owner, true, payload("second-failure-only"))
		// True root is outside the selected window and latest20, with an even
		// earlier foreign collision that must not replace the authorized root.
		save(tx, "mixed-old-root", "remaining-00001", "int-remaining-00001", "remaining-c", "\u00a0run_sql\u3000", now.Add(-time.Hour), owner, false, nil)
		save(tx, "mixed-foreign-root", "remaining-00001", "foreign-mixed-int", "foreign-c", "foreign-secret", now.Add(-2*time.Hour), foreign, false, nil)
		// Interleave grants and non-grants; same-interaction siblings inherit the
		// interaction-level grant, different interactions never do.
		save(tx, "matrix-no-grant", "matrix-denied", "matrix-denied-int", "foreign-c", "matrix-tool", now.Add(5000*time.Second), foreign, false, nil)
		save(tx, "matrix-grant", "matrix-shared", "matrix-shared-int", "foreign-c", "shared-root", now.Add(2*time.Second), foreign, false, nil, "kn-shared")
		save(tx, "matrix-own", "matrix-own", "matrix-own-int", "matrix-owner-c", "matrix-tool", now.Add(5001*time.Second), matrixOwner, false, nil)
		save(tx, "matrix-sibling-root", "matrix-sibling", "matrix-shared-int", "foreign-c", "shared-root", now.Add(-10*time.Minute), foreign, false, nil)
		save(tx, "matrix-app", "matrix-app", "matrix-app-int", "matrix-app-c", "matrix-tool", now.Add(5002*time.Second), matrixApp, false, nil)
		for i := 0; i < 25; i++ {
			save(tx, fmt.Sprintf("matrix-child-%02d", i), "matrix-sibling", "matrix-shared-int", "foreign-c", "child-tool", now.Add(3*time.Second+time.Duration(i)*time.Millisecond), foreign, false, nil)
		}
		collision := foreign
		collision.EffectiveSubjectID = "collision-owner"
		collision.ApplicationPrincipalID = "collision-app"
		save(tx, "matrix-collision", "matrix-sibling", "collision-int", "foreign-c", "foreign-secret", now.Add(-time.Hour), collision, false, nil)
	})
	var bulk bytes.Buffer
	for _, r := range receipts {
		meta, _ := json.Marshal(map[string]any{"index": map[string]string{"_index": index, "_id": r.ID}})
		doc := sessionvo.NewReceiptProjectionDocument(r)
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		bulk.Write(meta)
		bulk.WriteByte('\n')
		bulk.Write(raw)
		bulk.WriteByte('\n')
	}
	var bulkResult struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(send("POST", "/_bulk?refresh=true", bulk.Bytes()), &bulkResult); err != nil || bulkResult.Errors {
		t.Fatalf("bulk errors=%v decode=%v", bulkResult.Errors, err)
	}
	source := opensearchcoreprojection.New(osClient, index, artifacts)
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithSessionStore(store), evidencesvc.WithProjectionSource(source))
	// Read-only PIT inventory: never close another task's point-in-time context.
	pitInventory := func() map[string]bool {
		var decoded any
		if err := json.Unmarshal(send("GET", "/_search/point_in_time/_all", nil), &decoded); err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		var visit func(any)
		visit = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if k == "pit_id" {
						if id, ok := v.(string); ok {
							ids[id] = true
						}
					}
					visit(v)
				}
			case []any:
				for _, v := range x {
					visit(v)
				}
			}
		}
		visit(decoded)
		return ids
	}
	pitsBefore := pitInventory()
	defer func() {
		after := pitInventory()
		pitRecorder.mu.Lock()
		defer pitRecorder.mu.Unlock()
		for id := range pitRecorder.owned {
			if after[id] && !pitsBefore[id] {
				t.Errorf("summary scan leaked its owned PIT %q", id)
			}
		}
	}()
	query := func(t *testing.T, o evidencevo.SummaryQueryOptions) evidencevo.TraceSummaryPage {
		t.Helper()
		start := time.Now()
		page, err := svc.ListTraceExecutions(ctx, o)
		if err != nil {
			t.Fatalf("summary status=%q keyword=%q: %v", o.Status, o.ErrorKeyword, err)
		}
		if page.Truncated || page.Partial {
			t.Fatalf("exact scan became partial: %+v", page)
		}
		t.Logf("absolute scan cost status=%q keyword=%q total=%d entries=%d elapsed_ms=%.3f", o.Status, o.ErrorKeyword, page.Total, len(page.Entries), float64(time.Since(start).Microseconds())/1000)
		return page
	}
	assertIDs := func(t *testing.T, o evidencevo.SummaryQueryOptions, want ...string) evidencevo.TraceSummaryPage {
		t.Helper()
		p := query(t, o)
		got := []string{}
		for _, e := range p.Entries {
			got = append(got, e.TraceID)
		}
		sort.Strings(got)
		sort.Strings(want)
		if p.Total != len(want) || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("wanted IDs %v got %v total=%d", want, got, p.Total)
		}
		return p
	}
	t.Run("status-complete-pagination", func(t *testing.T) {
		o := evidencevo.SummaryQueryOptions{Scope: scope, Status: "completed", Limit: 200}
		seen := map[string]bool{}
		for pages := 0; ; pages++ {
			if pages > 12 {
				t.Fatal("cursor did not terminate")
			}
			p := query(t, o)
			if p.Total != 2195 {
				t.Fatalf("completed total=%d", p.Total)
			}
			for _, e := range p.Entries {
				if seen[e.TraceID] || e.Status != "completed" {
					t.Fatalf("duplicate/wrong status: %+v", e)
				}
				seen[e.TraceID] = true
			}
			if p.NextCursor == nil {
				break
			}
			o.Cursor = *p.NextCursor
		}
		if len(seen) != 2195 {
			t.Fatalf("reachable completed=%d", len(seen))
		}
		p := query(t, evidencevo.SummaryQueryOptions{Scope: scope, Status: "completed", Page: 30, Limit: 100})
		if p.Total != 2195 || len(p.Entries) != 0 {
			t.Fatalf("empty page total lost: %+v", p)
		}
	})
	t.Run("status-mixed-window-root", func(t *testing.T) {
		o := evidencevo.SummaryQueryOptions{Scope: scope, Status: "error", Limit: 20}
		assertIDs(t, o, "remaining-00000", "remaining-00001", "remaining-00002", "remaining-00003", "remaining-00004", "remaining-00005", "remaining-00006")
		o.From, o.To = now.Add(time.Second+time.Millisecond), now.Add(time.Second+25*time.Millisecond)
		assertIDs(t, o)
		o.Status = "completed"
		p := assertIDs(t, o, "remaining-00001")
		if p.Entries[0].RootOperation != "run_sql" {
			t.Fatalf("window/owner/latest20 changed root: %+v", p.Entries[0])
		}
	})
	t.Run("error-final-readable-summary", func(t *testing.T) {
		cases := []struct{ keyword, trace string }{
			{"oldest-visible", "remaining-00000"}, {"mixed-visible", "remaining-00001"}, {"\u754c\u754c", "remaining-00002"}, {"tail-visible", "remaining-00003"}, {"FIRST-VISIBLE", "remaining-00004"}, {"priority-visible", "remaining-00005"}, {"OpenBKN operation failed", "remaining-00006"},
			{"cut-away-only", ""}, {"head-discarded", ""}, {"second-failure-only", ""}, {"lower-priority-only", ""}, {"code-only", ""}, {"debug-only", ""}, {"input-output-only-marker", ""},
		}
		for _, tc := range cases {
			o := evidencevo.SummaryQueryOptions{Scope: scope, ErrorKeyword: tc.keyword, Limit: 20}
			if tc.trace == "" {
				assertIDs(t, o)
			} else {
				p := assertIDs(t, o, tc.trace)
				if tc.trace == "remaining-00002" && p.Entries[0].ErrorSummary != strings.Repeat("\u754c", 512)+"\u2026" {
					t.Fatal("512-rune prefix changed")
				}
				if tc.trace == "remaining-00003" && (!strings.HasPrefix(p.Entries[0].ErrorSummary, "\u2026") || len([]rune(p.Entries[0].ErrorSummary)) != 513) {
					t.Fatal("512-rune stderr tail changed")
				}
			}
		}

		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: scope, Status: "completed", ErrorKeyword: "oldest-visible", Limit: 20})
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: scope, Status: "error", Tool: "run_sql", Service: "remaining-service", ErrorKeyword: "oldest-visible", Limit: 20}, "remaining-00000")
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: scope, Status: "error", Tool: "child-tool", ErrorKeyword: "mixed-visible", Limit: 20})
	})
	profileScope := func(p evidencevo.AccessProfile) evidencevo.QueryScope {
		return evidencevo.QueryScope{AccountID: p.EffectiveSubjectID, AccountType: "service", View: evidencevo.AccessViewBusiness, AccessProfile: &p}
	}
	baseProfile := evidencevo.AccessProfile{EffectiveSubjectID: "matrix-reader", ApplicationPrincipalID: "reader-app", AccountActive: true}
	t.Run("shared-authorization-matrix", func(t *testing.T) {
		ownerProfile := baseProfile
		ownerProfile.EffectiveSubjectID = "matrix-owner"
		ownerProfile.ApplicationPrincipalID = "matrix-client"
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(ownerProfile), Status: "completed", Limit: 20}, "matrix-own", "matrix-app")
		managed := baseProfile
		managed.Roles = []string{"network_builder"}
		managed.ManagedKnowledgeNetworkIDs = []string{"kn-shared"}
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(managed), Limit: 20}, "matrix-shared", "matrix-sibling")
		combined := ownerProfile
		combined.Roles = managed.Roles
		combined.ManagedKnowledgeNetworkIDs = managed.ManagedKnowledgeNetworkIDs
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(combined), Limit: 20}, "matrix-own", "matrix-app", "matrix-shared", "matrix-sibling")
		p := assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(managed), Tool: "shared-root", From: now, To: now.Add(10 * time.Second), Limit: 20}, "matrix-shared", "matrix-sibling")
		for _, e := range p.Entries {
			if e.RootOperation != "shared-root" {
				t.Fatalf("unauthorized collision/root leaked: %+v", e)
			}
		}
		p = assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(managed), Service: "remaining-service", Limit: 20}, "matrix-shared", "matrix-sibling")
		for _, e := range p.Entries {
			if e.RootService != "remaining-service" {
				t.Fatalf("unauthorized interaction source leaked: %+v", e)
			}
		}
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(managed), Service: "foreign-private-service", Limit: 20})
		noIntersection := managed
		noIntersection.ManagedKnowledgeNetworkIDs = []string{"kn-other"}
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(noIntersection), Status: "completed", Limit: 20})
		inactive := managed
		inactive.AccountActive = false
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(inactive), Status: "completed", Limit: 20})
		permissionOnly := baseProfile
		permissionOnly.ManagedKnowledgeNetworkIDs = []string{"kn-shared"}
		permissionOnly.Permissions = []evidencevo.Permission{{ResourceType: "*", ResourceID: "*", Operations: []string{"*"}}}
		assertIDs(t, evidencevo.SummaryQueryOptions{Scope: profileScope(permissionOnly), Status: "completed", Limit: 20})
		administrator := baseProfile
		administrator.Roles = []string{"admin"}
		p = query(t, evidencevo.SummaryQueryOptions{Scope: profileScope(administrator), Status: "completed", Limit: 20})
		if p.Total != 2200 {
			t.Fatalf("admin completed total=%d", p.Total)
		}
	})
	t.Log("Executed remaining filters: 2202 identities, exact status/keyword/cursors, real narrow failure SQL and artifact adapter, shared interaction grants and negative authorization, PIT lifecycle")
}

// Record ownership from successful PIT creation responses, never by assuming all
// newly observed cluster PITs belong to this test.
type remainingPITRecorder struct {
	base     http.RoundTripper
	openPath string
	mu       sync.Mutex
	owned    map[string]bool
}

func (r *remainingPITRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodPost || req.URL.Path != r.openPath || resp.StatusCode >= 300 {
		return resp, err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	var created struct {
		ID string `json:"pit_id"`
	}
	if json.Unmarshal(raw, &created) == nil && created.ID != "" {
		r.mu.Lock()
		r.owned[created.ID] = true
		r.mu.Unlock()
	}
	return resp, nil
}
