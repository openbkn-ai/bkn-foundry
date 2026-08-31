// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/httphandler"
)

func TestLedgerHandlerReturnsDurableAckAndUsesTrustedOwner(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	handler := httphandler.NewLedgerHandler(ledgersvc.New(store), httphandler.LedgerSecurityConfig{
		AllowUnauthenticatedIngest: true,
	})
	envelope := json.RawMessage(`{"result":"15991"}`)
	payload := map[string]any{
		"event_id": "evt-http-1", "event_type": "operation.output.observed",
		"bkn.trace.schema.version": "3.0.0", "payload_hash": ledgervo.CanonicalPayloadHash(envelope),
		"conversation_id": "conv-1", "interaction_id": "int-1", "operation_id": "op-1",
		"attempt": 1, "producer_id": "context-loader", "producer_stream_id": "stream-1",
		"producer_epoch": 1, "producer_sequence": 1,
		"started_at":  time.Date(2026, 7, 30, 9, 59, 59, 0, time.UTC),
		"observed_at": time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		"emitted_at":  time.Date(2026, 7, 30, 10, 0, 1, 0, time.UTC),
		"envelope":    envelope,
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", bytes.NewReader(body))
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.Ingest(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	var ack ledgervo.DurableAck
	decodeLifecycleResponse(t, response, &ack)
	if !ack.Durable || ack.EventID != "evt-http-1" || store.LedgerCount() != 1 {
		t.Fatalf("unexpected durable ingest: %#v", ack)
	}
}

func TestLedgerHandlerRequiresEvidenceIngestToken(t *testing.T) {
	t.Parallel()

	handler := httphandler.NewLedgerHandler(ledgersvc.New(ledgerstore.New()), httphandler.LedgerSecurityConfig{
		IngestToken: "evidence-token",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", nil)
	response := httptest.NewRecorder()

	handler.Ingest(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing evidence ingest token = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

func TestLedgerHandlerRejectsCallerControlledIdentityFields(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	handler := httphandler.NewLedgerHandler(ledgersvc.New(store), httphandler.LedgerSecurityConfig{
		AllowUnauthenticatedIngest: true,
	})
	envelope := json.RawMessage(`{"result":"15991"}`)
	payload := map[string]any{
		"event_id": "evt-forged-owner", "event_type": "operation.output.observed",
		"bkn.trace.schema.version": "3.0.0", "payload_hash": ledgervo.CanonicalPayloadHash(envelope),
		"conversation_id": "conv-1", "interaction_id": "int-1",
		"application_principal_id": "forged-app",
		"operation_id":             "op-1", "producer_id": "context-loader", "producer_stream_id": "stream-1",
		"producer_epoch": 1, "producer_sequence": 1,
		"started_at":  time.Date(2026, 7, 30, 9, 59, 59, 0, time.UTC),
		"observed_at": time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		"emitted_at":  time.Date(2026, 7, 30, 10, 0, 1, 0, time.UTC), "envelope": envelope,
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", bytes.NewReader(body))
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.Ingest(response, request)

	if response.Code != http.StatusBadRequest || store.LedgerCount() != 0 {
		t.Fatalf("caller-controlled identity must be rejected, got %d: %s", response.Code, response.Body.String())
	}
}

func TestLedgerHandlerAcceptsTypedSemanticEvidence(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	handler := httphandler.NewLedgerHandler(ledgersvc.New(store), httphandler.LedgerSecurityConfig{
		AllowUnauthenticatedIngest: true,
	})
	envelope := json.RawMessage(`{"result":"11594"}`)
	payload := map[string]any{
		"event_id": "evt-http-semantic", "event_type": "claim.asserted",
		"bkn.trace.schema.version": "3.0.0", "payload_hash": ledgervo.CanonicalPayloadHash(envelope),
		"conversation_id": "conv-1", "interaction_id": "int-1", "operation_id": "op-1",
		"producer_id": "third-party-agent", "producer_stream_id": "stream-1",
		"producer_epoch": 1, "producer_sequence": 1,
		"started_at":    time.Date(2026, 7, 30, 9, 59, 59, 0, time.UTC),
		"observed_at":   time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		"emitted_at":    time.Date(2026, 7, 30, 10, 0, 1, 0, time.UTC),
		"envelope":      envelope,
		"artifact_refs": []string{"artifact:query", "artifact:answer"},
		"business_refs": []map[string]any{{
			"ref_type": "object_type", "ref_id": "object:supplychain:forecast",
			"version": "1",
		}},
		"evidence_refs": []map[string]any{{
			"evidence_ref": "evidence:june", "ref_type": "artifact_fragment",
			"source_interaction_id": "int-1", "source_revision_id": "rev-source-1",
			"source_operation_id": "op-1", "artifact_ref": "artifact:query",
			"fragment_selector": "rows:0-62", "version": "1", "content_hash": "sha256:june",
		}},
		"claims": []map[string]any{{
			"claim_id": "claim-total", "claim_type": "answer", "materiality": "material",
			"claim_status": "asserted", "content_artifact_ref": "artifact:answer",
			"required_support_roles": []string{"source_data"},
			"supports": []map[string]any{{
				"target_ref": "evidence:june", "target_type": "artifact_fragment",
				"source_interaction_id": "int-1", "source_revision_id": "rev-source-1",
				"source_operation_id": "op-1", "version": "1", "content_hash": "sha256:june",
				"fragment_selector": "rows:0-62", "role": "source_data", "status": "adopted",
			}},
		}},
		"operation_business_edges": []map[string]any{{
			"operation_id": "op-1", "role": "aggregate", "observed_at": time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
			"business_ref": map[string]any{
				"ref_type": "object_type", "ref_id": "object:supplychain:forecast",
				"version": "1",
			},
		}},
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-observability/v1/evidence/events", bytes.NewReader(body))
	setTrustedOwnerHeaders(request)
	response := httptest.NewRecorder()

	handler.Ingest(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected typed event to be accepted, got %d: %s", response.Code, response.Body.String())
	}
	stored, found := store.StoredEvent("evt-http-semantic")
	if !found || len(stored.ArtifactRefs) != 2 || len(stored.Claims) != 1 || len(stored.EvidenceRefs) != 1 || len(stored.OperationBusinessEdges) != 1 {
		t.Fatalf("typed semantic evidence was not durably retained: %#v", stored)
	}
}
