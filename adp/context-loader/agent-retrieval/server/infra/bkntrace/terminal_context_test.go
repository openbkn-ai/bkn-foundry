// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package bkntrace

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// The targets a handler resolved and its failure classification are frozen at
// the first Finish, whatever happened to the evidence event that carried them.
func TestRetainedTargetReceiptIndependentOfPublishDisposition(t *testing.T) {
	for _, disposition := range []string{"disabled", "accepted", "dropped", "success"} {
		t.Run(disposition, func(t *testing.T) {
			SetEvidencePublisher(nil)
			t.Cleanup(func() { SetEvidencePublisher(nil) })
			if disposition != "disabled" {
				publisher := expectationTestPublisher(t, 1)
				if disposition == "dropped" {
					submitExpectationEvents(t, testTraceContext(), []Event{expectationEvent("filler")})
				}
				_ = publisher
			}
			ctx := withEvidenceOutcome(testTraceContext())
			EmitSchemaSnapshotEvents(ctx, nil, "object", "kn_demo", []string{"inventory", " inventory ", ""}, nil, true)
			if disposition != "success" {
				RecordToolFailure(ctx, "SCHEMA_BACKEND_FAILED", "schema_query")
			}
			type finishBody struct {
				BusinessRefs        []BusinessRef        `json:"business_refs"`
				EvidenceExpectation *EvidenceExpectation `json:"evidence_expectation"`
				EvidenceDurability  string               `json:"evidence_durability"`
				Error               PayloadEnvelope      `json:"error"`
				RequestID           string               `json:"request_id"`
				TraceID             string               `json:"trace_id"`
			}
			var bodies []finishBody
			client := lifecycleClientWithTransport(func(r *http.Request) (*http.Response, error) {
				var body finishBody
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, body)
				return lifecycleJSONResponse(http.StatusOK, OperationResult{Operation: Operation{OperationID: "op", Attempt: 1}, Receipt: Receipt{ReceiptID: "receipt", ReceiptStatus: "failed"}}), nil
			})
			state := GuardState{Result: OperationResult{Operation: Operation{OperationID: "op", Attempt: 1}, Receipt: Receipt{ReceiptID: "receipt"}}}
			for attempt := 0; attempt < 2; attempt++ {
				if _, apiErr, err := NewGuard(client).Finish(ctx, state, json.RawMessage(`{"stage":"schema_query"}`), true, false); apiErr != nil || err != nil {
					t.Fatalf("finish: %v %v", apiErr, err)
				}
				// Late callbacks must not rewrite the frozen terminal target or classification.
				EmitSchemaSnapshotEvents(ctx, nil, "object", "kn_demo", []string{"different"}, nil, true)
				RecordToolFailure(ctx, "late", "input_validation")
			}
			want := []BusinessRef{{RefType: "object_type", RefID: "object:kn_demo:inventory", Version: "unversioned"}}
			if !reflect.DeepEqual(bodies[0].BusinessRefs, want) || !reflect.DeepEqual(bodies[0], bodies[1]) {
				t.Fatalf("terminal target or retry changed: %+v", bodies)
			}
			switch disposition {
			case "accepted", "success":
				if bodies[0].EvidenceDurability != "pending" || len(bodies[0].EvidenceExpectation.Events) != 1 {
					t.Fatalf("enqueue claimed durable: %+v", bodies[0])
				}
			case "dropped":
				if bodies[0].EvidenceDurability != "failed" || bodies[0].EvidenceExpectation.Events[0].DropReason != "queue_full" {
					t.Fatalf("drop was hidden: %+v", bodies[0])
				}
			}
			if disposition != "success" {
				if code, stage := FreezeToolFailure(ctx); code != "SCHEMA_BACKEND_FAILED" || stage != "schema_query" {
					t.Fatalf("classification changed: %s %s", code, stage)
				}
			}
		})
	}
}

func TestTerminalContextIsolatedBetweenNestedAttempts(t *testing.T) {
	parent := withEvidenceOutcome(context.Background())
	child := withEvidenceOutcome(parent)
	EmitSchemaSnapshotEvents(child, nil, "object", "kn_demo", []string{"child"}, nil, true)
	RecordToolFailure(child, "child", "schema_query")
	if code, stage := FreezeToolFailure(parent); code != "" || stage != "" || len(retainedBusinessRefs(parent)) != 0 {
		t.Fatal("child failure leaked to parent")
	}
	freezeEvidenceExpectation(child)
	refs := retainedBusinessRefs(child)
	refs[0].RefID = "mutated"
	if retainedBusinessRefs(child)[0].RefID != "object:kn_demo:child" {
		t.Fatal("caller mutated retained target")
	}
}

func TestUnmanagedChildCannotChangeParentTerminalFacts(t *testing.T) {
	parent := withRequestDerivedBusinessRefs(withEvidenceOutcome(testTraceContext()), []BusinessRef{{RefType: "knowledge_network", RefID: "kn:parent", Version: "unversioned"}})
	child := ClearManagedTraceContext(parent)
	// Same knowledge network as the parent, so a leak cannot hide behind the
	// cross-network guard in retainSchemaSnapshotTargets.
	EmitSchemaSnapshotEvents(child, nil, "object", "parent", []string{"child"}, nil, true)
	RecordToolFailure(child, "child", "input_validation")
	if code, stage := FreezeToolFailure(parent); code != "" || stage != "" || len(retainedBusinessRefs(parent)) != 0 {
		t.Fatal("unmanaged child contaminated parent terminal facts")
	}
	if evidenceOutcomeFromContext(child) != nil || len(requestDerivedBusinessRefsFromContext(child)) != 0 {
		t.Fatal("unmanaged child retained parent attempt context")
	}
	managedChild := withRequestDerivedBusinessRefs(withEvidenceOutcome(parent), nil)
	if len(requestDerivedBusinessRefsFromContext(managedChild)) != 0 {
		t.Fatal("empty child target inherited parent request refs")
	}
}
