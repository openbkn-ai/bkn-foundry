// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
	"time"
)

func TestCanonicalRootToolIsScopeSafeAndIndependentOfFilterOrProjectionCap(t *testing.T) {
	store := &pagingSessionStore{Store: sessionstore.New(), tracePage: isessionstore.SummaryIdentityPage{Entries: []isessionstore.SummaryIdentity{{ID: "trace", StartedAt: "2026-10-23T00:00:00Z"}}, Total: 1}}
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{EffectiveSubjectType: "app", EffectiveSubjectID: "acct_demo"}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		for i := 0; i < 25; i++ {
			tx.SaveReceipt(sessionvo.Receipt{ID: fmt.Sprintf("receipt-%02d", i), Owner: owner, TraceID: "trace", RequestID: "req", ConversationID: fmt.Sprintf("conversation-%d", i%2), ToolName: "child-tool", IssuedAt: at.Add(time.Duration(i) * time.Second)})
		}
		tx.SaveReceipt(sessionvo.Receipt{ID: "receipt-00", Owner: owner, TraceID: "trace", RequestID: "req", ConversationID: "conversation-0", ToolName: "\u3000Root工具\u00a0", IssuedAt: at})
		foreign := owner
		foreign.EffectiveSubjectID = "other"
		tx.SaveReceipt(sessionvo.Receipt{ID: "foreign", Owner: foreign, TraceID: "trace", RequestID: "other-req", ToolName: "secret-tool", IssuedAt: at.Add(-time.Hour)})
		tx.SaveReceipt(sessionvo.Receipt{ID: "unusable", Owner: owner, TraceID: "trace", ToolName: "unusable-tool", IssuedAt: at.Add(-time.Hour)})
		tx.SaveReceipt(sessionvo.Receipt{ID: "blank", Owner: owner, TraceID: "trace", RequestID: "req", ToolName: "\u0085\u2007", IssuedAt: at.Add(-time.Hour)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	trace := pageSummaryTrace("trace", "req", "2026-10-23T00:00:00Z", "acct_demo", "")
	trace.Events[0].OperationName = "child-tool"
	projection := &capturingProjectionSource{result: iprojectionsource.Result{Traces: []evidencevo.NormalizedTrace{trace}, Truncated: true}}
	svc := New(evidencestore.New(), WithSessionStore(store), WithProjectionSource(projection))
	for _, tool := range []string{"", "Root工具"} {
		page, err := svc.ListTraceExecutions(context.Background(), evidencevo.SummaryQueryOptions{Scope: summaryScope("acct_demo"), Tool: tool, From: at.Add(24 * time.Hour), Limit: 20})
		if err != nil || len(page.Entries) != 1 || page.Entries[0].RootOperation != "Root工具" || !page.Partial {
			t.Fatalf("tool=%q page=%+v err=%v", tool, page, err)
		}
	}
}

func TestCanonicalRootToolKeepsSharedNetworkAndMissingCoreFallback(t *testing.T) {
	store := sessionstore.New()
	scope := summaryScope("acct_demo")
	scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "acct_demo", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"kn"}}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveReceipt(sessionvo.Receipt{ID: "root", Owner: sessionvo.Owner{EffectiveSubjectID: "acct_demo"}, TraceID: "trace", RequestID: "req", ToolName: "owned-root"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{sessionStore: store}
	traces := []evidencevo.TraceSummary{{TraceID: "trace", RootOperation: "authorized-shared-root"}, {TraceID: "legacy", RootOperation: "legacy-root"}}
	if err := svc.applyCanonicalTraceIdentity(context.Background(), traces, scope); err != nil {
		t.Fatal(err)
	}
	if traces[0].RootOperation != "authorized-shared-root" || traces[1].RootOperation != "legacy-root" {
		t.Fatalf("fallback metadata changed: %+v", traces)
	}
	if canUseTraceSummaryIdentityPage(evidencevo.SummaryQueryOptions{Scope: scope, Tool: "owned-root"}) {
		t.Fatal("shared network scope must not enter SQL Tool path")
	}
}
