// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	memorystore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func integrityFixture() (sessionvo.EvidenceSnapshot, sessionvo.Owner, time.Time) {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	owner := sessionvo.Owner{ApplicationPrincipalID: "app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user"}
	input, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"supply","ot_id":"bom"}`))
	output, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"rows":[]}`))
	return sessionvo.EvidenceSnapshot{
		Interaction: sessionvo.EvidenceInteraction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, TerminalAt: &now},
		Operations:  []sessionvo.Operation{{ID: "op", InteractionID: "int", ConversationID: "conv", Attempt: 1}},
		CallFacts:   []sessionvo.OperationCallFact{{OperationID: "op", InteractionID: "int", ConversationID: "conv", Attempt: 1, ReceiptID: "rcpt", ToolName: "query_object_instance", Status: sessionvo.AttemptCompleted, Input: input, Output: &output, RequestID: "req", TraceID: "trace"}},
		Receipts:    []sessionvo.Receipt{{ID: "rcpt", OperationID: "op", InteractionID: "int", ConversationID: "conv", Attempt: 1, Owner: owner, Status: sessionvo.ReceiptCompleted, EvidenceDurability: sessionvo.DurabilityPending, Required: true, RequestID: "req", TraceID: "trace"}},
		Ledger:      &sessionvo.EvidenceLedgerSnapshot{Events: []sessionvo.EvidenceLedgerRecord{}},
	}, owner, now
}

func TestRecordIntegrityUsesStoredEvidenceNotPendingFlag(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.CallFacts[0].CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "ontology_result/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}
	snapshot.Receipts[0].BusinessRefs = []sessionvo.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "object_type", RefID: "object:supply:bom", Version: "unversioned"}}
	event := ledgervo.Event{EventID: "evt", EventType: "retrieval.completed", Owner: owner, ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, RequestID: "req", TraceID: "trace", Envelope: json.RawMessage(`{"payload":{"truncated":true}}`)}
	raw, _ := json.Marshal(event)
	snapshot.Ledger.Events = []sessionvo.EvidenceLedgerRecord{{EventID: "evt", Envelope: raw}}
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("durable event with accurately recorded partial result must be complete: %+v %v", report, err)
	}
	event.Attempt = 2
	raw, _ = json.Marshal(event)
	snapshot.Ledger.Events[0].Envelope = raw
	report, err = evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report.Status != "missing" {
		t.Fatalf("another attempt must not satisfy evidence: %+v %v", report, err)
	}
}

func TestRecordIntegrityFailedCallNeedsErrorNotSuccessEvidence(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.Interaction.ExecutionStatus = sessionvo.InteractionFailed
	fact := &snapshot.CallFacts[0]
	fact.Status = sessionvo.AttemptFailed
	fact.Error, fact.Output = fact.Output, nil
	fact.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "ontology_result/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}
	snapshot.Receipts[0].Status = sessionvo.ReceiptFailed
	snapshot.Receipts[0].BusinessRefs = []sessionvo.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}, {RefType: "object_type", RefID: "object:supply:bom", Version: "unversioned"}}
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report.Status != "complete" {
		t.Fatalf("fully recorded failed call must be complete: %+v %v", report, err)
	}
	snapshot.Receipts[0].BusinessRefs = nil
	report, err = evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report.Status != "missing" || report.Missing[0].Reason != "business_target_missing" {
		t.Fatalf("BOM request target was not recorded: %+v %v", report, err)
	}
}

func TestRecordIntegrityChecksRegisteredCallsAndCancellation(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.Receipts = nil
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report.Status != "missing" {
		t.Fatalf("receipt loss must not shrink expectations: %+v %v", report, err)
	}
	snapshot, owner, now = integrityFixture()
	snapshot.Interaction.ExecutionStatus = sessionvo.InteractionCanceled
	snapshot.CallFacts[0].Status = sessionvo.AttemptPending
	snapshot.CallFacts[0].Output = nil
	snapshot.Receipts[0].Status = sessionvo.ReceiptPending
	report, err = evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report.Status != "missing" || report.Missing[0].Reason != "call_outcome_missing" {
		t.Fatalf("interaction cancellation cannot invent a call outcome: %+v %v", report, err)
	}
}

func TestRecordIntegrityConvergenceAndReadErrors(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	deadline := now.Add(time.Minute)
	snapshot.Interaction.ClosureManifest = &sessionvo.ClosureManifest{AssemblerDeadline: &deadline}
	snapshot.CallFacts[0].Output = nil
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report != nil {
		t.Fatalf("incomplete inside existing convergence window is not a final verdict: %+v %v", report, err)
	}
	report, err = evaluateRecordIntegrity(snapshot, owner, deadline, nil)
	if err != nil || report.Status != "missing" {
		t.Fatalf("deadline must inspect actual missing output: %+v %v", report, err)
	}
	snapshot, owner, now = integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:result"}
	_, err = evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err == nil {
		t.Fatal("unavailable artifact verifier must not certify complete or report missing")
	}
}

type integritySnapshotStore struct {
	*memorystore.Store
	snapshot  sessionvo.EvidenceSnapshot
	readErr   error
	snapshots map[string]sessionvo.EvidenceSnapshot
	reads     int
}

func (s *integritySnapshotStore) ReadEvidenceSnapshot(_ context.Context, id string) (sessionvo.EvidenceSnapshot, bool, error) {
	s.reads++
	if snapshot, ok := s.snapshots[id]; ok {
		return snapshot, true, s.readErr
	}
	return s.snapshot, true, s.readErr
}
func TestRecordIntegritySummaryUsesCurrentCheckWithoutRewritingAssembly(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}
	ctx := context.Background()
	err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, EvidenceStatus: sessionvo.EvidencePartial, CreatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	probe, found, err := service.GetInteractionSummaryWithoutRecordIntegrity(ctx, "int", scope)
	if err != nil || !found || probe.CurrentRecordIntegrity != nil || probe.RecordIntegrityCheckFailed || store.reads != 0 {
		t.Fatalf("authorization summary must retain entry without reading call snapshot: %+v %v reads=%d", probe, err, store.reads)
	}

	summary, found, err := service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || summary.CurrentRecordIntegrity == nil || summary.CurrentRecordIntegrity.Status != "complete" {
		t.Fatalf("summary must include independent current result: %+v %v", summary, err)
	}
	if summary.EvidenceCompleteness != "partial" {
		t.Fatal("historical assembly state must not be rewritten")
	}
	store.readErr = errors.New("database read failed")
	summary, found, err = service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || summary.CurrentRecordIntegrity != nil || !summary.RecordIntegrityCheckFailed {
		t.Fatalf("check failure must preserve facts without a verdict: %+v %v", summary, err)
	}
}

func TestRecordIntegrityFiltersUseCurrentResult(t *testing.T) {
	report := &evidencevo.RecordIntegrity{Status: "complete"}
	entries := []evidencevo.ConversationSummary{{ConversationID: "conv", EvidenceCompleteness: "partial", CurrentRecordIntegrity: report}}
	if got := filterConversationSummaries(entries, evidencevo.SummaryQueryOptions{RecordIntegrity: "complete"}); len(got) != 1 {
		t.Fatal("current complete must match independently of historical partial")
	}
	entry := evidencevo.InteractionListSummary{EvidenceCompleteness: "partial", CurrentRecordIntegrity: report}
	if !matchesInteractionSummaryFilters(entry, evidencevo.SummaryQueryOptions{RecordIntegrity: "complete"}) || matchesInteractionSummaryFilters(entry, evidencevo.SummaryQueryOptions{RecordIntegrity: "missing"}) {
		t.Fatal("interaction integrity filter must use current result")
	}
}

func TestRecordIntegrityReadyRetryAndExpectedReceipt(t *testing.T) {
	s, o, n := integrityFixture()
	s.Operations[0].Attempt = 2
	s.Operations[0].AttemptStatus = sessionvo.AttemptReady
	r, err := evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "complete" {
		t.Fatalf("unclaimed retry is not a missing call: %+v %v", r, err)
	}
	s.Interaction.ClosureManifest = &sessionvo.ClosureManifest{ExpectedReceipts: []sessionvo.ExpectedReceipt{{ReceiptID: "lost", Required: true}}}
	r, err = evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "missing" {
		t.Fatalf("explicit expected receipt must be checked: %+v %v", r, err)
	}
}
func TestRecordIntegrityTargetIsSpecificAndRejectedInputHasNoInventedTarget(t *testing.T) {
	s, o, n := integrityFixture()
	f := &s.CallFacts[0]
	f.Status = sessionvo.AttemptFailed
	f.Error = f.Output
	f.Output = nil
	f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", RequiredTraceFields: []string{"business_refs"}}
	s.Receipts[0].Status = sessionvo.ReceiptFailed
	s.Receipts[0].BusinessRefs = []sessionvo.BusinessRef{{RefType: "knowledge_network", RefID: "kn:supply", Version: "unversioned"}}
	r, err := evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "missing" {
		t.Fatalf("network alone does not record object target: %+v %v", r, err)
	}
	f.Input, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"ot_id":17}`))
	e, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"stage":"input_validation","code":"invalid_arguments"}`))
	f.Error = &e
	s.Receipts[0].BusinessRefs = nil
	r, err = evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "complete" {
		t.Fatalf("rejected input has no known valid target: %+v %v", r, err)
	}
}
func TestRecordIntegrityManagedFunctionUsesRecordedExecution(t *testing.T) {
	s, o, n := integrityFixture()
	s.CallFacts[0].ToolName = "function"
	s.CallFacts[0].CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "managed_function_execution/v1", RequiredTraceFields: []string{"result_completeness"}}
	r, err := evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "complete" {
		t.Fatalf("function contract must not require a query event: %+v %v", r, err)
	}
}

func TestRecordIntegrityHeaderNetworkDoesNotEraseKnownObjectTarget(t *testing.T) {
	s, o, n := integrityFixture()
	f := &s.CallFacts[0]
	f.Input, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"ot_id":"bom"}`))
	f.Status = sessionvo.AttemptFailed
	f.Error = f.Output
	f.Output = nil
	f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", RequiredTraceFields: []string{"business_refs"}}
	s.Receipts[0].Status = sessionvo.ReceiptFailed
	r, err := evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "missing" {
		t.Fatalf("header-only network cannot excuse known object target: %+v %v", r, err)
	}
}

type integrityArtifacts struct {
	iartifactstore.ArtifactStorePort
	result iartifactstore.CaptureReadResult
}

func (a integrityArtifacts) ReadArtifactForCapture(context.Context, string, evidencevo.QueryScope, int64) (iartifactstore.CaptureReadResult, error) {
	return a.result, nil
}
func TestRecordIntegrityReferencedPayloadAbsenceAndVisibility(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"}
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}
	ctx := context.Background()
	err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := captureFixture("a", "rows")
	artifact.InteractionID = "int"
	artifact.OperationID = "op"
	artifact.RequestID = "req"
	artifact.TraceID = "trace"
	artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
	for _, test := range []struct {
		name   string
		result iartifactstore.CaptureReadResult
		status string
		failed bool
	}{
		{"stored", iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}, "complete", false},
		{"absent", iartifactstore.CaptureReadResult{}, "missing", false},
		{"inaccessible", iartifactstore.CaptureReadResult{Exists: true}, "", true},
		{"missing-content", iartifactstore.CaptureReadResult{Artifact: func() evidencevo.EvidenceArtifact { a := artifact; a.Content = nil; return a }(), Found: true, Exists: true}, "missing", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
			service.artifactStore = integrityArtifacts{result: test.result}
			summary, found, err := service.GetInteractionSummary(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
			if err != nil || !found || summary.RecordIntegrityCheckFailed != test.failed {
				t.Fatalf("summary: %+v %v", summary, err)
			}
			if test.status != "" && (summary.CurrentRecordIntegrity == nil || summary.CurrentRecordIntegrity.Status != test.status) {
				t.Fatalf("verdict: %+v", summary.CurrentRecordIntegrity)
			}
		})
	}
	artifact.OperationID = "another-op"
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Found: true, Exists: true, Artifact: artifact}}
	summary, _, _ := service.GetInteractionSummary(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if summary.CurrentRecordIntegrity != nil || !summary.RecordIntegrityCheckFailed {
		t.Fatal("another operation must not certify this payload")
	}
}

func TestRecordIntegrityExplicitInputRejectionAndLostInput(t *testing.T) {
	s, o, n := integrityFixture()
	f := &s.CallFacts[0]
	f.Status = sessionvo.AttemptFailed
	f.Output = nil
	e, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"stage":"input_validation","code":"invalid_condition"}`))
	f.Error = &e
	f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", RequiredTraceFields: []string{"business_refs"}}
	s.Receipts[0].Status = sessionvo.ReceiptFailed
	r, err := evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r.Status != "complete" {
		t.Fatalf("explicit rejection cannot require validated target: %+v %v", r, err)
	}
	f.Input = sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadOmitted, OmittedReason: sessionvo.PayloadOmittedReasonArtifact}
	e, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"stage":"tool_execution","code":"downstream_error"}`))
	f.Error = &e
	r, err = evaluateRecordIntegrity(s, o, n, nil)
	if err != nil || r == nil || r.Status != "missing" || len(r.Missing) != 1 || r.Missing[0].Field != "input" {
		t.Fatalf("known lost input must survive target check: %+v %v", r, err)
	}
}

type integrityCandidateStore struct {
	*integritySnapshotStore
	queries []isessionstore.SummaryPageQuery
}

func (s *integrityCandidateStore) ListTraceSummaryIdentities(context.Context, isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	return isessionstore.SummaryIdentityPage{}, nil
}
func (s *integrityCandidateStore) ListConversationSummaryIdentities(_ context.Context, q isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	s.queries = append(s.queries, q)
	if !q.IncludeRegisteredCalls {
		return isessionstore.SummaryIdentityPage{}, nil
	}
	return isessionstore.SummaryIdentityPage{Entries: []isessionstore.SummaryIdentity{{ID: "conv"}}, Total: 1}, nil
}
func TestRecordIntegrityListSurvivesAllReceiptAndProjectionLoss(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.Receipts = nil
	store := &integrityCandidateStore{integritySnapshotStore: &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}}
	ctx := context.Background()
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner, Status: sessionvo.ConversationClosed, CreatedAt: now})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now, TerminalAt: &now})
		tx.SaveOperation(snapshot.Operations[0])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := New(evidencestore.New(), WithSessionStore(store), WithProjectionSource(&capturingProjectionSource{}), WithCurrentRecordIntegrity())
	for _, filter := range []string{"", "missing"} {
		page, err := service.ListConversations(ctx, evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "user", AccountType: "user"}, RecordIntegrity: filter})
		if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].CurrentRecordIntegrity == nil || page.Entries[0].CurrentRecordIntegrity.Status != "missing" {
			t.Fatalf("filter %q lost registered call: %+v %v", filter, page, err)
		}
	}

	for _, options := range []evidencevo.SummaryQueryOptions{{Keyword: "absent"}, {AgentOrApp: "other"}, {TraceID: "other"}, {ConversationID: "other"}, {InteractionID: "other"}} {
		options.Scope = evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
		options.RecordIntegrity = "missing"
		page, err := service.ListConversations(ctx, options)
		if err != nil || len(page.Entries) != 0 || page.Total != 0 {
			t.Fatalf("content filter must not reintroduce excluded conversation: %+v %+v %v", options, page, err)
		}
	}
	page, err := service.ListConversations(ctx, evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "other", AccountType: "user"}, RecordIntegrity: "missing"})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("missing projection cannot authorize another owner: %+v %v", page, err)
	}
	page, err = service.ListConversations(ctx, evidencevo.SummaryQueryOptions{Scope: evidencevo.QueryScope{AccountID: "user", AccountType: "user"}, RecordIntegrity: "missing", Tool: "query_object_instance"})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("missing projection cannot prove tool filter: %+v %v", page, err)
	}
}

func TestConversationIntegrityIgnoresRoundsWithNoRegisteredCalls(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	empty := sessionvo.EvidenceSnapshot{Interaction: sessionvo.EvidenceInteraction{ID: "empty", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted}}
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot, snapshots: map[string]sessionvo.EvidenceSnapshot{"empty": empty}}
	ctx := context.Background()
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		for _, id := range []string{"int", "empty"} {
			tx.SaveInteraction(sessionvo.Interaction{ID: id, ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	entries := []evidencevo.ConversationSummary{{ConversationID: "conv"}}
	if err := service.applyConversationRecordIntegrity(ctx, entries, evidencevo.QueryScope{AccountID: "user", AccountType: "user"}); err != nil || entries[0].CurrentRecordIntegrity == nil || entries[0].CurrentRecordIntegrity.Status != "complete" {
		t.Fatalf("empty round must not prevent complete call records: %+v %v", entries, err)
	}
}
func TestInitialReadyOperationHasNoIntegrityVerdict(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.CallFacts, snapshot.Receipts = nil, nil
	snapshot.Operations[0].AttemptStatus = sessionvo.AttemptReady
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report != nil {
		t.Fatalf("unclaimed initial attempt has no call record scope: %+v %v", report, err)
	}
}

func TestRecordIntegrityArtifactBudgetIsSharedAcrossRounds(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a"}
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}
	ctx := context.WithValue(context.Background(), recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{reads: 127})
	if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	artifact := captureFixture("a", "rows")
	artifact.InteractionID, artifact.OperationID, artifact.RequestID, artifact.TraceID = "int", "op", "req", "trace"
	artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}}
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	first, found, err := service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || first.CurrentRecordIntegrity == nil {
		t.Fatalf("last permitted content read must succeed: %+v %v", first, err)
	}
	second, found, err := service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || second.CurrentRecordIntegrity != nil || !second.RecordIntegrityCheckFailed {
		t.Fatalf("exhausted request budget must retain facts without verdict: %+v %v", second, err)
	}
	if reads := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget).reads; reads != 128 {
		t.Fatalf("budget exhaustion must stop further artifact reads: %d", reads)
	}
}

func TestIntegrityBudgetChecksNewestCandidateDeterministically(t *testing.T) {
	for _, kind := range []string{"conversation", "interaction"} {
		for _, order := range [][]string{{"a", "b"}, {"b", "a"}} {
			t.Run(kind+order[0], func(t *testing.T) {
				_, owner, now := integrityFixture()
				store := &integritySnapshotStore{Store: memorystore.New(), snapshots: map[string]sessionvo.EvidenceSnapshot{}}
				artifacts := &integrityArtifactMap{byID: map[string]evidencevo.EvidenceArtifact{}}
				ctx := context.WithValue(context.Background(), recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{reads: 127})
				var conversations []evidencevo.ConversationSummary
				var interactions []evidencevo.InteractionListSummary
				for _, id := range order {
					snapshot, _, _ := integrityFixture()
					created := now
					if id == "b" {
						created = now.Add(time.Hour)
					}
					convID, intID := "conv_"+id, "int_"+id
					snapshot.Interaction.ID, snapshot.Interaction.ConversationID = intID, convID
					snapshot.Operations[0].InteractionID, snapshot.Operations[0].ConversationID = intID, convID
					snapshot.CallFacts[0].InteractionID, snapshot.CallFacts[0].ConversationID = intID, convID
					snapshot.Receipts[0].InteractionID, snapshot.Receipts[0].ConversationID = intID, convID
					snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:" + id}
					store.snapshots[intID] = snapshot
					artifact := captureFixture(id, "rows")
					artifact.InteractionID, artifact.OperationID, artifact.RequestID, artifact.TraceID = intID, "op", "req", "trace"
					artifact.ArtifactType = evidencevo.ArtifactTypeDataResult
					artifacts.byID[id] = artifact
					if err := store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
						tx.SaveConversation(sessionvo.Conversation{ID: convID, Owner: owner, CreatedAt: created})
						tx.SaveInteraction(sessionvo.Interaction{ID: intID, ConversationID: convID, Ordinal: 1, ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: created})
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					conversations = append(conversations, evidencevo.ConversationSummary{ConversationID: convID, StartedAt: created.Format(time.RFC3339Nano)})
					interactions = append(interactions, evidencevo.InteractionListSummary{InteractionID: intID, StartedAt: created.Format(time.RFC3339Nano)})
				}
				service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
				service.artifactStore = artifacts
				scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
				if kind == "conversation" {
					if err := service.applyConversationRecordIntegrity(ctx, conversations, scope); err != nil {
						t.Fatal(err)
					}
					for i, e := range conversations {
						if e.ConversationID != "conv_"+order[i] {
							t.Fatalf("check reordered conversation page: %+v", conversations)
						}
						if e.ConversationID == "conv_b" && (e.CurrentRecordIntegrity == nil || e.RecordIntegrityCheckFailed) {
							t.Fatalf("newest candidate lost budget: %+v", e)
						}
						if e.ConversationID == "conv_a" && (e.CurrentRecordIntegrity != nil || !e.RecordIntegrityCheckFailed) {
							t.Fatalf("older candidate took budget: %+v", e)
						}
					}
				} else {
					if err := service.applyInteractionRecordIntegrity(ctx, interactions, scope); err != nil {
						t.Fatal(err)
					}
					for i, e := range interactions {
						if e.InteractionID != "int_"+order[i] {
							t.Fatalf("check reordered round page: %+v", interactions)
						}
						if e.InteractionID == "int_b" && (e.CurrentRecordIntegrity == nil || e.RecordIntegrityCheckFailed) {
							t.Fatalf("newest round lost budget: %+v", e)
						}
						if e.InteractionID == "int_a" && (e.CurrentRecordIntegrity != nil || !e.RecordIntegrityCheckFailed) {
							t.Fatalf("older round took budget: %+v", e)
						}
					}
				}
			})
		}
	}
}

type integrityArtifactMap struct {
	iartifactstore.ArtifactStorePort
	byID map[string]evidencevo.EvidenceArtifact
}

func (a integrityArtifactMap) ReadArtifactForCapture(_ context.Context, id string, _ evidencevo.QueryScope, _ int64) (iartifactstore.CaptureReadResult, error) {
	artifact, found := a.byID[id]
	return iartifactstore.CaptureReadResult{Artifact: artifact, Found: found, Exists: found, ReadBytes: 20}, nil
}
