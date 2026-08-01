package ledgerstore

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

type ledgerRecord struct {
	event ledgervo.Event
	ack   ledgervo.DurableAck
}

func (s *Store) ListInteractionEvents(ctx context.Context, owner sessionvo.Owner, interactionID string) ([]ledgervo.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type orderedEvent struct {
		sequence uint64
		event    ledgervo.Event
	}
	ordered := make([]orderedEvent, 0)
	for _, record := range s.ledger {
		if record.event.Owner.Equal(owner) && record.event.InteractionID == interactionID {
			ordered = append(ordered, orderedEvent{sequence: record.ack.IngestSequence, event: record.event})
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sequence < ordered[j].sequence })
	result := make([]ledgervo.Event, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, item.event)
	}
	return result, nil
}

type Store struct {
	mu                  sync.Mutex
	ledger              map[string]ledgerRecord
	streamSequences     map[string]string
	streamEpochs        map[string]uint64
	streamHeads         map[string]uint64
	projectionOutbox    map[string]ledgervo.Event
	conflicts           int
	nextSequence        uint64
	failProjectionWrite bool
}

func New() *Store {
	return &Store{
		ledger:           make(map[string]ledgerRecord),
		streamSequences:  make(map[string]string),
		streamEpochs:     make(map[string]uint64),
		streamHeads:      make(map[string]uint64),
		projectionOutbox: make(map[string]ledgervo.Event),
	}
}

func (s *Store) Commit(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, error) {
	if err := ctx.Err(); err != nil {
		return ledgervo.DurableAck{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.ledger[event.EventID]; found {
		if ledgervo.ImmutableRecordHash(existing.event) != ledgervo.ImmutableRecordHash(event) {
			s.conflicts++
			return ledgervo.DurableAck{}, ievidenceledger.ErrPayloadConflict
		}
		ack := existing.ack
		ack.Replayed = true
		return ack, nil
	}
	events := make([]ledgervo.Event, 0, len(s.ledger)+1)
	for _, record := range s.ledger {
		if record.event.Owner.TenantID == event.Owner.TenantID &&
			record.event.Owner.BusinessDomainID == event.Owner.BusinessDomainID &&
			record.event.InteractionID == event.InteractionID {
			events = append(events, record.event)
		}
	}
	for _, causeID := range event.CausationEventIDs {
		if cause, found := s.ledger[causeID]; found &&
			(cause.event.Owner.TenantID != event.Owner.TenantID ||
				cause.event.Owner.BusinessDomainID != event.Owner.BusinessDomainID ||
				cause.event.InteractionID != event.InteractionID) {
			s.conflicts++
			return ledgervo.DurableAck{}, ievidenceledger.ErrCausalityConflict
		}
	}
	events = append(events, event)
	if ledgervo.HasCausationCycle(events) {
		s.conflicts++
		return ledgervo.DurableAck{}, ievidenceledger.ErrCausalityConflict
	}
	streamKey := event.Owner.TenantID + "\x00" + event.ProducerStreamID + "\x00" +
		strconv.FormatUint(event.ProducerEpoch, 10) + "\x00" + strconv.FormatUint(event.ProducerSequence, 10)
	producerKey := event.Owner.TenantID + "\x00" + event.ProducerStreamID
	if currentEpoch, found := s.streamEpochs[producerKey]; found && event.ProducerEpoch < currentEpoch {
		s.conflicts++
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	epochKey := producerKey + "\x00" + strconv.FormatUint(event.ProducerEpoch, 10)
	if lastSequence, found := s.streamHeads[epochKey]; found && event.ProducerSequence <= lastSequence {
		s.conflicts++
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	if existingEventID, found := s.streamSequences[streamKey]; found && existingEventID != event.EventID {
		s.conflicts++
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	event.CausalityStatus = "complete"
	for _, causeID := range event.CausationEventIDs {
		if _, found := s.ledger[causeID]; !found {
			event.MissingCauseIDs = append(event.MissingCauseIDs, causeID)
		}
	}
	if len(event.MissingCauseIDs) > 0 {
		event.CausalityStatus = "causality_missing"
	}
	if s.failProjectionWrite {
		return ledgervo.DurableAck{}, errors.New("projection outbox write failed")
	}
	s.nextSequence++
	ack := ledgervo.DurableAck{
		EventID: event.EventID, Durable: true, IngestSequence: s.nextSequence, IngestedAt: time.Now().UTC(),
	}
	s.ledger[event.EventID] = ledgerRecord{event: event, ack: ack}
	s.streamSequences[streamKey] = event.EventID
	if event.ProducerEpoch > s.streamEpochs[producerKey] {
		s.streamEpochs[producerKey] = event.ProducerEpoch
	}
	s.streamHeads[epochKey] = event.ProducerSequence
	s.projectionOutbox[event.EventID] = event
	return ack, nil
}

func (s *Store) StoredEvent(eventID string) (ledgervo.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.ledger[eventID]
	return record.event, found
}

func (s *Store) FailProjectionWrites(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failProjectionWrite = fail
}

func (s *Store) LedgerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ledger)
}

func (s *Store) PendingProjectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.projectionOutbox)
}

func (s *Store) ConflictCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conflicts
}
