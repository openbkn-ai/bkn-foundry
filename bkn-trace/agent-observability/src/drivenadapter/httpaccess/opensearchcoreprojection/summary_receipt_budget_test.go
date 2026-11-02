// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchcoreprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

func budgetReceipt(trace string, index int) map[string]any {
	at := time.Date(2026, 10, 30, 4, 0, index, 0, time.UTC)
	return map[string]any{
		"receipt_id": fmt.Sprintf("%s-%d", trace, index), "trace_id": trace,
		"request_id": fmt.Sprintf("req-%s-%d", trace, index), "interaction_id": "int-1", "conversation_id": "conv-1",
		"owner":          map[string]any{"effective_subject_id": "user-1", "effective_subject_type": "user"},
		"receipt_status": "completed", "tool_name": "run_sql", "issued_at": at.Format(time.RFC3339Nano),
		"terminal_at": at.Add(time.Second).Format(time.RFC3339Nano),
	}
}

func TestTraceSummaryReceiptRefillIsBoundedAndCompleteWhenPageFits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		heavy, limit      int
		artifactTruncated bool
		failRefill        bool
		foreignAndTime    bool
		wantLoaded        int
		wantTruncated     bool
	}{
		{name: "spare budget", heavy: 35, limit: 60, wantLoaded: 36},
		{name: "exact budget", heavy: 35, limit: 36, wantLoaded: 36},
		{name: "over budget", heavy: 70, limit: 40, wantLoaded: 40, wantTruncated: true},
		{name: "artifact still truncated", heavy: 35, limit: 60, artifactTruncated: true, wantLoaded: 36, wantTruncated: true},
		{name: "refill dependency failure", heavy: 35, limit: 60, failRefill: true},
		{name: "scope time and concurrent arrivals", heavy: 35, limit: 60, foreignAndTime: true, wantLoaded: 36},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, firstLoaded := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var query map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if tc.foreignAndTime && !strings.Contains(string(query["query"]), `"range":{"issued_at"`) {
					t.Error("receipt read lost its time bounds")
				}
				if calls == 1 {
					var collapse struct {
						Inner struct {
							Size int `json:"size"`
						} `json:"inner_hits"`
					}
					if err := json.Unmarshal(query["collapse"], &collapse); err != nil {
						t.Error(err)
					}
					share := min(20, tc.limit/2)
					if collapse.Inner.Size != share {
						t.Errorf("initial fair share=%d, want %d", collapse.Inner.Size, share)
					}
					groups := []any{}
					for _, trace := range []string{"heavy", "light"} {
						total := tc.heavy
						if trace == "light" {
							total = 1
						}
						hits := []any{}
						for i := total - 1; i >= max(0, total-share); i-- {
							hits = append(hits, map[string]any{"_source": budgetReceipt(trace, i)})
							firstLoaded++
						}
						groups = append(groups, map[string]any{"inner_hits": map[string]any{"selected_receipts": map[string]any{"hits": map[string]any{"total": map[string]any{"value": total}, "hits": hits}}}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": groups}})
					return
				}
				if calls > 2 {
					t.Error("receipt refill must not loop")
				}
				if tc.failRefill {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if len(query["collapse"]) != 0 {
					t.Error("refill must spend remaining budget across unread receipts")
				}
				var size int
				_ = json.Unmarshal(query["size"], &size)
				if size != tc.limit-firstLoaded+1 {
					t.Errorf("refill size=%d exceeds remaining budget plus sentinel", size)
				}
				var filters struct {
					Bool struct {
						MustNot []struct {
							Terms map[string][]string `json:"terms"`
						} `json:"must_not"`
					} `json:"bool"`
				}
				_ = json.Unmarshal(query["query"], &filters)
				excluded := map[string]bool{}
				for _, filter := range filters.Bool.MustNot {
					for _, id := range filter.Terms["receipt_id.keyword"] {
						excluded[id] = true
					}
				}
				if len(excluded) != firstLoaded {
					t.Errorf("excluded %d receipts, want %d", len(excluded), firstLoaded)
				}
				if !strings.Contains(string(query["query"]), `"trace_id.keyword":["heavy","light"]`) {
					t.Error("refill lost selected Trace IDs")
				}
				hits := []any{}
				for i := tc.heavy - 1; i >= 0 && len(hits) < size; i-- {
					receipt := budgetReceipt("heavy", i)
					if !excluded[receipt["receipt_id"].(string)] {
						hits = append(hits, map[string]any{"_source": receipt})
					}
				}
				if tc.foreignAndTime {
					foreign := budgetReceipt("heavy", 5)
					foreign["receipt_id"], foreign["request_id"] = "foreign", "private-request"
					foreign["owner"] = map[string]any{"effective_subject_id": "foreign-user", "effective_subject_type": "user"}
					outside := budgetReceipt("heavy", 200)
					hits = append(hits, map[string]any{"_source": foreign}, map[string]any{"_source": outside})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
			}))
			t.Cleanup(server.Close)
			artifacts := &recordingArtifactProjectionSource{artifactResult: iprojectionsource.ArtifactResult{Truncated: tc.artifactTruncated}}
			source := opensearchcoreprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "core", artifacts)
			options := iprojectionsource.Query{
				Scope:    evidencevo.QueryScope{AccountID: "user-1", AccountType: "user"},
				TraceIDs: []string{"heavy", "light"}, SummaryOnly: true, Limit: tc.limit,
			}
			if tc.foreignAndTime {
				options.From = time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC)
				options.To = options.From.Add(time.Minute)
			}
			result, err := source.LoadExecutionProjection(context.Background(), options)
			if tc.failRefill {
				if err == nil {
					t.Fatal("refill dependency failure was hidden")
				}
				return
			}
			if err != nil || calls != 2 || len(result.Traces) != 2 || result.Truncated != tc.wantTruncated {
				t.Fatalf("calls=%d traces=%d truncated=%v error=%v", calls, len(result.Traces), result.Truncated, err)
			}
			loaded := 0
			for _, trace := range result.Traces {
				loaded += len(trace.Events)
			}
			if loaded != tc.wantLoaded {
				t.Fatalf("loaded %d receipts, want %d", loaded, tc.wantLoaded)
			}
			if tc.heavy < tc.limit {
				_, summaries := evidencevo.BuildExecutionSummaries(result.Traces, nil)
				for _, summary := range summaries {
					if summary.TraceID == "heavy" && (summary.RequestID != "req-heavy-0" || summary.DurationMS != int64(tc.heavy)*1000) {
						t.Fatalf("refill failed to restore full lifetime/root request: %+v", summary)
					}
				}
			}
		})
	}
}

func TestTraceSummaryReceiptInitialShareRespectsWholePageBudget(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		identities, limit, receipts int
		wantTruncated               bool
	}{
		{name: "small complete page needs no refill", identities: 2, limit: 40, receipts: 3},
		{name: "full budget preserves both identities", identities: 2, limit: 40, receipts: 35, wantTruncated: true},
		{name: "maximum page keeps global cap", identities: 200, limit: 2000, receipts: 11, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := make([]string, tc.identities)
			for i := range ids {
				ids[i] = fmt.Sprintf("trace-%d", i)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls != 1 {
					t.Error("full/complete initial page must not issue a refill")
				}
				var query struct {
					Collapse struct {
						Inner struct {
							Size int `json:"size"`
						} `json:"inner_hits"`
					} `json:"collapse"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
				}
				share := min(20, tc.limit/tc.identities)
				if query.Collapse.Inner.Size != share {
					t.Errorf("share=%d, want %d", query.Collapse.Inner.Size, share)
				}
				groups := []any{}
				for _, id := range ids {
					hits := []any{}
					for i := 0; i < min(tc.receipts, share); i++ {
						hits = append(hits, map[string]any{"_source": budgetReceipt(id, i)})
					}
					groups = append(groups, map[string]any{"inner_hits": map[string]any{"selected_receipts": map[string]any{"hits": map[string]any{"total": map[string]any{"value": tc.receipts}, "hits": hits}}}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": groups}})
			}))
			t.Cleanup(server.Close)
			source := opensearchcoreprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "core", nil)
			result, err := source.LoadExecutionProjection(context.Background(), iprojectionsource.Query{Scope: evidencevo.QueryScope{AccountID: "user-1", AccountType: "user"}, TraceIDs: ids, SummaryOnly: true, Limit: tc.limit})
			if err != nil || calls != 1 || len(result.Traces) != tc.identities || result.Truncated != tc.wantTruncated {
				t.Fatalf("calls=%d traces=%d truncated=%v error=%v", calls, len(result.Traces), result.Truncated, err)
			}
			loaded := 0
			for _, trace := range result.Traces {
				loaded += len(trace.Events)
			}
			if loaded > tc.limit {
				t.Fatalf("loaded %d receipts exceeds budget %d", loaded, tc.limit)
			}
		})
	}
}
