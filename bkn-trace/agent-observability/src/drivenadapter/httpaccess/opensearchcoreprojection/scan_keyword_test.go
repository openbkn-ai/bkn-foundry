// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchcoreprojection

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/evidencesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

// Exercise the public service through the production PIT scanner, including
// multi-page discovery, receipt expansion, time matching and owner isolation.
func TestTraceKeywordPITFindsHistoricalCandidateAndRejectsForeignCollision(t *testing.T) {
	f := &scanFixture{}
	for i := 0; i < evidencesvc.MaxSummaryScanEntries; i++ {
		r := scanReceipt(fmt.Sprintf("new-%04d", i), fmt.Sprintf("trace-%04d", i), "", "owner", "ordinary", "2026-10-29T00:00:00Z")
		r.Status = "completed"
		f.seeds = append(f.seeds, r)
		f.receipts = append(f.receipts, r)
	}
	old := scanReceipt("old", "historical-trace", "", "owner", "historical-tool", "2026-10-22T02:19:37.537468Z")
	old.Status = "completed"
	f.seeds = append(f.seeds, old)
	f.receipts = append(f.receipts, old)
	// TraceID collision must not supply another owner's root or timestamp.
	foreign := scanReceipt("foreign", old.TraceID, "private-interaction", "other", "private-marker", "2020-01-01T00:00:00Z")
	f.receipts = append(f.receipts, foreign)
	foreignOnly := scanReceipt("foreign-only", "private-marker", "", "other", "private-marker", old.IssuedAt)
	f.seeds = append(f.seeds, foreignOnly)
	f.receipts = append(f.receipts, foreignOnly)
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(scanSource(t, f)))
	opts := evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user", View: evidencevo.AccessViewTechnical}, Keyword: "ICAL-TRA", Limit: 1}
	wide, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || wide.Total != 1 || len(wide.Entries) != 1 || wide.Partial || wide.Truncated || wide.NextCursor != nil {
		t.Fatalf("historical keyword lost: %+v err=%v", wide, err)
	}
	if got := wide.Entries[0]; got.TraceID != old.TraceID || got.RootOperation != old.ToolName || got.StartedAt != old.IssuedAt {
		t.Fatalf("foreign collision changed visible summary: %+v", got)
	}
	if f.seedPages <= 1 || strings.Contains(fmt.Sprint(f.seedBody), "range:") {
		t.Fatalf("wide query must traverse candidate pages without hidden time bounds: pages=%d body=%v", f.seedPages, f.seedBody)
	}
	opts.From, _ = time.Parse(time.RFC3339, "2026-10-22T02:19:37Z")
	opts.To, _ = time.Parse(time.RFC3339, "2026-10-22T02:19:38Z")
	narrow, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || narrow.Total != wide.Total || len(narrow.Entries) != 1 || narrow.Entries[0].TraceID != old.TraceID || narrow.Partial || narrow.Truncated {
		t.Fatalf("wide/narrow keyword disagreement: %+v err=%v", narrow, err)
	}
	if !strings.Contains(fmt.Sprint(f.seedBody), "issued_at") {
		t.Fatalf("explicit time range was not passed to discovery: %v", f.seedBody)
	}
	opts.From, opts.To = time.Time{}, time.Time{}
	opts.Keyword = "private-marker"
	denied, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || denied.Total != 0 || len(denied.Entries) != 0 || denied.Partial || denied.Truncated || denied.NextCursor != nil {
		t.Fatalf("foreign candidate or root leaked through keyword: %+v err=%v", denied, err)
	}
	if f.opened != 3 || f.closed != f.opened {
		t.Fatalf("PIT lifecycle mismatch: opened=%d closed=%d", f.opened, f.closed)
	}
}

func TestTraceKeywordPITPreservesPreviewsWithoutSearchingTheirBodies(t *testing.T) {
	r := scanReceipt("r", "t", "i", "owner", "root-tool", "2026-10-23T00:00:00Z")
	r.Status = "completed"
	f := &scanFixture{seeds: []receiptDocument{r}, receipts: []receiptDocument{r}}
	source := scanSource(t, f)
	artifacts := &scanArtifactSource{artifactResult: iprojectionsource.ArtifactResult{Artifacts: []evidencevo.EvidenceArtifact{
		{ArtifactID: "q", TraceID: "t", RequestID: r.RequestID, InteractionID: "i", ArtifactType: evidencevo.ArtifactTypeQuestion, AgentOrApp: "recorded-agent", ObservedAt: r.IssuedAt, Content: "question-body-marker"},
		{ArtifactID: "a", TraceID: "t", RequestID: r.RequestID, InteractionID: "i", ArtifactType: evidencevo.ArtifactTypeResult, ObservedAt: r.IssuedAt, Content: "answer-body-marker"},
	}}}
	source.artifacts = artifacts
	svc := evidencesvc.New(evidencestore.New(), evidencesvc.WithProjectionSource(source))
	opts := evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, Keyword: "RECORDED-AGENT", Limit: 1}
	page, err := svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].QuestionPreview != "question-body-marker" || page.Entries[0].ResultPreview != "answer-body-marker" {
		t.Fatalf("artifact identity/preview changed: %+v err=%v", page, err)
	}
	if len(artifacts.artifactProjectionQueries) != 1 || !artifacts.artifactProjectionQueries[0].SummaryOnly || artifacts.artifactProjectionQueries[0].OmitSummaryTerminalContent {
		t.Fatalf("lost summary preview contract: %+v", artifacts.artifactProjectionQueries)
	}
	opts.Keyword = "body-marker"
	page, err = svc.ListTraceExecutions(context.Background(), opts)
	if err != nil || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("keyword searched question/answer bodies: %+v err=%v", page, err)
	}
}
