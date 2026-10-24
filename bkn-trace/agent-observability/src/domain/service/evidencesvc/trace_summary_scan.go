// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

const maxScannedSummaryPageRetention = 10000

func needsTraceSummaryScan(o evidencevo.SummaryQueryOptions) bool {
	shared := o.Scope.AccessProfile != nil && evidencevo.NeedsCrossAccountCandidates(o.Scope) && !evidencevo.HasGlobalTraceAccess(*o.Scope.AccessProfile)
	return (o.Status != "" || o.ErrorKeyword != "" || shared) &&
		o.TraceID == "" && o.ConversationID == "" && o.InteractionID == "" &&
		o.AgentOrApp == "" && o.Keyword == "" && o.KnowledgeNetwork == "" &&
		o.RecordIntegrity == "" && o.EvidenceCompleteness == "" &&
		o.ExcludeAgentOrApp == "" && len(o.ExcludeAgentOrApps) == 0 &&
		(o.Scope.View == "" || o.Scope.View == evidencevo.AccessViewBusiness || o.Scope.View == evidencevo.AccessViewTechnical)
}

func (s *Service) listScannedTraceSummaryPage(ctx context.Context, o evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, bool, error) {
	source, ok := s.projectionSource.(iprojectionsource.TraceSummaryScanSourcePort)
	if !ok || !needsTraceSummaryScan(o) {
		return evidencevo.TraceSummaryPage{}, false, nil
	}
	empty := evidencevo.TraceSummaryPage{Entries: []evidencevo.TraceSummary{}, Page: normalizeSummaryPage(o.Page), PageSize: normalizeSummaryLimit(o.Limit)}
	if !trustedQueryScope(o.Scope) || o.Scope.AccessProfile != nil && (!o.Scope.AccessProfile.AccountActive || !summaryScopeMatchesProfile(o.Scope)) {
		return empty, true, nil
	}
	cursor, hasCursor, err := decodeSummaryCursor(o.Cursor)
	if err != nil {
		return empty, true, err
	}
	binding := traceScanBinding(o)
	if hasCursor && cursor.ScanBinding != binding {
		return empty, true, ErrSummaryCursorInvalid
	}
	offset := summaryQueryOffset(o)
	if hasCursor {
		offset = 0
	}
	limit := normalizeSummaryLimit(o.Limit)
	if offset > maxScannedSummaryPageRetention-limit-1 {
		return empty, true, iprojectionsource.ErrSummaryScanBudgetExceeded
	}
	retain := offset + limit + 1
	selected := &scannedTraceHeap{}
	heap.Init(selected)
	total := 0
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status := o.Status
	if status == "" && strings.TrimSpace(o.ErrorKeyword) != "" {
		status = "error"
	}
	err = source.ScanTraceSummaryProjection(ctx, iprojectionsource.Query{Scope: o.Scope, From: o.From, To: o.To, Status: status, Tool: o.Tool, SummaryOnly: true}, func(result iprojectionsource.Result) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.Truncated {
			return iprojectionsource.ErrSummaryScanBudgetExceeded
		}
		_, traces := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
		if len(traces) > 0 && s.sessionStore != nil && result.CanonicalScopes == nil {
			return ErrSummaryProjectionLag
		}
		if s.sessionStore != nil {
			for _, trace := range traces {
				if len(result.CanonicalScopes[trace.TraceID]) == 0 {
					return ErrSummaryProjectionLag
				}
			}
		}
		// Scanner membership and roots share one complete authorized receipt PIT.
		// Archive purge can remove SQL receipts without removing this projection;
		// SQL identity-page race guards therefore do not apply to this path.
		if err := s.applyTraceMetadata(ctx, traces, o.Scope, false, result.CanonicalScopes); err != nil {
			return err
		}
		for _, trace := range traces {
			root, found := result.CanonicalRoots[trace.TraceID]
			if !found {
				return ErrSummaryProjectionLag
			}
			trace.RootOperation = root
			if !matchesTraceFilters(trace, o) {
				continue
			}
			total++
			if hasCursor && !traceScanAfter(trace, cursor) {
				continue
			}
			if selected.Len() < retain {
				heap.Push(selected, trace)
			} else if traceScanBefore(trace, (*selected)[0]) {
				heap.Pop(selected)
				heap.Push(selected, trace)
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.Join(iprojectionsource.ErrSummaryScanBudgetExceeded, err)
		}
		return empty, true, err
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.Join(iprojectionsource.ErrSummaryScanBudgetExceeded, err)
		}
		return empty, true, err
	}
	entries := []evidencevo.TraceSummary(*selected)
	sort.Slice(entries, func(i, j int) bool { return traceScanBefore(entries[i], entries[j]) })
	if offset > len(entries) {
		offset = len(entries)
	}
	end := min(offset+limit, len(entries))
	empty.Entries = append(empty.Entries, entries[offset:end]...)
	empty.Total = total
	if end < len(entries) && len(empty.Entries) > 0 {
		last := empty.Entries[len(empty.Entries)-1]
		next := encodeSummaryCursor(summaryCursor{StartedAt: last.StartedAt, ID: last.TraceID, ScanBinding: binding})
		empty.NextCursor = &next
	}
	// Discarded candidates need no span-count read. Only enrich the returned page.
	// The legacy statistics port counts the global TraceID namespace and has
	// no owner/interaction constraint. A receipt grant cannot authorize spans
	// belonging to another account with the same TraceID. Keep unavailable
	// until an explicitly scoped statistics capability exists.
	if o.Scope.AccessProfile != nil && evidencevo.HasGlobalTraceAccess(*o.Scope.AccessProfile) {
		s.enrichTraceStats(ctx, empty.Entries)
	}
	return empty, true, nil
}

func traceScanBinding(o evidencevo.SummaryQueryOptions) string {
	o.Cursor = ""
	o.Page = 0
	o.Limit = 0
	// Display-only identity snapshots are not permission inputs.
	if o.Scope.AccessProfile != nil {
		p := *o.Scope.AccessProfile
		p.ActorNameSnapshot = ""
		o.Scope.AccessProfile = &p
	}
	// QueryScope intentionally excludes View and AccessProfile from JSON. Bind
	// them explicitly, otherwise a cursor could survive a grant/view change.
	raw, _ := json.Marshal(struct {
		Options evidencevo.SummaryQueryOptions
		View    evidencevo.AccessView
		Profile *evidencevo.AccessProfile
	}{o, o.Scope.View, o.Scope.AccessProfile})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func traceScanBefore(a, b evidencevo.TraceSummary) bool {
	x, xe := time.Parse(time.RFC3339Nano, a.StartedAt)
	y, ye := time.Parse(time.RFC3339Nano, b.StartedAt)
	if xe == nil && ye == nil {
		if !x.Equal(y) {
			return x.After(y)
		}
	} else if a.StartedAt != b.StartedAt {
		return a.StartedAt > b.StartedAt
	}
	return a.TraceID < b.TraceID
}
func traceScanAfter(a evidencevo.TraceSummary, c summaryCursor) bool {
	return traceScanBefore(evidencevo.TraceSummary{TraceID: c.ID, StartedAt: c.StartedAt}, a)
}

type scannedTraceHeap []evidencevo.TraceSummary

func (h scannedTraceHeap) Len() int           { return len(h) }
func (h scannedTraceHeap) Less(i, j int) bool { return traceScanBefore(h[j], h[i]) }
func (h scannedTraceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *scannedTraceHeap) Push(x any)        { *h = append(*h, x.(evidencevo.TraceSummary)) }
func (h *scannedTraceHeap) Pop() any          { v := *h; x := v[len(v)-1]; *h = v[:len(v)-1]; return x }
