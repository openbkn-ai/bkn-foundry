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

func TestSQLTargetReceiptIndependentOfPublishDisposition(t *testing.T) {
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
			if disposition == "success" {
				EmitRunSQLEvents(ctx, nil, "SELECT * FROM {{.inventory}}", []string{"inventory", " inventory ", ""}, nil)
			} else {
				EmitRunSQLFailure(ctx, nil, "SELECT * FROM {{.inventory}}", []string{"inventory", " inventory ", ""}, RunSQLFailure{Stage: "vega_query", Code: "RUN_SQL_VEGA_QUERY_FAILED", Summary: "backend unavailable"})
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
				if _, apiErr, err := NewGuard(client).Finish(ctx, state, json.RawMessage(`{"stage":"vega_query"}`), true, false); apiErr != nil || err != nil {
					t.Fatalf("finish: %v %v", apiErr, err)
				}
				// Late callbacks must not rewrite the frozen terminal target or classification.
				EmitRunSQLFailure(ctx, nil, "late", []string{"different"}, RunSQLFailure{Stage: "input_validation", Code: "late"})
			}
			want := []BusinessRef{{RefType: "data_resource", RefID: "resource:inventory", Version: "unversioned"}}
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
				if code, stage := FreezeToolFailure(ctx); code != "RUN_SQL_VEGA_QUERY_FAILED" || stage != "vega_query" {
					t.Fatalf("classification changed: %s %s", code, stage)
				}
			}
		})
	}
}

func TestTerminalContextIsolatedBetweenNestedAttempts(t *testing.T) {
	parent := withEvidenceOutcome(context.Background())
	child := withEvidenceOutcome(parent)
	EmitRunSQLFailure(child, nil, "", []string{"child"}, RunSQLFailure{Code: "child", Stage: "vega_query"})
	if code, stage := FreezeToolFailure(parent); code != "" || stage != "" || len(retainedBusinessRefs(parent)) != 0 {
		t.Fatal("child failure leaked to parent")
	}
	freezeEvidenceExpectation(child)
	refs := retainedBusinessRefs(child)
	refs[0].RefID = "mutated"
	if retainedBusinessRefs(child)[0].RefID != "resource:child" {
		t.Fatal("caller mutated retained target")
	}
}

func TestUnmanagedChildCannotChangeParentTerminalFacts(t *testing.T) {
	parent := withRequestDerivedBusinessRefs(withEvidenceOutcome(testTraceContext()), []BusinessRef{{RefType: "knowledge_network", RefID: "kn:parent", Version: "unversioned"}})
	child := ClearManagedTraceContext(parent)
	EmitRunSQLFailure(child, nil, "", []string{"child"}, RunSQLFailure{Code: "child", Stage: "input_validation"})
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
