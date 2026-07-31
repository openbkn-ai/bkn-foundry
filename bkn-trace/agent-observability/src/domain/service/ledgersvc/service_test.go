package ledgersvc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/ledgerstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
)

func TestEvidenceLedgerReturnsDurableAckOnlyAfterOutboxCommit(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	event := testEvent()
	ack, err := service.Ingest(context.Background(), event)
	if err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	if !ack.Durable || ack.IngestSequence != 1 || ack.Replayed {
		t.Fatalf("unexpected durable ack: %#v", ack)
	}
	if store.LedgerCount() != 1 || store.PendingProjectionCount() != 1 {
		t.Fatalf("ledger and outbox must commit together: ledger=%d outbox=%d",
			store.LedgerCount(), store.PendingProjectionCount())
	}

	replayed, err := service.Ingest(context.Background(), event)
	if err != nil {
		t.Fatalf("replay event: %v", err)
	}
	if !replayed.Durable || !replayed.Replayed || replayed.IngestSequence != ack.IngestSequence {
		t.Fatalf("unexpected replay ack: %#v", replayed)
	}
	if store.LedgerCount() != 1 || store.PendingProjectionCount() != 1 {
		t.Fatal("idempotent replay duplicated durable records")
	}
}

func TestEvidenceLedgerRecordsAcceptedAndHashConflictMetrics(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	metrics := &ledgerTestMetrics{counts: make(map[string]uint64)}
	service := ledgersvc.NewWithMetrics(store, metrics)
	event := testEvent()
	if _, err := service.Ingest(context.Background(), event); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	event.Owner.TenantID = "tenant-2"
	if _, err := service.Ingest(context.Background(), event); !ledgersvc.IsCode(err, ledgersvc.CodeEventPayloadConflict) {
		t.Fatalf("expected immutable event conflict, got %v", err)
	}
	if metrics.counts[icoremetrics.EvidenceIngestTotal] != 1 ||
		metrics.counts[icoremetrics.EvidenceHashConflictsTotal] != 1 {
		t.Fatalf("unexpected evidence metrics: %#v", metrics.counts)
	}
}

func TestEvidenceEventPayloadConflictIsQuarantined(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	event := testEvent()
	if _, err := service.Ingest(context.Background(), event); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	event.Envelope = json.RawMessage(`{"answer":"different"}`)
	event.PayloadHash = ledgervo.CanonicalPayloadHash(event.Envelope)
	if _, err := service.Ingest(context.Background(), event); !ledgersvc.IsCode(err, ledgersvc.CodeEventPayloadConflict) {
		t.Fatalf("expected event_payload_conflict, got %v", err)
	}
	if store.ConflictCount() != 1 || store.LedgerCount() != 1 {
		t.Fatalf("conflict must be quarantined without overwriting ledger")
	}
}

func TestEvidenceReplayCannotChangeImmutableOwnershipMetadata(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	event := testEvent()
	if _, err := service.Ingest(context.Background(), event); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	event.Owner.TenantID = "tenant-2"
	if _, err := service.Ingest(context.Background(), event); !ledgersvc.IsCode(err, ledgersvc.CodeEventPayloadConflict) {
		t.Fatalf("expected immutable event conflict, got %v", err)
	}
}

func TestProjectionWriteFailureDoesNotReturnDurableAck(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	store.FailProjectionWrites(true)
	service := ledgersvc.New(store)
	ack, err := service.Ingest(context.Background(), testEvent())
	if err == nil || ack.Durable {
		t.Fatalf("expected failed ingest without durable ack, got %#v, %v", ack, err)
	}
	if store.LedgerCount() != 0 || store.PendingProjectionCount() != 0 {
		t.Fatal("failed transaction left a partial ledger or outbox record")
	}
}

func TestProducerSequenceAndEpochRollbackAreRejected(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	first := testEvent()
	first.ProducerSequence = 10
	if _, err := service.Ingest(context.Background(), first); err != nil {
		t.Fatalf("ingest stream head: %v", err)
	}

	sequenceRollback := testEvent()
	sequenceRollback.EventID = "evt-sequence-rollback"
	sequenceRollback.ProducerSequence = 9
	sequenceRollback.Envelope = json.RawMessage(`{"answer":"sequence rollback"}`)
	sequenceRollback.PayloadHash = ledgervo.CanonicalPayloadHash(sequenceRollback.Envelope)
	if _, err := service.Ingest(context.Background(), sequenceRollback); !ledgersvc.IsCode(err, ledgersvc.CodeEventSequenceConflict) {
		t.Fatalf("expected sequence rollback conflict, got %v", err)
	}

	nextEpoch := testEvent()
	nextEpoch.EventID = "evt-next-epoch"
	nextEpoch.ProducerEpoch = 2
	nextEpoch.ProducerSequence = 1
	nextEpoch.Envelope = json.RawMessage(`{"answer":"new epoch"}`)
	nextEpoch.PayloadHash = ledgervo.CanonicalPayloadHash(nextEpoch.Envelope)
	if _, err := service.Ingest(context.Background(), nextEpoch); err != nil {
		t.Fatalf("ingest next epoch: %v", err)
	}

	epochRollback := testEvent()
	epochRollback.EventID = "evt-epoch-rollback"
	epochRollback.ProducerEpoch = 1
	epochRollback.ProducerSequence = 11
	epochRollback.Envelope = json.RawMessage(`{"answer":"epoch rollback"}`)
	epochRollback.PayloadHash = ledgervo.CanonicalPayloadHash(epochRollback.Envelope)
	if _, err := service.Ingest(context.Background(), epochRollback); !ledgersvc.IsCode(err, ledgersvc.CodeEventSequenceConflict) {
		t.Fatalf("expected epoch rollback conflict, got %v", err)
	}
}

func TestMissingCausationIsPersistedAsExplicitState(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	event := testEvent()
	event.CausationEventIDs = []string{"evt-parent-late"}
	if _, err := service.Ingest(context.Background(), event); err != nil {
		t.Fatalf("ingest event with late parent: %v", err)
	}
	stored, found := store.StoredEvent(event.EventID)
	if !found || stored.CausalityStatus != "causality_missing" ||
		len(stored.MissingCauseIDs) != 1 || stored.MissingCauseIDs[0] != "evt-parent-late" {
		t.Fatalf("missing causation was not explicit: %#v", stored)
	}
}

func TestEvidenceEventRequiresStartedObservedAndEmittedTimes(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	event := testEvent()
	event.StartedAt = time.Time{}
	if _, err := service.Ingest(context.Background(), event); !ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent) {
		t.Fatalf("expected missing started_at to be rejected, got %v", err)
	}
}

func TestLateCausationThatClosesCycleIsRejected(t *testing.T) {
	t.Parallel()

	store := ledgerstore.New()
	service := ledgersvc.New(store)
	first := testEvent()
	first.CausationEventIDs = []string{"evt-2"}
	if _, err := service.Ingest(context.Background(), first); err != nil {
		t.Fatalf("ingest event with late parent: %v", err)
	}

	second := testEvent()
	second.EventID = "evt-2"
	second.ProducerSequence = 2
	second.CausationEventIDs = []string{"evt-1"}
	second.Envelope = json.RawMessage(`{"answer":"cycle"}`)
	second.PayloadHash = ledgervo.CanonicalPayloadHash(second.Envelope)
	if _, err := service.Ingest(context.Background(), second); !ledgersvc.IsCode(err, ledgersvc.CodeInvalidEvent) {
		t.Fatalf("causation cycle was not rejected: %v", err)
	}
	if store.LedgerCount() != 1 {
		t.Fatalf("cyclic event was written to ledger, count=%d", store.LedgerCount())
	}
}

func testEvent() ledgervo.Event {
	envelope := json.RawMessage(`{"answer":"15991"}`)
	return ledgervo.Event{
		EventID: "evt-1", EventType: "operation.output.observed", SchemaVersion: "3.0.0",
		PayloadHash: ledgervo.CanonicalPayloadHash(envelope),
		Owner: sessionvo.Owner{
			TenantID: "tenant-1", BusinessDomainID: "domain-1",
			ApplicationPrincipalID: "app-1", EffectiveSubjectType: sessionvo.SubjectService,
			EffectiveSubjectID: "agent-1",
		},
		ConversationID: "conv-1", InteractionID: "int-1", OperationID: "op-1", Attempt: 1,
		ProducerID: "context-loader", ProducerStreamID: "stream-1", ProducerEpoch: 1, ProducerSequence: 1,
		StartedAt:  time.Date(2026, 7, 30, 9, 59, 59, 0, time.UTC),
		ObservedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		EmittedAt:  time.Date(2026, 7, 30, 10, 0, 1, 0, time.UTC),
		Envelope:   envelope,
	}
}

type ledgerTestMetrics struct {
	counts map[string]uint64
}

func (m *ledgerTestMetrics) Increment(name string) {
	m.Add(name, 1)
}

func (m *ledgerTestMetrics) Add(name string, delta uint64) {
	m.counts[name] += delta
}

func (m *ledgerTestMetrics) Set(string, float64) {}
