// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	memorystore "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"strings"
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

func TestPolicyOmissionDoesNotHideHistoricalArtifactLoss(t *testing.T) {
	snapshot, owner, now := integrityFixture()
	snapshot.Receipts[0].PartialReasons = []string{"not_collected_due_to_policy"}
	snapshot.CallFacts[0].Output = nil
	report, err := evaluateRecordIntegrity(snapshot, owner, now, nil)
	if err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("policy-only omitted output should not be missing: %#v %v", report, err)
	}
	snapshot.Receipts[0].ArtifactRefs = []string{"artifact:historical-result"}
	verify := func(sessionvo.OperationCallFact, string, sessionvo.PayloadEnvelope) (json.RawMessage, error) {
		return nil, nil
	}
	report, err = evaluateRecordIntegrity(snapshot, owner, now, verify)
	if err != nil || report == nil || report.Status != "missing" {
		t.Fatalf("historical artifact loss was hidden: %#v %v", report, err)
	}
}

func TestPolicyOmissionStillChecksHistoricalReferencedOutputAndError(t *testing.T) {
	for _, status := range []sessionvo.AttemptStatus{sessionvo.AttemptCompleted, sessionvo.AttemptFailed} {
		snapshot, owner, now := integrityFixture()
		snapshot.Receipts[0].PartialReasons = []string{"not_collected_due_to_policy"}
		ref := sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:historical-payload"}
		if status == sessionvo.AttemptCompleted {
			snapshot.CallFacts[0].Output = &ref
		} else {
			snapshot.CallFacts[0].Status = sessionvo.AttemptFailed
			snapshot.CallFacts[0].Output = nil
			snapshot.CallFacts[0].Error = &ref
			snapshot.Receipts[0].Status = sessionvo.ReceiptFailed
		}
		report, err := evaluateRecordIntegrity(snapshot, owner, now, func(sessionvo.OperationCallFact, string, sessionvo.PayloadEnvelope) (json.RawMessage, error) {
			return nil, nil
		})
		if err != nil || report == nil || report.Status != "missing" {
			t.Fatalf("status %q historical referenced payload was hidden: %#v %v", status, report, err)
		}
	}
}

func TestPolicyOmissionOnlySkipsNewResultLedger(t *testing.T) {
	base, owner, now := integrityFixture()
	base.Receipts[0].PartialReasons = []string{"not_collected_due_to_policy"}
	base.CallFacts[0].CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "ontology_result/v1", RequiredTraceFields: []string{"result_completeness"}}
	base.CallFacts[0].Output = nil
	report, err := evaluateRecordIntegrity(base, owner, now, nil)
	if err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("policy-only result omission = %#v %v", report, err)
	}

	withReference := base
	withReference.Receipts = append([]sessionvo.Receipt(nil), base.Receipts...)
	withReference.Receipts[0].ObservedEvidenceRefs = []string{"evt-old"}
	report, err = evaluateRecordIntegrity(withReference, owner, now, nil)
	if err != nil || report == nil || report.Status != "missing" {
		t.Fatalf("missing referenced ledger event was hidden: %#v %v", report, err)
	}

	withOutput := base
	withOutput.CallFacts = append([]sessionvo.OperationCallFact(nil), base.CallFacts...)
	output, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"rows":[]}`))
	withOutput.CallFacts[0].Output = &output
	report, err = evaluateRecordIntegrity(withOutput, owner, now, nil)
	if err != nil || report == nil || report.Status != "missing" {
		t.Fatalf("non-empty historical output missing ledger was hidden: %#v %v", report, err)
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

	materializeIntegrityFixture(t, service)
	summary, found, err := service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || summary.CurrentRecordIntegrity == nil || summary.CurrentRecordIntegrity.Status != "complete" {
		t.Fatalf("summary must include independent current result: %+v %v", summary, err)
	}
	if summary.EvidenceCompleteness != "partial" {
		t.Fatal("historical assembly state must not be rewritten")
	}
	store.readErr = errors.New("database read failed")
	summary, found, err = service.GetInteractionSummary(ctx, "int", scope)
	if err != nil || !found || summary.CurrentRecordIntegrity == nil || summary.RecordIntegrityCheckFailed || store.reads != 1 {
		t.Fatalf("stored check must survive evidence read failure: %+v %v reads=%d", summary, err, store.reads)
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

func TestRecordIntegrityManagedFunctionRequiresCanonicalTarget(t *testing.T) {
	for _, status := range []sessionvo.AttemptStatus{sessionvo.AttemptCompleted, sessionvo.AttemptFailed} {
		for _, test := range []struct {
			name string
			ref  sessionvo.BusinessRef
			want string
		}{
			{"matching function", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:bom_function", Version: "unversioned"}, "complete"},
			{"other network", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:other:bom_function", Version: "unversioned"}, "missing"},
			{"other function", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:inventory_function", Version: "unversioned"}, "missing"},
			{"network only", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefKnowledgeNetwork, RefID: "kn:supply", Version: "unversioned"}, "missing"},
			{"historical four-segment function", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:box:bom_function", Version: "unversioned"}, "complete"},
			{"historical four-segment function from another toolbox", sessionvo.BusinessRef{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:other_box:bom_function", Version: "unversioned"}, "missing"},
		} {
			t.Run(string(status)+"/"+test.name, func(t *testing.T) {
				s, owner, now := integrityFixture()
				f := &s.CallFacts[0]
				f.ToolName = "bom_function"
				f.Input, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"supply","toolbox_id":"box","tool_id":"bom_function","arguments":{"product":"382-000005"}}`))
				f.Status = status
				f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "managed_function_execution/v1", RequiredTraceFields: []string{"business_refs", "result_completeness"}}
				s.Receipts[0].Status = sessionvo.ReceiptStatus(status)
				s.Receipts[0].BusinessRefs = []sessionvo.BusinessRef{test.ref}
				if status == sessionvo.AttemptFailed {
					f.Error, f.Output = f.Output, nil
				}
				report, err := evaluateRecordIntegrity(s, owner, now, nil)
				if err != nil || report == nil || report.Status != test.want {
					t.Fatalf("recorded %s function target %q: report=%+v err=%v", status, test.ref.RefID, report, err)
				}
				if test.want == "missing" && (len(report.Missing) != 1 || report.Missing[0].Reason != "business_target_missing") {
					t.Fatalf("only the unmatched target is missing: %+v", report.Missing)
				}
			})
		}
	}
}

func TestRecordIntegrityManagedFunctionObservedTargetRequiresScope(t *testing.T) {
	s, owner, now := integrityFixture()
	f := &s.CallFacts[0]
	f.ToolName = "bom_function"
	f.Input, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"tool_id":"bom_function"}`))
	f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: "managed_function_execution/v1", RequiredTraceFields: []string{"business_refs"}}
	event := ledgervo.Event{EventID: "evt", Owner: owner, ConversationID: "conv", InteractionID: "int", OperationID: "op", Attempt: 1, RequestID: "req", TraceID: "trace", Envelope: json.RawMessage(`{"payload":{"source_refs":[{"ref_id":"function::bom_function"}]}}`)}
	raw, _ := json.Marshal(event)
	s.Ledger.Events = []sessionvo.EvidenceLedgerRecord{{EventID: "evt", Envelope: raw}}
	report, err := evaluateRecordIntegrity(s, owner, now, nil)
	if err != nil || report == nil || report.Status != "missing" || len(report.Missing) != 1 || report.Missing[0].Reason != "business_target_missing" {
		t.Fatalf("a missing request KN must not accept an unscoped observed function: report=%+v err=%v", report, err)
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
			report, err := service.inspectLiveRecordIntegrity(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
			if (err != nil) != test.failed {
				t.Fatalf("strict verification: %+v %v", report, err)
			}
			if test.status != "" && (report == nil || report.Status != test.status) {
				t.Fatalf("verdict: %+v", report)
			}
		})
	}
	artifact.OperationID = "another-op"
	service := New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
	service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Found: true, Exists: true, Artifact: artifact}}
	report, err := service.inspectLiveRecordIntegrity(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if report != nil || err == nil {
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
	materializeIntegrityFixture(t, service)
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
	materializeIntegrityFixture(t, service)
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
	first, err := service.inspectLiveRecordIntegrity(ctx, "int", scope)
	if err != nil || first == nil {
		t.Fatalf("last permitted content read must succeed: %+v %v", first, err)
	}
	second, err := service.inspectLiveRecordIntegrity(ctx, "int", scope)
	if err == nil || second != nil {
		t.Fatalf("exhausted request budget must retain facts without verdict: %+v %v", second, err)
	}
	if reads := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget).reads; reads != 128 {
		t.Fatalf("budget exhaustion must stop further artifact reads: %d", reads)
	}
}

func TestMaterializedPagesIgnoreExhaustedRequestBudget(t *testing.T) {
	for _, kind := range []string{"conversation", "interaction"} {
		for _, order := range [][]string{{"a", "b"}, {"b", "a"}} {
			t.Run(kind+order[0], func(t *testing.T) {
				_, owner, now := integrityFixture()
				store := &integritySnapshotStore{Store: memorystore.New(), snapshots: map[string]sessionvo.EvidenceSnapshot{}}
				artifacts := &integrityArtifactMap{byID: map[string]evidencevo.EvidenceArtifact{}}
				ctx := context.WithValue(context.Background(), recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{reads: 128})
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
				materializeIntegrityFixture(t, service)
				scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
				if kind == "conversation" {
					if err := service.applyConversationRecordIntegrity(ctx, conversations, scope); err != nil {
						t.Fatal(err)
					}
					for i, e := range conversations {
						if e.ConversationID != "conv_"+order[i] {
							t.Fatalf("check reordered conversation page: %+v", conversations)
						}
						if e.CurrentRecordIntegrity == nil || e.RecordIntegrityCheckFailed {
							t.Fatalf("newest candidate lost budget: %+v", e)
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
						if e.CurrentRecordIntegrity == nil || e.RecordIntegrityCheckFailed {
							t.Fatalf("newest round lost budget: %+v", e)
						}
					}
				}
				if store.reads != 2 {
					t.Fatalf("page performed live checks: %d", store.reads)
				}
			})
		}
	}
}

type integrityArtifactMap struct {
	iartifactstore.ArtifactStorePort
	byID map[string]evidencevo.EvidenceArtifact
}

func integrityArtifactWithContent(t *testing.T, id string, content any) (evidencevo.EvidenceArtifact, int, int) {
	t.Helper()
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		t.Fatal(err)
	}
	canonical := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	sum := sha256.Sum256(canonical)
	legacy, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	artifact := captureFixture(id, "rows")
	artifact.InteractionID, artifact.OperationID, artifact.RequestID, artifact.TraceID = "int", "op", "req", "trace"
	artifact.ArtifactType, artifact.Content, artifact.ContentHash = evidencevo.ArtifactTypeDataResult, content, "sha256:"+hex.EncodeToString(sum[:])
	return artifact, len(canonical), len(legacy)
}

func integrityServiceForSnapshot(t *testing.T, snapshot sessionvo.EvidenceSnapshot) *Service {
	t.Helper()
	_, owner, now := integrityFixture()
	store := &integritySnapshotStore{Store: memorystore.New(), snapshot: snapshot}
	if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
		tx.SaveConversation(sessionvo.Conversation{ID: "conv", Owner: owner})
		tx.SaveInteraction(sessionvo.Interaction{ID: "int", ConversationID: "conv", ExecutionStatus: sessionvo.InteractionCompleted, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return New(evidencestore.New(), WithSessionStore(store), WithCurrentRecordIntegrity())
}

func TestRecordIntegrityAcceptsOnlyExactVerifiedPayloadLengths(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"HTML", "<stock>&supplier"},
		{"Unicode", "物料😀\u2028next\u2029line"},
		{"precise numbers", "precise &"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := map[string]any{"text": tc.text, "number": json.Number("9007199254740993"), "decimal": json.Number("1.234567890123456789")}
			artifact, canonicalLength, legacyLength := integrityArtifactWithContent(t, "a", content)
			for _, length := range []int{canonicalLength, legacyLength, canonicalLength + 1, legacyLength + 1} {
				snapshot, _, _ := integrityFixture()
				snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a", ByteLength: length}
				service := integrityServiceForSnapshot(t, snapshot)
				service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}}
				report, err := service.inspectLiveRecordIntegrity(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
				valid := length == canonicalLength || length == legacyLength
				if valid && (err != nil || report == nil || report.Status != "complete") {
					t.Fatalf("verified length=%d canonical=%d legacy=%d: %+v %v", length, canonicalLength, legacyLength, report, err)
				}
				if !valid && (err == nil || report != nil) {
					t.Fatalf("arbitrary length=%d must fail: %+v %v", length, report, err)
				}
			}
		})
	}
}

func TestRecordIntegrityPayloadLengthCompatibilityPreservesVerification(t *testing.T) {
	for _, mutation := range []string{"hash", "artifact identity", "operation", "request", "trace", "type"} {
		t.Run(mutation, func(t *testing.T) {
			artifact, canonicalLength, legacyLength := integrityArtifactWithContent(t, "a", map[string]any{"text": "<stock>&"})
			switch mutation {
			case "hash":
				artifact.ContentHash = "sha256:" + strings.Repeat("0", 64)
			case "artifact identity":
				artifact.ArtifactID = "wrong"
			case "operation":
				artifact.OperationID = "wrong"
			case "request":
				artifact.RequestID = "wrong"
			case "trace":
				artifact.TraceID = "wrong"
			case "type":
				artifact.ArtifactType = evidencevo.ArtifactTypeQuery
			}
			snapshot, _, _ := integrityFixture()
			length := canonicalLength
			if mutation == "hash" {
				length = legacyLength
			}
			snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a", ByteLength: length}
			service := integrityServiceForSnapshot(t, snapshot)
			service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}}
			if report, err := service.inspectLiveRecordIntegrity(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"}); err == nil || report != nil {
				t.Fatalf("invalid %s must fail: %+v %v", mutation, report, err)
			}
		})
	}
}

func TestRecordIntegrityRepeatedReceiptArtifactsUseOneBoundedRead(t *testing.T) {
	snapshot, _, _ := integrityFixture()
	fact, receipt, operation := snapshot.CallFacts[0], snapshot.Receipts[0], snapshot.Operations[0]
	snapshot.CallFacts, snapshot.Receipts, snapshot.Operations = nil, nil, nil
	reader := &integrityCountingArtifacts{captureArtifactReader: captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{}}}
	for i := 0; i < 12; i++ {
		id, opID := fmt.Sprintf("a%d", i), fmt.Sprintf("op%d", i)
		artifact, canonicalLength, _ := integrityArtifactWithContent(t, id, map[string]any{"text": strings.Repeat("x", 1430000)})
		artifact.OperationID = opID
		reader.records[id] = artifact
		f, r, op := fact, receipt, operation
		f.OperationID, r.OperationID, op.ID = opID, opID, opID
		f.ReceiptID, r.ID = "receipt"+id, "receipt"+id
		f.Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:" + id, ByteLength: canonicalLength}
		r.ArtifactRefs = []string{"artifact:" + id}
		snapshot.CallFacts = append(snapshot.CallFacts, f)
		snapshot.Receipts = append(snapshot.Receipts, r)
		snapshot.Operations = append(snapshot.Operations, op)
	}
	reader.hook = func(id string) (iartifactstore.CaptureReadResult, error) {
		if reader.budgets[len(reader.budgets)-1] < 1430000 {
			return iartifactstore.CaptureReadResult{}, errors.New("artifact exceeds remaining read budget")
		}
		return iartifactstore.CaptureReadResult{Artifact: reader.records[id], Found: true, Exists: true, ReadBytes: 1430000}, nil
	}
	service := integrityServiceForSnapshot(t, snapshot)
	service.artifactStore = reader
	ctx := withRecordIntegrityReadBudget(context.Background())
	report, err := service.inspectLiveRecordIntegrity(ctx, "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("12 complete repeated artifacts must fit budget: %+v %v", report, err)
	}
	budget := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget)
	if len(reader.ids) != 12 || budget.reads != 12 || budget.bytes != 12*1430000 {
		t.Fatalf("duplicate refs incurred I/O: ids=%v budget=%+v", reader.ids, budget)
	}
	t.Logf("12 artifacts + 12 receipt references: reads=%d bytes=%d (32MiB limit=%d)", budget.reads, budget.bytes, 32<<20)
}

type integrityCountingArtifacts struct {
	iartifactstore.ArtifactStorePort
	captureArtifactReader
}

func TestRecordIntegrityCachedArtifactStillChecksEveryCallReference(t *testing.T) {
	for _, mutation := range []string{"operation", "request", "trace", "length", "type"} {
		t.Run(mutation, func(t *testing.T) {
			artifact, length, _ := integrityArtifactWithContent(t, "a", map[string]any{"text": "<stock>&"})
			snapshot, _, _ := integrityFixture()
			snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a", ByteLength: length}
			f, r := snapshot.CallFacts[0], snapshot.Receipts[0]
			f.Attempt, r.Attempt, snapshot.Operations[0].Attempt = 2, 2, 2
			f.ReceiptID, r.ID = "rcpt2", "rcpt2"
			switch mutation {
			case "operation":
				f.OperationID, r.OperationID = "wrong", "wrong"
			case "request":
				f.RequestID, r.RequestID = "wrong", "wrong"
			case "trace":
				f.TraceID, r.TraceID = "wrong", "wrong"
			case "length":
				f.Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a", ByteLength: length + 1}
			case "type":
				f.Input = *f.Output
			}
			snapshot.CallFacts = append(snapshot.CallFacts, f)
			snapshot.Receipts = append(snapshot.Receipts, r)
			service := integrityServiceForSnapshot(t, snapshot)
			service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true}}
			if report, err := service.inspectLiveRecordIntegrity(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"}); err == nil || report != nil {
				t.Fatalf("cached artifact accepted invalid %s: %+v %v", mutation, report, err)
			}
		})
	}
}

func TestRecordIntegrityReceiptReuseDoesNotExtendSharedByteBudget(t *testing.T) {
	artifact, length, _ := integrityArtifactWithContent(t, "a", map[string]any{"text": "rows"})
	snapshot, _, _ := integrityFixture()
	snapshot.CallFacts[0].Output = &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadReferenced, Ref: "artifact:a", ByteLength: length}
	snapshot.Receipts[0].ArtifactRefs = []string{"artifact:a"}
	service := integrityServiceForSnapshot(t, snapshot)
	service.artifactStore = integrityArtifacts{result: iartifactstore.CaptureReadResult{Artifact: artifact, Found: true, Exists: true, ReadBytes: int64(length)}}
	ctx := context.WithValue(context.Background(), recordIntegrityBudgetKey{}, &recordIntegrityReadBudget{bytes: (32 << 20) - int64(length)})
	scope := evidencevo.QueryScope{AccountID: "user", AccountType: "user"}
	if report, err := service.inspectLiveRecordIntegrity(ctx, "int", scope); err != nil || report == nil || report.Status != "complete" {
		t.Fatalf("receipt reuse must fit last allowed read: %+v %v", report, err)
	}
	if report, err := service.inspectLiveRecordIntegrity(ctx, "int", scope); err == nil || report != nil {
		t.Fatalf("new inspection must still fail exhausted budget: %+v %v", report, err)
	}
	budget := ctx.Value(recordIntegrityBudgetKey{}).(*recordIntegrityReadBudget)
	if budget.reads != 1 || budget.bytes != 32<<20 {
		t.Fatalf("shared budget changed: %+v", budget)
	}
}

func TestRecordIntegrityCachedReceiptChecksCurrentSource(t *testing.T) {
	artifact, _, _ := integrityArtifactWithContent(t, "a", map[string]any{"text": "rows"})
	artifact.InteractionID = ""
	snapshot, _, _ := integrityFixture()
	snapshot.Receipts[0].ArtifactRefs = []string{"artifact:a"}
	second := snapshot.Receipts[0]
	second.ID, second.Attempt, second.RequestID = "rcpt2", 2, "wrong"
	snapshot.Receipts = append(snapshot.Receipts, second)
	reader := &integrityCountingArtifacts{captureArtifactReader: captureArtifactReader{records: map[string]evidencevo.EvidenceArtifact{"a": artifact}}}
	service := integrityServiceForSnapshot(t, snapshot)
	service.artifactStore = reader
	report, err := service.inspectLiveRecordIntegrity(context.Background(), "int", evidencevo.QueryScope{AccountID: "user", AccountType: "user"})
	if err == nil || report != nil || !strings.Contains(err.Error(), "unverified_source_scope") {
		t.Fatalf("cached receipt must recheck its request source: %+v %v", report, err)
	}
	if len(reader.ids) != 1 {
		t.Fatalf("source recheck must not reread content: %v", reader.ids)
	}
}

func (a integrityArtifactMap) ReadArtifactForCapture(_ context.Context, id string, _ evidencevo.QueryScope, _ int64) (iartifactstore.CaptureReadResult, error) {
	artifact, found := a.byID[id]
	return iartifactstore.CaptureReadResult{Artifact: artifact, Found: found, Exists: found, ReadBytes: 20}, nil
}
