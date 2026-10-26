// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const integrityFunctionResultContract = "function_result_evidence/v1"

// Match the registered call and publisher event shape from the real FunctionResult SQL
// fixture. The verdict checks an explicit producer declaration, never answer text.
func integrityFunctionResultFixture() (sessionvo.EvidenceSnapshot, sessionvo.Owner, ledgervo.Event) {
	snapshot, owner, _ := integrityFixture()
	f := &snapshot.CallFacts[0]
	f.ToolName = "quality_check"
	f.SourceModule = "context-loader-function-result/v1"
	f.Input, _ = sessionvo.InlineJSONPayload(json.RawMessage(`{"kn_id":"supply","toolbox_id":"box","tool_id":"quality_check","arguments":{"products":["P1","P2"]}}`))
	f.CapabilityProfile = &sessionvo.CapabilityProfile{Resolution: "matched", EvidenceContract: integrityFunctionResultContract, RequiredTraceFields: []string{"business_refs", "result_completeness"}}
	r := &snapshot.Receipts[0]
	r.BusinessRefs = []sessionvo.BusinessRef{{RefType: sessionvo.BusinessRefFunction, RefID: "function:supply:quality_check", Version: "approved-version"}}
	r.ObservedEvidenceRefs = []string{"result-proof"}
	event := ledgervo.Event{EventID: "result-proof", EventType: "logic.execution.observed", ProducerID: "agent-retrieval", Owner: owner, ConversationID: f.ConversationID, InteractionID: f.InteractionID, OperationID: f.OperationID, Attempt: f.Attempt, RequestID: f.RequestID, TraceID: f.TraceID, Envelope: json.RawMessage(`{"payload":{"result_contract":"function_result_evidence/v1","complete":true}}`)}
	return snapshot, owner, event
}

func setIntegrityFunctionResultEvents(t *testing.T, snapshot *sessionvo.EvidenceSnapshot, events ...ledgervo.Event) {
	t.Helper()
	snapshot.Ledger.Events = nil
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Ledger.Events = append(snapshot.Ledger.Events, sessionvo.EvidenceLedgerRecord{EventID: event.EventID, Envelope: raw})
	}
}

func TestRecordIntegrityFunctionResultTypedResultCompleteness(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		mutate  func(*sessionvo.EvidenceSnapshot, *ledgervo.Event)
		want    string
	}{
		{name: "complete", want: "complete"},
		{name: "missing", payload: `{"result_contract":"function_result_evidence/v1"}`, want: "missing"},
		{name: "false", payload: `{"result_contract":"function_result_evidence/v1","complete":false}`, want: "missing"},
		{name: "string", payload: `{"result_contract":"function_result_evidence/v1","complete":"true"}`, want: "missing"},
		{name: "null", payload: `{"result_contract":"function_result_evidence/v1","complete":null}`, want: "missing"},
		{name: "number", payload: `{"result_contract":"function_result_evidence/v1","complete":1}`, want: "missing"},
		{name: "truncated flag is not substitute", payload: `{"result_contract":"function_result_evidence/v1","truncated":false}`, want: "missing"},
		{name: "wrong contract", payload: `{"result_contract":"other/v1","complete":true}`, want: "missing"},
		{name: "missing contract", payload: `{"complete":true}`, want: "missing"},
		{name: "wrong producer", mutate: func(_ *sessionvo.EvidenceSnapshot, e *ledgervo.Event) { e.ProducerID = "other" }, want: "missing"},
		{name: "not receipt bound", mutate: func(s *sessionvo.EvidenceSnapshot, _ *ledgervo.Event) { s.Receipts[0].ObservedEvidenceRefs = nil }, want: "missing"},
		{name: "wrong attempt", mutate: func(_ *sessionvo.EvidenceSnapshot, e *ledgervo.Event) { e.Attempt++ }, want: "missing"},
		{name: "wrong owner", mutate: func(_ *sessionvo.EvidenceSnapshot, e *ledgervo.Event) { e.Owner.EffectiveSubjectID = "other" }, want: "missing"},
		{name: "wrong registered source", mutate: func(s *sessionvo.EvidenceSnapshot, _ *ledgervo.Event) { s.CallFacts[0].SourceModule = "untrusted" }, want: "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, owner, event := integrityFunctionResultFixture()
			if test.payload != "" {
				event.Envelope = json.RawMessage(`{"payload":` + test.payload + `}`)
			}
			if test.mutate != nil {
				test.mutate(&snapshot, &event)
			}
			setIntegrityFunctionResultEvents(t, &snapshot, event)
			report, err := evaluateRecordIntegrity(snapshot, owner, snapshot.Interaction.TerminalAt.UTC(), nil)
			if err != nil || report == nil || report.Status != test.want {
				t.Fatalf("%s: report=%+v err=%v", test.name, report, err)
			}
			if test.want == "missing" && !hasFunctionResultIntegrityGap(report, "evidence.result_completeness") {
				t.Fatalf("missing declaration not diagnosed: %+v", report)
			}
		})
	}
}

func TestRecordIntegrityFunctionResultRejectsAmbiguousDeclarationsAndUnrelatedTargets(t *testing.T) {
	for _, test := range []string{"two result declarations", "network only", "other function"} {
		t.Run(test, func(t *testing.T) {
			snapshot, owner, event := integrityFunctionResultFixture()
			events := []ledgervo.Event{event}
			field := "business_refs"
			switch test {
			case "two result declarations":
				other := event
				other.EventID = "other-proof"
				snapshot.Receipts[0].ObservedEvidenceRefs = append(snapshot.Receipts[0].ObservedEvidenceRefs, other.EventID)
				events = append(events, other)
				field = "evidence.result_completeness"
			case "network only":
				snapshot.Receipts[0].BusinessRefs = []sessionvo.BusinessRef{{RefType: sessionvo.BusinessRefKnowledgeNetwork, RefID: "kn:supply", Version: "unversioned"}}
			default:
				snapshot.Receipts[0].BusinessRefs[0].RefID = "function:supply:other"
			}
			setIntegrityFunctionResultEvents(t, &snapshot, events...)
			report, err := evaluateRecordIntegrity(snapshot, owner, snapshot.Interaction.TerminalAt.UTC(), nil)
			if err != nil || report == nil || report.Status != "missing" || !hasFunctionResultIntegrityGap(report, field) {
				t.Fatalf("%s accepted: report=%+v err=%v", test, report, err)
			}
		})
	}
}

func hasFunctionResultIntegrityGap(report *evidencevo.RecordIntegrity, field string) bool {
	for _, missing := range report.Missing {
		if missing.Field == field {
			return true
		}
	}
	return false
}
