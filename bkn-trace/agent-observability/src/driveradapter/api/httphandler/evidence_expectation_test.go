// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

func TestFinishAttemptHTTPFreezesExpectedEventsAndConverges(t *testing.T) {
	for _, eventFirst := range []bool{false, true} {
		t.Run(strconv.FormatBool(eventFirst), func(t *testing.T) {
			sessions, ledger := sessionstore.New(), ledgerstore.New()
			sessions.SetEvidenceReader(ledger.ReadEvidenceEventsByIDs)
			lifecycle := sessionsvc.New(sessions, sessionsvc.Options{})
			ledgerService := ledgersvc.New(ledger)
			ledgerService.SetDurableObserver(lifecycle.ReconcileEvidence)
			owner := sessionvo.Owner{ApplicationPrincipalID: "app-1", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: "user-1"}
			conversation, err := lifecycle.EnsureCurrentConversation(context.Background(), sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "wire", IdempotencyKey: "wire-conv"})
			if err != nil {
				t.Fatal(err)
			}
			interaction, err := lifecycle.StartInteraction(context.Background(), sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conversation.ID, IdempotencyKey: "wire-int"})
			if err != nil {
				t.Fatal(err)
			}
			input, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"args":[]}`))
			operation, receipt, err := lifecycle.EnsureOperation(context.Background(), sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID, OperationKey: "wire-op", ToolName: "wire-tool", Input: input, Required: true, LeaseToken: interaction.LeaseToken, LeaseEpoch: interaction.LeaseEpoch})
			if err != nil {
				t.Fatal(err)
			}
			traceID, spanID := "0123456789abcdef0123456789abcdef", "0123456789abcdef"
			envelope, _ := json.Marshal(map[string]any{"owner": owner, "event_id": "wire-event", "event_type": "retrieval.completed", "conversation_id": conversation.ID, "interaction_id": interaction.ID, "operation_id": operation.ID, "attempt": receipt.Attempt, "bkn.request.id": "wire-request", "trace_id": traceID, "span_id": spanID})
			now := time.Now().UTC()
			event := ledgervo.Event{EventID: "wire-event", EventType: "retrieval.completed", SchemaVersion: "3.0.0", Envelope: envelope, PayloadHash: ledgervo.CanonicalPayloadHash(envelope), ProducerID: "wire-producer", ProducerStreamID: "wire-stream", ProducerEpoch: 1, ProducerSequence: 1, Owner: owner, ConversationID: conversation.ID, InteractionID: interaction.ID, OperationID: operation.ID, Attempt: receipt.Attempt, RequestID: "wire-request", TraceID: traceID, SpanID: spanID, StartedAt: now, ObservedAt: now, EmittedAt: now}
			if eventFirst {
				if _, err := ledgerService.Ingest(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			output, _ := sessionvo.InlineJSONPayload(json.RawMessage(`{"result":"<>&"}`))
			body, _ := json.Marshal(map[string]any{"receipt_id": receipt.ID, "output": output, "evidence_durability": "pending", "request_id": "wire-request", "trace_id": traceID, "span_id": spanID, "evidence_expectation": sessionvo.EvidenceExpectation{Version: 1, Closed: true, Events: []sessionvo.ExpectedEvidenceEvent{{EventID: event.EventID, EventType: event.EventType, PayloadHash: event.PayloadHash, ProducerID: event.ProducerID, PublishDisposition: "accepted"}}}})
			mux := http.NewServeMux()
			httphandler.RegisterSessionRoutes(mux, "/api/agent-observability/v1", httphandler.NewSessionHandler(lifecycle))
			path := "/api/agent-observability/v1/operations/" + operation.ID + "/attempts/1:complete"
			response := performLifecycleRequest(t, mux, http.MethodPost, path, string(body))
			if response.Code != http.StatusOK {
				t.Fatalf("finish wire rejected: %d %s", response.Code, response.Body.String())
			}
			if !eventFirst {
				if _, err := ledgerService.Ingest(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			confirmed, err := lifecycle.GetReceipt(context.Background(), owner, receipt.ID)
			if err != nil || confirmed.EvidenceDurability != sessionvo.DurabilityDurable || len(confirmed.ObservedEvidenceRefs) != 1 {
				t.Fatalf("wire did not converge: %#v %v", confirmed, err)
			}
			response = performLifecycleRequest(t, mux, http.MethodPost, path, string(body))
			if response.Code != http.StatusOK {
				t.Fatalf("exact original HTTP finish did not replay: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
