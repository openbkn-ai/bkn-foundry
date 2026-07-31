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
		"schema_version": "3.0.0", "payload_hash": ledgervo.CanonicalPayloadHash(envelope),
		"tenant_id": "forged", "business_domain_id": "forged",
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
