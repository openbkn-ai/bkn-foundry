// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package evidenceconsumer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/assemblysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/comm-go/bkntrace/evidencepublisher"
)

type semanticTransportSender struct{}

func (*semanticTransportSender) Send(context.Context, evidencepublisher.Record) error { return nil }

// Exercise the actual wire encoder, consumer decoder and business projection.
// There is no broker or database in this test: durable ingestion is tested separately.
func TestPublishedSemanticMetadataReachesBusinessAssembly(t *testing.T) {
	publisher, err := evidencepublisher.New(evidencepublisher.Config{
		ProducerID: "agent-retrieval", BaseStreamID: "agent-retrieval",
		WorkloadIdentity: "agent-retrieval", ProcessBootID: "boot-semantic", CapturePolicyRevision: "41",
	}, &semanticTransportSender{})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close(context.Background())
	// JSON decoding keeps this regression compilable against the previous shared
	// transport, which discarded the optional fields and produced an empty graph.
	var event evidencepublisher.Event
	if err := json.Unmarshal([]byte(`{"business_refs":[{"ref_type":"object_type","ref_id":"object:kn:stock","version":"v3"}],"operation_business_edges":[{"operation_id":"op-1","business_ref":{"ref_type":"object_type","ref_id":"object:kn:stock","version":"v3"},"role":"read","observed_at":"2026-10-22T08:00:01Z"}],"causation_event_ids":["evt-parent"]}`), &event); err != nil {
		t.Fatal(err)
	}
	event.EventID, event.EventType = "evt-result", "retrieval.completed"
	event.ConversationID, event.InteractionID, event.OperationID = "conv-1", "int-1", "op-1"
	event.Attempt, event.RequestID = 1, "req-1"
	event.StartedAt, event.ObservedAt, event.EmittedAt = "2026-10-22T08:00:00Z", "2026-10-22T08:00:01Z", "2026-10-22T08:00:02Z"
	event.Envelope = json.RawMessage(`{"owner":{"application_principal_id":"app-1","effective_subject_id":"user-1","effective_subject_type":"user"},"payload":{"source_refs":[]}}`)
	if result := publisher.TryPublish(event); result.Disposition != evidencepublisher.Accepted {
		t.Fatalf("publish: %#v", result)
	}
	decoded, err := parseLiveEvent(publisher.SnapshotQueue()[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Owner.ApplicationPrincipalID != "app-1" || decoded.Owner.EffectiveSubjectID != "user-1" {
		t.Fatalf("owner lost: %#v", decoded.Owner)
	}
	if decoded.PayloadHash != ledgervo.CanonicalPayloadHash(decoded.Envelope) {
		t.Fatal("payload hash differs from Ledger canonical hash")
	}
	if err := ledgersvc.ValidateHistoricalEvent(decoded); err != nil {
		t.Fatalf("Ledger semantic validation rejected published event: %v", err)
	}
	assembled := assemblysvc.Assemble("int-1", []ledgervo.Event{decoded}, nil)
	if len(assembled.BusinessRefs) != 1 || len(assembled.OperationBusinessEdges) != 1 {
		t.Fatalf("semantic graph lost across Kafka wire: %#v", assembled)
	}
	if assembled.BusinessRefs[0].Version != "v3" || assembled.OperationBusinessEdges[0].OperationID != "op-1" {
		t.Fatalf("business attribution changed: %#v", assembled)
	}
	if len(decoded.CausationEventIDs) != 1 || decoded.CausationEventIDs[0] != "evt-parent" {
		t.Fatalf("causality lost: %#v", decoded.CausationEventIDs)
	}
}
