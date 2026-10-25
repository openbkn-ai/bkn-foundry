// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
package evidencesvc

import (
	"context"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
)

type archiveScanSource struct {
	remainingFilterSource
	missingRoot, missingScope bool
}

func (s *archiveScanSource) ScanTraceSummaryProjection(_ context.Context, _ iprojectionsource.Query, visit func(iprojectionsource.Result) error) error {
	id := s.traces[0].TraceID
	roots := map[string]string{id: "snapshot-root"}
	scopes := map[string][]iprojectionsource.CanonicalTraceScope{id: {{ConversationID: "authorized", InteractionID: "archived"}}}
	if s.missingRoot {
		delete(roots, id)
	}
	if s.missingScope {
		delete(scopes, id)
	}
	return visit(iprojectionsource.Result{Traces: s.traces, CanonicalRoots: roots, CanonicalScopes: scopes})
}

// Archive purge removes SQL receipts but retains the receipt projection.
func TestScannedToolUsesSnapshotRootAfterArchive(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, keyword := range []bool{false, true} {
			for _, differentSQL := range []bool{false, true} {
				t.Run(fmt.Sprintf("admin=%v/keyword=%v/differentSQL=%v", admin, keyword, differentSQL), func(t *testing.T) {
					traces := remainingFilterTraces(1)
					traces[0].Events[0].Payload["status"] = "failed"
					traces[0].Events[0].Payload["safe_error_summary"] = "archived-error"
					traces[0].Events[0].OperationName = "window-child"
					source := &archiveScanSource{remainingFilterSource: remainingFilterSource{traces: traces}}
					store := sessionstore.New()
					if differentSQL {
						if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
							tx.SaveReceipt(sessionvo.Receipt{ID: "new-root", Owner: sessionvo.Owner{EffectiveSubjectType: "app", EffectiveSubjectID: "acct_demo"}, TraceID: traces[0].TraceID, RequestID: "later", ToolName: "sql-other-root"})
							return nil
						}); err != nil {
							t.Fatal(err)
						}
					}
					svc := New(evidencestore.New(), WithProjectionSource(source), WithSessionStore(store))
					o := evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Tool: "snapshot-root", Status: "error"}
					if admin {
						o.Scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Roles: []string{"admin"}}
					}
					if keyword {
						o.Status = ""
						o.ErrorKeyword = "archived-error"
					}
					page, err := svc.ListTraceExecutions(context.Background(), o)
					if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].RootOperation != "snapshot-root" {
						t.Fatalf("snapshot lost total=%d entries=%d err=%v", page.Total, len(page.Entries), err)
					}
					for _, missing := range []string{"root", "scope"} {
						source.missingRoot = missing == "root"
						source.missingScope = missing == "scope"
						p, e := svc.ListTraceExecutions(context.Background(), o)
						if !errors.Is(e, ErrSummaryProjectionLag) || p.Total != 0 || len(p.Entries) != 0 {
							t.Fatalf("missing %s not failclosed: %+v %v", missing, p, e)
						}
					}
				})
			}
		}
	}
}

func TestScannedDeadlinePreservesCause(t *testing.T) {
	source := &remainingFilterSource{traces: remainingFilterTraces(1), err: context.DeadlineExceeded}
	svc := New(evidencestore.New(), WithProjectionSource(source))
	page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Status: "completed"})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, iprojectionsource.ErrSummaryScanBudgetExceeded) || page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("deadline cause or atomic failure lost: %+v %v", page, err)
	}
}
