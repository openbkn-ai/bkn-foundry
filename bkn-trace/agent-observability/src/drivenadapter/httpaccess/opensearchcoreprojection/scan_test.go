// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

type scanFixture struct {
	t                         *testing.T
	seeds, receipts           []receiptDocument
	opened, closed, seedPages int
	closeStatus               int
	interactionQueries        int
	malformed                 string
	seedBody                  map[string]any
}

func (f *scanFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		f.closed++
		if f.closeStatus != 0 {
			w.WriteHeader(f.closeStatus)
		}
		_, _ = w.Write([]byte(`{"pits":[{"pit_id":"isolated-pit","successful":true}]}`))
		return
	}
	if strings.Contains(r.URL.Path, "point_in_time") {
		f.opened++
		_, _ = w.Write([]byte(`{"pit_id":"isolated-pit"}`))
		return
	}
	var q map[string]any
	if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
		f.t.Error(e)
		w.WriteHeader(400)
		return
	}
	if _, ok := q["pit"]; !ok {
		f.t.Error("search must use PIT")
	}
	if f.malformed != "" {
		_, _ = w.Write([]byte(f.malformed))
		return
	}
	var rows []receiptDocument
	if _, seed := q["_source"]; seed {
		f.seedPages++
		f.seedBody = q
		rows = f.seeds
	} else {
		if strings.Contains(fmt.Sprint(q["query"]), "interaction_id.keyword") {
			f.interactionQueries++
		}
		filters := q["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
		receiptOnly := false
		for _, raw := range filters {
			if e, ok := raw.(map[string]any)["exists"].(map[string]any); ok && e["field"] == "receipt_id" {
				receiptOnly = true
			}
		}
		if !receiptOnly {
			f.t.Error("complete receipt expansion lacks receipt_id exists filter")
		}

		for _, receipt := range f.receipts {
			keep := true
			for _, raw := range filters {
				filter := raw.(map[string]any)
				if terms, ok := filter["terms"].(map[string]any); ok {
					field := "trace_id.keyword"
					if _, ok := terms["interaction_id.keyword"]; ok {
						field = "interaction_id.keyword"
					}
					values := terms[field].([]any)
					found := false
					for _, v := range values {
						target := receipt.TraceID
						if field == "interaction_id.keyword" {
							target = receipt.InteractionID
						}
						if v == target {
							found = true
						}
					}
					keep = keep && found
				}
				if term, ok := filter["term"].(map[string]any); ok {
					if v, ok := term["interaction_id.keyword"].(map[string]any); ok {
						keep = keep && v["value"] == receipt.InteractionID
					}
				}
			}
			if keep {
				rows = append(rows, receipt)
			}
		}
	}
	offset := 0
	if a, ok := q["search_after"].([]any); ok {
		offset = int(a[0].(float64))
	}
	hits := []any{}
	end := min(offset+int(q["size"].(float64)), len(rows))
	for i := offset; i < end; i++ {
		hits = append(hits, map[string]any{"_source": rows[i], "sort": []any{i + 1, rows[i].ReceiptID}})
	}
	if e := json.NewEncoder(w).Encode(map[string]any{"timed_out": false, "_shards": map[string]any{"failed": 0}, "hits": map[string]any{"hits": hits}}); e != nil {
		f.t.Error(e)
	}
}
func scanReceipt(id, trace, interaction, owner, tool, at string) receiptDocument {
	return receiptDocument{ReceiptID: id, TraceID: trace, InteractionID: interaction, RequestID: "req-" + trace, Owner: sessionvo.Owner{EffectiveSubjectType: "user", EffectiveSubjectID: owner}, ToolName: tool, IssuedAt: at}
}
func scanSource(t *testing.T, f *scanFixture) *Source {
	t.Helper()
	f.t = t
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return New(opensearch.NewWithHTTPClient(server.URL, opensearch.AuthConfig{}, server.Client()), "receipts", nil)
}
func TestScanPITMultiPageFullLifetimeAndUniqueTraces(t *testing.T) {
	f := &scanFixture{}
	for i := 0; i < 257; i++ {
		r := scanReceipt(fmt.Sprintf("r-%03d", i), fmt.Sprintf("t-%03d", i), "", "owner", "current", "2026-10-23T00:00:00Z")
		f.seeds = append(f.seeds, r)
		f.receipts = append(f.receipts, r)
	}
	f.receipts = append(f.receipts, scanReceipt("old-root", "t-000", "", "owner", "\u3000original\u0085", "2020-01-01T00:00:00Z"))
	s := scanSource(t, f)
	seen := map[string]bool{}
	root := ""
	q := iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	err := s.ScanTraceSummaryProjection(context.Background(), q, func(r iprojectionsource.Result) error {
		for _, trace := range r.Traces {
			if seen[trace.TraceID] {
				t.Fatal("duplicate callback trace")
			}
			seen[trace.TraceID] = true
			if trace.TraceID == "t-000" {
				root = r.CanonicalRoots[trace.TraceID]
				if len(trace.Events) != 1 {
					t.Fatalf("root outside window became status event: %d", len(trace.Events))
				}
			}
		}
		return nil
	})
	if err != nil || len(seen) != 257 || root != "original" || f.seedPages != 2 || f.opened != 1 || f.closed != 1 {
		t.Fatalf("scan err=%v traces=%d root=%q pages=%d open/close=%d/%d", err, len(seen), root, f.seedPages, f.opened, f.closed)
	}
}
func TestScanSharedInteractionAuthorizationAndSiblingQueue(t *testing.T) {
	a := scanReceipt("a", "shared-a", "allowed", "foreign", "a", "2026-10-23T00:00:00Z")
	a.KnowledgeNetworkIDs = []string{"managed"}
	sibling := scanReceipt("b", "shared-b", "allowed", "foreign", "b", "2026-10-23T00:00:00Z")
	denied := scanReceipt("c", "denied", "separate", "foreign", "secret", "2026-10-23T00:00:00Z")
	denied.KnowledgeNetworkIDs = []string{"other"}
	legacy := scanReceipt("d", "legacy", "", "owner", "legacy", "2026-10-23T00:00:00Z")
	f := &scanFixture{seeds: []receiptDocument{a, denied, legacy}, receipts: []receiptDocument{a, sibling, denied, legacy}}
	s := scanSource(t, f)
	seen := map[string]int{}
	q := iprojectionsource.Query{Scope: evidencevo.QueryScope{AccessProfile: &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "owner", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"managed"}}}}
	err := s.ScanTraceSummaryProjection(context.Background(), q, func(r iprojectionsource.Result) error {
		for _, v := range r.Traces {
			seen[v.TraceID]++
		}
		return nil
	})
	if err != nil || seen["shared-a"] != 1 || seen["shared-b"] != 1 || seen["legacy"] != 1 || seen["denied"] != 0 {
		t.Fatalf("authorization err=%v result=%v", err, seen)
	}
	raw, e := json.Marshal(f.seedBody)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "knowledge_network_ids.keyword") || !strings.Contains(string(raw), "owner.effective_subject_id") {
		t.Fatal("seed lost scope pushdown")
	}
}
func TestScanFailuresAlwaysClosePIT(t *testing.T) {
	for _, tc := range []struct {
		name, raw   string
		closeStatus int
		visitor     bool
	}{
		{"missing", `{}`, 0, false}, {"missing-source", `{"timed_out":false,"_shards":{"failed":0},"hits":{"hits":[{"sort":[1,"r"]}]}}`, 0, false}, {"timeout", `{"timed_out":true,"_shards":{"failed":0},"hits":{"hits":[]}}`, 0, false}, {"shard", `{"timed_out":false,"_shards":{"failed":1},"hits":{"hits":[]}}`, 0, false}, {"visitor", "", 0, true}, {"close", "", 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := scanReceipt("r", "t", "", "owner", "x", "2026-10-23T00:00:00Z")
			f := &scanFixture{seeds: []receiptDocument{r}, receipts: []receiptDocument{r}, malformed: tc.raw, closeStatus: tc.closeStatus}
			s := scanSource(t, f)
			visitorErr := errors.New("visitor failure")
			err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(iprojectionsource.Result) error {
				if tc.visitor {
					return visitorErr
				}
				return nil
			})
			if err == nil || f.closed != 1 {
				t.Fatalf("err=%v closed=%d", err, f.closed)
			}
			if tc.visitor && !errors.Is(err, visitorErr) {
				t.Fatal("visitor error lost")
			}
		})
	}
}
func TestScanReceiptBudgetRefusesBeforeCallback(t *testing.T) {
	r := scanReceipt("seed", "t", "", "owner", "x", "2026-10-23T00:00:00Z")
	f := &scanFixture{seeds: []receiptDocument{r}}
	for i := 0; i < scanReceiptBudget+1; i++ {
		v := r
		v.ReceiptID = fmt.Sprintf("r-%d", i)
		f.receipts = append(f.receipts, v)
	}
	s := scanSource(t, f)
	calls := 0
	err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(iprojectionsource.Result) error { calls++; return nil })
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || calls != 0 || f.closed != 1 {
		t.Fatalf("err=%v calls=%d closed=%d", err, calls, f.closed)
	}
}

func TestScanFiltersBeforeNarrowArtifactReads(t *testing.T) {
	for _, tc := range []struct {
		status, tool string
		want         int
	}{
		{"error", "old-root", 1}, {"completed", "old-root", 0}, {"error", "current", 0}, {"unsupported", "", 0},
	} {
		t.Run(tc.status+tc.tool, func(t *testing.T) {
			r := scanReceipt("current", "t", "interaction", "owner", "current", "2026-10-23T00:00:00Z")
			r.Status = "error"
			old := scanReceipt("old", "t", "interaction", "owner", " old-root ", "2020-01-01T00:00:00Z")
			f := &scanFixture{seeds: []receiptDocument{r}, receipts: []receiptDocument{r, old}}
			s := scanSource(t, f)
			a := &scanArtifactSource{}
			s.artifacts = a
			calls := 0
			err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: tc.status, Tool: tc.tool}, func(iprojectionsource.Result) error { calls++; return nil })
			if err != nil || calls != tc.want || len(a.artifactProjectionQueries) != tc.want || len(a.queries) != 0 {
				t.Fatalf("err=%v visits=%d narrow=%d broad=%d", err, calls, len(a.artifactProjectionQueries), len(a.queries))
			}
			if tc.want > 0 {
				q := a.artifactProjectionQueries[0]
				if len(q.AuthorizedInteractionIDs) != 1 || q.AuthorizedInteractionIDs[0] != "interaction" || len(q.TraceIDs) != 0 {
					t.Fatalf("bad handoff %#v", q)
				}
			}
		})
	}
}

func TestScanArtifactTruncationFailsAndMixedLegacyIsScoped(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprint(truncated), func(t *testing.T) {
			r := scanReceipt("r", "t", "i", "owner", "x", "2026-10-23T00:00:00Z")
			legacy := scanReceipt("l", "legacy", "", "owner", "x", "2026-10-23T00:00:00Z")
			f := &scanFixture{seeds: []receiptDocument{r, legacy}, receipts: []receiptDocument{r, legacy}}
			s := scanSource(t, f)
			a := &scanArtifactSource{artifactResult: iprojectionsource.ArtifactResult{Truncated: truncated}}
			s.artifacts = a
			calls := 0
			err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(iprojectionsource.Result) error { calls++; return nil })
			if truncated {
				if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || calls != 0 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil || calls != 1 || len(a.artifactProjectionQueries) != 2 {
				t.Fatalf("err=%v calls=%d queries=%d", err, calls, len(a.artifactProjectionQueries))
			}
			q := a.artifactProjectionQueries[1]
			if len(q.AuthorizedInteractionIDs) != 0 || len(q.TraceIDs) != 1 || q.TraceIDs[0] != "legacy" || q.Scope.AccountID != "owner" {
				t.Fatal("legacy scope or selector lost")
			}
		})
	}
}

type scanArtifactSource struct {
	artifactResult                     iprojectionsource.ArtifactResult
	queries, artifactProjectionQueries []iprojectionsource.Query
}

func (s *scanArtifactSource) LoadExecutionProjection(_ context.Context, q iprojectionsource.Query) (iprojectionsource.Result, error) {
	s.queries = append(s.queries, q)
	return iprojectionsource.Result{}, nil
}
func (s *scanArtifactSource) LoadArtifactProjection(_ context.Context, q iprojectionsource.Query) (iprojectionsource.ArtifactResult, error) {
	s.artifactProjectionQueries = append(s.artifactProjectionQueries, q)
	return s.artifactResult, nil
}

func TestScanExactReceiptBudgetAndCancellation(t *testing.T) {
	for _, cancelVisit := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelVisit), func(t *testing.T) {
			r := scanReceipt("seed", "t", "", "owner", "x", "2026-10-23T00:00:00Z")
			f := &scanFixture{seeds: []receiptDocument{r}}
			for i := 0; i < scanReceiptBudget; i++ {
				v := r
				v.ReceiptID = fmt.Sprintf("r-%05d", i)
				f.receipts = append(f.receipts, v)
			}
			s := scanSource(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := s.ScanTraceSummaryProjection(ctx, iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(result iprojectionsource.Result) error {
				calls++
				if len(result.Traces) != 1 || len(result.Traces[0].Events) != scanReceiptBudget {
					t.Fatal("exact boundary lost receipts")
				}
				if cancelVisit {
					cancel()
					return ctx.Err()
				}
				return nil
			})
			if calls != 1 || f.closed != 1 {
				t.Fatalf("calls=%d close=%d", calls, f.closed)
			}
			if cancelVisit {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScanBatchesInteractionExpansionAndScopes(t *testing.T) {
	f := &scanFixture{}
	for i := 0; i < 64; i++ {
		r := scanReceipt(fmt.Sprint(i), fmt.Sprint(i), "i-"+fmt.Sprint(i), "owner", "x", "2026-10-23T00:00:00Z")
		r.ConversationID = "c"
		f.seeds = append(f.seeds, r)
		f.receipts = append(f.receipts, r)
	}
	s := scanSource(t, f)
	count := 0
	err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(r iprojectionsource.Result) error {
		count += len(r.Traces)
		for _, trace := range r.Traces {
			tuples := r.CanonicalScopes[trace.TraceID]
			if len(tuples) != 1 || tuples[0].InteractionID != "i-"+trace.TraceID || tuples[0].ConversationID != "c" {
				t.Fatal("incorrect authorized scopes")
			}
		}
		return nil
	})
	if err != nil || count != 64 || f.interactionQueries != 1 {
		t.Fatalf("err=%v count=%d interaction queries=%d", err, count, f.interactionQueries)
	}
}
func TestScanInactiveAndEmptyScopeAvoidPIT(t *testing.T) {
	for _, scope := range []evidencevo.QueryScope{{}, {AccessProfile: &evidencevo.AccessProfile{AccountActive: false, Roles: []string{"admin"}}}} {
		f := &scanFixture{}
		s := scanSource(t, f)
		err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: scope}, func(iprojectionsource.Result) error { t.Fatal("unexpected callback"); return nil })
		if err != nil || f.opened != 0 {
			t.Fatalf("err=%v opened=%d", err, f.opened)
		}
	}
}

func TestScanDenseInteractionBudgetFailsBeforeCallback(t *testing.T) {
	seed := scanReceipt("seed", "selected", "dense", "owner", "x", "2026-10-23T00:00:00Z")
	f := &scanFixture{seeds: []receiptDocument{seed}, receipts: []receiptDocument{seed}}
	for i := 0; i < scanReceiptBudget; i++ {
		f.receipts = append(f.receipts, scanReceipt(fmt.Sprint(i), "sibling-"+fmt.Sprint(i), "dense", "owner", "x", "2026-10-23T00:00:00Z"))
	}
	s := scanSource(t, f)
	calls := 0
	err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(iprojectionsource.Result) error { calls++; return nil })
	if !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || calls != 0 || f.closed != 1 {
		t.Fatalf("err=%v calls=%d closed=%d", err, calls, f.closed)
	}
}

func TestScanMalformedFullReceiptFailsClosed(t *testing.T) {
	for _, field := range []string{"id", "time"} {
		t.Run(field, func(t *testing.T) {
			seed := scanReceipt("valid", "t", "i", "owner", "x", "2026-10-23T00:00:00Z")
			bad := seed
			if field == "id" {
				bad.ReceiptID = ""
			} else {
				bad.IssuedAt = "invalid-date"
			}
			f := &scanFixture{seeds: []receiptDocument{seed}, receipts: []receiptDocument{bad}}
			s := scanSource(t, f)
			calls := 0
			err := s.ScanTraceSummaryProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}}, func(iprojectionsource.Result) error { calls++; return nil })
			if err == nil || calls != 0 || f.closed != 1 {
				t.Fatalf("err=%v calls=%d close=%d", err, calls, f.closed)
			}
		})
	}
}
