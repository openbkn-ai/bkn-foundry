// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

func TestSpecExactDuplicateMustNotRepublishIntoQueueDrop(t *testing.T) {
	p := expectationTestPublisher(t, 1)
	ctx := withEvidenceOutcome(testTraceContext())
	event := expectationEvent("same")
	if err := SubmitEvents(ctx, nil, nil, []Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitEvents(ctx, nil, nil, []Event{event}); err != nil {
		t.Fatal(err)
	}
	frozen := freezeEvidenceExpectation(ctx)
	if !frozen.Closed || len(frozen.Events) != 1 || frozen.Events[0].PublishDisposition != "accepted" || len(p.SnapshotQueue()) != 1 {
		t.Fatalf("duplicate incorrectly changes confirmation contract: closed=%t count=%d disposition=%s queue=%d", frozen.Closed, len(frozen.Events), frozen.Events[0].PublishDisposition, len(p.SnapshotQueue()))
	}
	if p.NextSequence() != 2 {
		t.Fatal("duplicate advanced producer sequence")
	}
}
func TestSpecSubmissionAfterFreezeMustBeExplicitError(t *testing.T) {
	p := expectationTestPublisher(t, 1)
	ctx := withEvidenceOutcome(testTraceContext())
	freezeEvidenceExpectation(ctx)
	if err := SubmitEvents(ctx, nil, nil, []Event{expectationEvent("late")}); !errors.Is(err, ErrEvidenceExpectationClosed) {
		t.Fatal("late planned event silently discarded as success after closed-empty contract")
	}
	if len(p.SnapshotQueue()) != 0 || len(freezeEvidenceExpectation(ctx).Events) != 0 {
		t.Fatal("late submission changed frozen evidence")
	}
}

func TestEvidenceDuplicateWithAvailableQueueDoesNotRepublish(t *testing.T) {
	p := expectationTestPublisher(t, 4)
	ctx := withEvidenceOutcome(testTraceContext())
	if err := SubmitEvents(ctx, nil, nil, []Event{expectationEvent("same"), expectationEvent("same")}); err != nil {
		t.Fatal(err)
	}
	if len(p.SnapshotQueue()) != 1 || p.NextSequence() != 2 || !freezeEvidenceExpectation(ctx).Closed {
		t.Fatal("same batch duplicate published twice")
	}
}

func TestEvidenceFirstDroppedDecisionCannotBecomeLateAccepted(t *testing.T) {
	p := expectationTestPublisher(t, 1)
	if err := SubmitEvents(testTraceContext(), nil, nil, []Event{expectationEvent("filler")}); err != nil {
		t.Fatal(err)
	}
	ctx := withEvidenceOutcome(testTraceContext())
	if err := SubmitEvents(ctx, nil, nil, []Event{expectationEvent("same")}); err != nil {
		t.Fatal(err)
	}
	p.Flush(context.Background())
	if err := SubmitEvents(ctx, nil, nil, []Event{expectationEvent("same")}); err != nil {
		t.Fatal(err)
	}
	e := freezeEvidenceExpectation(ctx)
	if len(p.SnapshotQueue()) != 0 || p.NextSequence() != 2 || !e.Closed || e.Events[0].DropReason != "queue_full" || evidenceExpectationDurability(e) != "failed" {
		t.Fatalf("first dropped decision was changed: %+v", e)
	}
}

type blockedEvidencePublisher struct {
	underlying       *evidencepublisher.Publisher
	entered, release chan struct{}
	count            int
}

func (p *blockedEvidencePublisher) TryPublish(event evidencepublisher.Event) evidencepublisher.PublishResult {
	p.count++
	if p.count == 2 {
		close(p.entered)
		<-p.release
	}
	return p.underlying.TryPublish(event)
}

func TestEvidenceFreezeWaitsForEntireAdmittedBatch(t *testing.T) {
	p := expectationTestPublisher(t, 4)
	blocking := &blockedEvidencePublisher{underlying: p, entered: make(chan struct{}), release: make(chan struct{})}
	SetEvidencePublisher(blocking)
	ctx := withEvidenceOutcome(testTraceContext())
	submitted := make(chan error, 1)
	go func() {
		submitted <- SubmitEvents(ctx, nil, nil, []Event{expectationEvent("one"), expectationEvent("two")})
	}()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("batch did not reach second publish")
	}
	frozen := make(chan *EvidenceExpectation, 1)
	go func() { frozen <- freezeEvidenceExpectation(ctx) }()
	select {
	case <-frozen:
		close(blocking.release)
		t.Fatal("freeze passed unfinished batch")
	case <-time.After(20 * time.Millisecond):
	}
	close(blocking.release)
	if err := <-submitted; err != nil {
		t.Fatal(err)
	}
	e := <-frozen
	if !e.Closed || len(e.Events) != 2 || len(p.SnapshotQueue()) != 2 {
		t.Fatalf("freeze missed batch events: %+v", e)
	}
}

func TestSpecHealthyNestedGuardDoesNotSpendBudgetOnBusinessTime(t *testing.T) {
	client := lifecycleClientWithTransport(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if strings.HasSuffix(req.URL.Path, "/int-1") {
			return lifecycleJSONResponse(200, Interaction{InteractionID: "int-1", ConversationID: "conv-1", ExecutionStatus: "active", LeaseToken: "lease-1", LeaseEpoch: 1}), nil
		}
		return lifecycleJSONResponse(200, pendingGuardState().Result), nil
	})
	// Ensure response must authorize actual GuardExecute.
	guard := NewGuard(client)
	intent := GuardIntent{Context: BusinessContext{ConversationID: "conv-1", InteractionID: "int-1", OperationKey: "outer"}, ToolName: "search_schema", Protocol: "mcp", SourceModule: "context-loader"}
	outer, state, disposition, api, err := guard.Begin(testTraceContext(), intent)
	if err != nil || api != nil || disposition != GuardExecute {
		t.Fatalf("healthy outer begin failed: disposition=%s api=%+v err=%v", disposition, api, err)
	}
	if _, ok := outer.Deadline(); ok {
		t.Fatal("Trace deadline leaked into business context")
	}
	time.Sleep(tracePhaseBudget + 100*time.Millisecond)
	if outer.Err() != nil {
		t.Fatalf("business time canceled outer: %v", outer.Err())
	}
	intent.Context.OperationKey = "child"
	child, childState, disposition, api, err := guard.Begin(outer, intent)
	if err != nil || api != nil || disposition != GuardExecute {
		t.Fatalf("healthy child begin after business failed: disposition=%s api=%+v err=%v", disposition, api, err)
	}
	for _, item := range []struct {
		ctx   context.Context
		state GuardState
	}{{child, childState}, {outer, state}} {
		if _, api, err := guard.Finish(item.ctx, item.state, map[string]any{"ok": true}, false, false); err != nil || api != nil {
			t.Fatalf("healthy nested finish failed api=%+v err=%v", api, err)
		}
	}
}
