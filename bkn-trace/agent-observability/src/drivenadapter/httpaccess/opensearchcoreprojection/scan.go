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
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

const scanPageSize = 256
const scanBatchSize = 64
const scanIdentityBudget = 65536
const scanReceiptBudget = 10000

// ScanTraceSummaryProjection keeps candidate discovery and all receipt
// expansion on one immutable PIT. Candidate hits never confer authorization.
func (s *Source) ScanTraceSummaryProjection(ctx context.Context, query iprojectionsource.Query, visit func(iprojectionsource.Result) error) (err error) {
	if visit == nil {
		return fmt.Errorf("summary scan visitor is required")
	}
	if query.Scope.AccessProfile != nil && !query.Scope.AccessProfile.AccountActive {
		return nil
	}
	if query.Scope.AccessProfile == nil && query.Scope.AccountID == "" {
		return nil
	}
	if p := query.Scope.AccessProfile; p != nil && !evidencevo.HasGlobalTraceAccess(*p) && !evidencevo.NeedsCrossAccountCandidates(query.Scope) && p.EffectiveSubjectID == "" && p.ApplicationPrincipalID == "" {
		return nil
	}
	if query.Scope.AccessProfile != nil && evidencevo.NeedsCrossAccountCandidates(query.Scope) && !evidencevo.HasGlobalTraceAccess(*query.Scope.AccessProfile) {
		query.FilterTraceCandidates = nil
	}
	pit, err := s.client.OpenPointInTime(ctx, s.index)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := s.client.ClosePointInTime(cleanup, pit); closeErr != nil {
			// Cleanup cannot invalidate a complete page or reclassify a scan
			// failure as a timeout. The PIT expires after one minute.
			slog.WarnContext(cleanup, "failed to close summary scan PIT; snapshot will expire", "error", closeErr)
		}
	}()
	seen := make(map[string]bool)
	pending := make([]string, 0)
	enqueue := func(id string) error {
		if id == "" || seen[id] {
			return nil
		}
		if len(seen) >= scanIdentityBudget {
			return iprojectionsource.ErrSummaryScanBudgetExceeded
		}
		seen[id] = true
		pending = append(pending, id)
		return nil
	}
	filters := scanSeedFilters(query)
	fields := []string{"trace_id", "interaction_id"}
	if query.FilterTraceCandidates != nil {
		fields = append(fields, "conversation_id", "owner")
	}
	err = s.scanReceiptPages(ctx, pit, filters, fields, 0, func(page []receiptDocument) error {
		var possible map[string]bool
		if query.FilterTraceCandidates != nil {
			candidates := make([]iprojectionsource.TraceSummaryCandidate, 0, len(page))
			for _, r := range page {
				if receiptMatchesScope(r, query.Scope) {
					candidates = append(candidates, iprojectionsource.TraceSummaryCandidate{TraceID: r.TraceID, ConversationID: r.ConversationID, InteractionID: r.InteractionID})
				}
			}
			ids, filterErr := query.FilterTraceCandidates(ctx, candidates)
			if filterErr != nil {
				return filterErr
			}
			possible = make(map[string]bool, len(ids))
			for _, id := range ids {
				possible[id] = true
			}
		}
		for _, r := range page {
			if possible != nil && !possible[r.TraceID] {
				continue
			}
			if err := enqueue(r.TraceID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	expandSibling := enqueue
	if query.FilterTraceCandidates != nil {
		// Owner/global discovery covers every candidate in the PIT already.
		// A sibling must pass its own metadata filter, not bypass it through an
		// otherwise matching Interaction. Shared-network discovery never filters.
		expandSibling = func(string) error { return nil }
	}
	for offset := 0; offset < len(pending); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+scanBatchSize, len(pending))
		ids := append([]string(nil), pending[offset:end]...)
		offset = end
		result, err := s.scanTraceBatch(ctx, pit, query, ids, expandSibling)
		if err != nil {
			return err
		}
		if len(result.Traces) > 0 {
			if err := visit(result); err != nil {
				return err
			}
		}
	}
	return nil
}

func scanSeedFilters(q iprojectionsource.Query) []map[string]any {
	filters := []map[string]any{
		{"exists": map[string]any{"field": "receipt_id"}},
		{"exists": map[string]any{"field": "trace_id"}},
		{"exists": map[string]any{"field": "request_id"}},
		{"bool": map[string]any{"must_not": []map[string]any{exactKeywordQuery("trace_id", ""), exactKeywordQuery("request_id", "")}}},
	}
	filters = append(filters, receiptScopeCandidates(q.Scope)...)
	for field, value := range map[string]string{"request_id": q.RequestID, "trace_id": q.TraceID, "interaction_id": q.InteractionID} {
		if value != "" {
			filters = append(filters, exactKeywordQuery(field, value))
		}
	}
	if len(q.TraceIDs) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"trace_id.keyword": q.TraceIDs}})
	}
	if len(q.ConversationIDs) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"conversation_id.keyword": q.ConversationIDs}})
	}
	if len(q.InteractionIDs) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"interaction_id.keyword": q.InteractionIDs}})
	}
	if !q.From.IsZero() || !q.To.IsZero() {
		bounds := map[string]any{}
		if !q.From.IsZero() {
			bounds["gte"] = q.From
		}
		if !q.To.IsZero() {
			bounds["lte"] = q.To
		}
		filters = append(filters, map[string]any{"range": map[string]any{"issued_at": bounds}})
	}
	return filters
}

// A budget applies to the complete result, not one page. Reaching exactly the
// budget requires one final page to prove there are no omitted receipts.
func (s *Source) scanReceiptPages(ctx context.Context, pit string, filters []map[string]any, fields []string, budget int, visit func([]receiptDocument) error) error {
	var after []json.RawMessage
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		body := map[string]any{"size": scanPageSize, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []map[string]any{{"issued_at": map[string]any{"order": "desc"}}, {"receipt_id.keyword": map[string]any{"order": "asc"}}}, "track_total_hits": false}
		if fields != nil {
			body["_source"] = fields
		}
		if len(after) > 0 {
			body["search_after"] = after
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw, err = s.client.SearchPointInTime(ctx, pit, raw)
		if errors.Is(err, opensearch.ErrPointInTimeResponseBudgetExceeded) {
			return iprojectionsource.ErrSummaryScanBudgetExceeded
		}
		if err != nil {
			return err
		}
		var response struct {
			TimedOut *bool `json:"timed_out"`
			Shards   *struct {
				Failed *int `json:"failed"`
			} `json:"_shards"`
			Hits *struct {
				Hits *[]struct {
					Source *receiptDocument  `json:"_source"`
					Sort   []json.RawMessage `json:"sort"`
				} `json:"hits"`
			} `json:"hits"`
		}
		if err = json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode summary scan: %w", err)
		}
		if response.TimedOut == nil || response.Shards == nil || response.Shards.Failed == nil || response.Hits == nil || response.Hits.Hits == nil {
			return fmt.Errorf("summary scan response lacks completeness metadata")
		}
		if *response.TimedOut || *response.Shards.Failed != 0 {
			return fmt.Errorf("summary scan returned incomplete search results")
		}
		hits := *response.Hits.Hits
		if len(hits) == 0 {
			return nil
		}
		count += len(hits)
		if budget > 0 && count > budget {
			return iprojectionsource.ErrSummaryScanBudgetExceeded
		}
		page := make([]receiptDocument, 0, len(hits))
		for _, hit := range hits {
			if hit.Source == nil {
				return fmt.Errorf("summary scan hit lacks receipt source")
			}
			page = append(page, *hit.Source)
		}
		cursor := hits[len(hits)-1].Sort
		if len(cursor) != 2 || string(cursor[0]) == "null" || string(cursor[1]) == "null" {
			return fmt.Errorf("summary scan response lacks stable sort cursor")
		}
		old, _ := json.Marshal(after)
		next, _ := json.Marshal(cursor)
		if string(old) == string(next) {
			return fmt.Errorf("summary scan cursor did not advance")
		}
		if err = visit(page); err != nil {
			return err
		}
		if len(hits) < scanPageSize {
			return nil
		}
		after = cursor
	}
}

func (s *Source) scanAllReceipts(ctx context.Context, pit string, filters []map[string]any) ([]receiptDocument, error) {
	result := make([]receiptDocument, 0)
	filters = append(append([]map[string]any(nil), filters...), map[string]any{"exists": map[string]any{"field": "receipt_id"}})
	err := s.scanReceiptPages(ctx, pit, filters, nil, scanReceiptBudget, func(page []receiptDocument) error {
		for _, r := range page {
			if strings.TrimSpace(r.ReceiptID) == "" {
				return fmt.Errorf("summary scan receipt lacks an ID")
			}
			if _, err := time.Parse(time.RFC3339Nano, r.IssuedAt); err != nil {
				return fmt.Errorf("summary scan receipt has invalid issued_at")
			}
		}
		result = append(result, page...)
		return nil
	})
	return result, err
}

func scanMatchesReceipt(r receiptDocument, q iprojectionsource.Query) bool {
	if r.TraceID == "" || r.RequestID == "" || !matchesReceiptQuery(r, q) {
		return false
	}
	return len(q.InteractionIDs) == 0 || containsProjectionID(q.InteractionIDs, r.InteractionID)
}

func (s *Source) scanTraceBatch(ctx context.Context, pit string, q iprojectionsource.Query, ids []string, enqueue func(string) error) (iprojectionsource.Result, error) {
	raw, err := s.scanAllReceipts(ctx, pit, []map[string]any{{"terms": map[string]any{"trace_id.keyword": ids}}})
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	allowed := make([]receiptDocument, 0, len(raw))
	interactionIDs := map[string]bool{}
	for _, r := range raw {
		if r.InteractionID != "" {
			interactionIDs[r.InteractionID] = true
			continue
		}
		if r.TraceID != "" && r.RequestID != "" && receiptMatchesScope(r, q.Scope) {
			allowed = append(allowed, r)
		}
	}
	authorized := map[string]bool{}
	// Each interaction is expanded completely before its network union is used.
	ordered := make([]string, 0, len(interactionIDs))
	for id := range interactionIDs {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	grouped := map[string][]receiptDocument{}
	if len(ordered) > 0 {
		expanded, e := s.scanAllReceipts(ctx, pit, []map[string]any{{"terms": map[string]any{"interaction_id.keyword": ordered}}})
		if e != nil {
			return iprojectionsource.Result{}, e
		}
		for _, r := range expanded {
			grouped[r.InteractionID] = append(grouped[r.InteractionID], r)
		}
	}
	for _, id := range ordered {
		receipts := grouped[id]
		if !interactionMatchesScope(receipts, q.Scope) {
			continue
		}
		for _, r := range receipts {
			if r.TraceID == "" || r.RequestID == "" {
				continue
			}
			if scanMatchesReceipt(r, q) {
				if e := enqueue(r.TraceID); e != nil {
					return iprojectionsource.Result{}, e
				}
				if selected[r.TraceID] {
					authorized[id] = true
				}
			}
			if selected[r.TraceID] {
				allowed = append(allowed, r)
			}
		}
		if len(allowed) > scanReceiptBudget {
			return iprojectionsource.Result{}, iprojectionsource.ErrSummaryScanBudgetExceeded
		}
	}
	// Determine root over authorized lifetime; only window events feed Status.
	sort.Slice(allowed, func(i, j int) bool {
		a, ae := time.Parse(time.RFC3339Nano, allowed[i].IssuedAt)
		b, be := time.Parse(time.RFC3339Nano, allowed[j].IssuedAt)
		if ae == nil && be == nil && !a.Equal(b) {
			return a.Before(b)
		}
		if (ae != nil || be != nil) && allowed[i].IssuedAt != allowed[j].IssuedAt {
			return allowed[i].IssuedAt < allowed[j].IssuedAt
		}
		return allowed[i].ReceiptID < allowed[j].ReceiptID
	})
	roots := map[string]string{}
	scopes := map[string][]iprojectionsource.CanonicalTraceScope{}
	scopeSeen := map[string]map[iprojectionsource.CanonicalTraceScope]bool{}
	window := make([]receiptDocument, 0, len(allowed))
	seenReceipt := map[string]bool{}
	for _, r := range allowed {
		if seenReceipt[r.ReceiptID] {
			continue
		}
		seenReceipt[r.ReceiptID] = true
		tuple := iprojectionsource.CanonicalTraceScope{ConversationID: r.ConversationID, InteractionID: r.InteractionID}
		if scopeSeen[r.TraceID] == nil {
			scopeSeen[r.TraceID] = map[iprojectionsource.CanonicalTraceScope]bool{}
		}
		if !scopeSeen[r.TraceID][tuple] {
			scopes[r.TraceID] = append(scopes[r.TraceID], tuple)
			scopeSeen[r.TraceID][tuple] = true
		}
		name := strings.TrimSpace(r.ToolName)
		if prior, ok := roots[r.TraceID]; !ok || prior == "" && name != "" {
			roots[r.TraceID] = name
		}
		if scanMatchesReceipt(r, q) {
			window = append(window, r)
		}
	}
	traces := tracesFromReceipts(window)
	_, summaries := evidencevo.BuildExecutionSummaries(traces, nil)
	keep := map[string]bool{}
	for _, summary := range summaries {
		if q.Status != "" && summary.Status != q.Status {
			continue
		}
		if q.Tool != "" && roots[summary.TraceID] != q.Tool {
			continue
		}
		keep[summary.TraceID] = true
	}
	filtered := traces[:0]
	for _, trace := range traces {
		if keep[trace.TraceID] {
			filtered = append(filtered, trace)
		}
	}
	traces = filtered
	for id := range scopes {
		if !keep[id] {
			delete(scopes, id)
			delete(roots, id)
		}
	}
	if len(traces) == 0 {
		return iprojectionsource.Result{}, nil
	}
	legacyIDs := map[string]bool{}
	authorized = map[string]bool{}
	for _, r := range window {
		if !keep[r.TraceID] {
			continue
		}
		if r.InteractionID != "" {
			authorized[r.InteractionID] = true
		}
		if r.InteractionID == "" {
			legacyIDs[r.TraceID] = true
		}
	}
	artifacts, err := s.scanArtifacts(ctx, q, traces, authorized, legacyIDs)
	if err != nil {
		return iprojectionsource.Result{}, err
	}
	artifacts = artifactsForTraces(artifacts, traces)
	attachArtifactEvents(traces, artifacts)
	return iprojectionsource.Result{Traces: traces, Artifacts: artifacts, CanonicalRoots: roots, CanonicalScopes: scopes}, nil
}

func (s *Source) scanArtifacts(ctx context.Context, q iprojectionsource.Query, traces []evidencevo.NormalizedTrace, authorized map[string]bool, legacyIDs map[string]bool) ([]evidencevo.EvidenceArtifact, error) {
	if s.artifacts == nil {
		return nil, nil
	}
	q.TraceID = ""
	q.RequestID = ""
	q.InteractionID = ""
	q.TraceIDs = nil
	q.ConversationIDs = nil
	q.InteractionIDs = nil
	q.AuthorizedInteractionIDs = nil
	q.Limit = scanReceiptBudget
	queries := []iprojectionsource.Query{}
	if len(authorized) > 0 {
		shared := q
		for id := range authorized {
			shared.AuthorizedInteractionIDs = append(shared.AuthorizedInteractionIDs, id)
		}
		sort.Strings(shared.AuthorizedInteractionIDs)
		// No Trace selector: authorized interactions include trace-less terminal artifacts.
		queries = append(queries, shared)
	}
	if len(legacyIDs) > 0 {
		legacy := q
		for id := range legacyIDs {
			legacy.TraceIDs = append(legacy.TraceIDs, id)
		}
		sort.Strings(legacy.TraceIDs)
		queries = append(queries, legacy)
	}
	artifacts := []evidencevo.EvidenceArtifact{}
	for _, aq := range queries {
		if narrow, ok := s.artifacts.(iprojectionsource.ArtifactProjectionSourcePort); ok {
			result, err := narrow.LoadArtifactProjection(ctx, aq)
			if err != nil {
				return nil, err
			}
			if result.Truncated {
				return nil, iprojectionsource.ErrSummaryScanBudgetExceeded
			}
			artifacts = append(artifacts, result.Artifacts...)
		} else {
			result, err := s.artifacts.LoadExecutionProjection(ctx, aq)
			if err != nil {
				return nil, err
			}
			if result.Truncated {
				return nil, iprojectionsource.ErrSummaryScanBudgetExceeded
			}
			artifacts = append(artifacts, result.Artifacts...)
		}
		if len(artifacts) > scanReceiptBudget {
			return nil, iprojectionsource.ErrSummaryScanBudgetExceeded
		}
	}
	return artifacts, nil
}
