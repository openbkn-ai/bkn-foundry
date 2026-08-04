package sessionstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

type Store struct {
	mu             sync.Mutex
	now            func() time.Time
	conversations  map[string]sessionvo.Conversation
	interactions   map[string]sessionvo.Interaction
	operations     map[string]sessionvo.Operation
	receipts       map[string]sessionvo.Receipt
	idempotencies  map[string]sessionvo.IdempotencyRecord
	projections    map[uint64]sessionvo.ProjectionMutation
	nextProjection uint64
	revisions      map[string]sessionvo.AssemblyRevision
}

func New() *Store {
	return NewWithClock(time.Now)
}

func NewWithClock(now func() time.Time) *Store {
	return &Store{
		now:           now,
		conversations: make(map[string]sessionvo.Conversation),
		interactions:  make(map[string]sessionvo.Interaction),
		operations:    make(map[string]sessionvo.Operation),
		receipts:      make(map[string]sessionvo.Receipt),
		idempotencies: make(map[string]sessionvo.IdempotencyRecord),
		projections:   make(map[uint64]sessionvo.ProjectionMutation),
		revisions:     make(map[string]sessionvo.AssemblyRevision),
	}
}

func (s *Store) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(memoryTransaction{s: s, now: s.now().UTC()})
}

type memoryTransaction struct {
	s   *Store
	now time.Time
}

func (tx memoryTransaction) Now() time.Time {
	return tx.now
}

func (tx memoryTransaction) FindCurrentConversation(owner sessionvo.Owner, externalKey string) (sessionvo.Conversation, bool) {
	var current sessionvo.Conversation
	found := false
	for _, conversation := range tx.s.conversations {
		if !conversation.Owner.Equal(owner) || conversation.ExternalConversationKey != externalKey {
			continue
		}
		if !found || conversation.Generation > current.Generation {
			current = conversation
			found = true
		}
	}
	return current, found
}

func (tx memoryTransaction) SaveConversation(conversation sessionvo.Conversation) {
	tx.s.conversations[conversation.ID] = conversation
}

func (tx memoryTransaction) FindConversation(conversationID string) (sessionvo.Conversation, bool) {
	conversation, found := tx.s.conversations[conversationID]
	return conversation, found
}

func (tx memoryTransaction) FindIdempotency(
	scope string,
	owner sessionvo.Owner,
	externalKey string,
	idempotencyKey string,
) (sessionvo.IdempotencyRecord, bool) {
	record, found := tx.s.idempotencies[idempotencyRecordKey(scope, owner, externalKey, idempotencyKey)]
	return record, found
}

func (tx memoryTransaction) SaveIdempotency(record sessionvo.IdempotencyRecord) {
	tx.s.idempotencies[idempotencyRecordKey(
		record.Scope, record.Owner, record.ExternalConversationKey, record.IdempotencyKey,
	)] = record
}

func idempotencyRecordKey(scope string, owner sessionvo.Owner, externalKey, idempotencyKey string) string {
	return scope + "\x00" + owner.Key() + "\x00" + externalKey + "\x00" + idempotencyKey
}

func (tx memoryTransaction) ListConversations(owner sessionvo.Owner, limit int) []sessionvo.Conversation {
	result := make([]sessionvo.Conversation, 0)
	for _, conversation := range tx.s.conversations {
		if conversation.Owner.Equal(owner) {
			result = append(result, conversation)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (tx memoryTransaction) FindActiveInteraction(conversationID string) (sessionvo.Interaction, bool) {
	for _, interaction := range tx.s.interactions {
		if interaction.ConversationID == conversationID && interaction.ExecutionStatus == sessionvo.InteractionActive {
			return interaction, true
		}
	}
	return sessionvo.Interaction{}, false
}

func (tx memoryTransaction) FindInteraction(interactionID string) (sessionvo.Interaction, bool) {
	interaction, found := tx.s.interactions[interactionID]
	return interaction, found
}

func (tx memoryTransaction) PeekInteraction(interactionID string) (sessionvo.Interaction, bool) {
	return tx.FindInteraction(interactionID)
}

func (tx memoryTransaction) NextInteractionOrdinal(conversationID string) uint64 {
	var max uint64
	for _, interaction := range tx.s.interactions {
		if interaction.ConversationID == conversationID && interaction.Ordinal > max {
			max = interaction.Ordinal
		}
	}
	return max + 1
}

func (tx memoryTransaction) SaveInteraction(interaction sessionvo.Interaction) {
	tx.s.interactions[interaction.ID] = interaction
}

func (tx memoryTransaction) FindOperationByKey(interactionID, operationKey string) (sessionvo.Operation, bool) {
	for _, operation := range tx.s.operations {
		if operation.InteractionID == interactionID && operation.OperationKey == operationKey {
			return operation, true
		}
	}
	return sessionvo.Operation{}, false
}

func (tx memoryTransaction) SaveOperation(operation sessionvo.Operation) {
	tx.s.operations[operation.ID] = operation
}

func (tx memoryTransaction) FindOperation(operationID string) (sessionvo.Operation, bool) {
	operation, found := tx.s.operations[operationID]
	return operation, found
}

func (tx memoryTransaction) PeekOperation(operationID string) (sessionvo.Operation, bool) {
	return tx.FindOperation(operationID)
}

func (tx memoryTransaction) ListOperations(interactionID string) []sessionvo.Operation {
	var result []sessionvo.Operation
	for _, operation := range tx.s.operations {
		if operation.InteractionID == interactionID {
			result = append(result, operation)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (tx memoryTransaction) FindReceipt(receiptID string) (sessionvo.Receipt, bool) {
	receipt, found := tx.s.receipts[receiptID]
	return receipt, found
}

func (tx memoryTransaction) PeekReceipt(receiptID string) (sessionvo.Receipt, bool) {
	return tx.FindReceipt(receiptID)
}

func (tx memoryTransaction) FindReceiptByOperationAttempt(operationID string, attempt uint32) (sessionvo.Receipt, bool) {
	for _, receipt := range tx.s.receipts {
		if receipt.OperationID == operationID && receipt.Attempt == attempt {
			return receipt, true
		}
	}
	return sessionvo.Receipt{}, false
}

func (tx memoryTransaction) ListReceipts(interactionID string) []sessionvo.Receipt {
	var result []sessionvo.Receipt
	for _, receipt := range tx.s.receipts {
		if receipt.InteractionID == interactionID {
			result = append(result, receipt)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (tx memoryTransaction) SaveReceipt(receipt sessionvo.Receipt) {
	tx.s.receipts[receipt.ID] = receipt
}

func (tx memoryTransaction) ListRequests(owner sessionvo.Owner, limit int) []sessionvo.RequestSummary {
	type accumulator struct {
		summary    sessionvo.RequestSummary
		operations map[string]bool
		traces     map[string]bool
	}
	grouped := make(map[string]*accumulator)
	for _, receipt := range tx.s.receipts {
		if receipt.RequestID == "" || !receipt.Owner.Equal(owner) {
			continue
		}
		item := grouped[receipt.RequestID]
		if item == nil {
			item = &accumulator{
				summary: sessionvo.RequestSummary{
					RequestID: receipt.RequestID, ConversationID: receipt.ConversationID,
					InteractionID: receipt.InteractionID, UpdatedAt: receipt.IssuedAt,
				},
				operations: make(map[string]bool), traces: make(map[string]bool),
			}
			grouped[receipt.RequestID] = item
		}
		item.summary.ReceiptCount++
		item.operations[receipt.OperationID] = true
		if receipt.TraceID != "" {
			item.traces[receipt.TraceID] = true
		}
		if receipt.TerminalAt != nil && receipt.TerminalAt.After(item.summary.UpdatedAt) {
			item.summary.UpdatedAt = *receipt.TerminalAt
		}
	}
	result := make([]sessionvo.RequestSummary, 0, len(grouped))
	for _, item := range grouped {
		item.summary.OperationCount = len(item.operations)
		for traceID := range item.traces {
			item.summary.TraceIDs = append(item.summary.TraceIDs, traceID)
		}
		sort.Strings(item.summary.TraceIDs)
		result = append(result, item.summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].RequestID > result[j].RequestID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (tx memoryTransaction) FindRequest(owner sessionvo.Owner, requestID string) (sessionvo.RequestSummary, bool) {
	for _, item := range tx.ListRequests(owner, 0) {
		if item.RequestID == requestID {
			return item, true
		}
	}
	return sessionvo.RequestSummary{}, false
}

func (tx memoryTransaction) ListIdleOneShotConversations(
	cutoff time.Time,
	limit int,
) []sessionvo.Conversation {
	result := make([]sessionvo.Conversation, 0)
	for _, conversation := range tx.s.conversations {
		if !conversation.OneShot || conversation.Status != sessionvo.ConversationActive ||
			conversation.UpdatedAt.After(cutoff) || tx.hasInteraction(conversation.ID) {
			continue
		}
		result = append(result, conversation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.Before(result[j].UpdatedAt)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (tx memoryTransaction) hasInteraction(conversationID string) bool {
	for _, interaction := range tx.s.interactions {
		if interaction.ConversationID == conversationID {
			return true
		}
	}
	return false
}

func (tx memoryTransaction) ListExpiredActiveInteractions(limit int) []sessionvo.Interaction {
	result := make([]sessionvo.Interaction, 0)
	for _, interaction := range tx.s.interactions {
		if interaction.ExecutionStatus != sessionvo.InteractionActive || interaction.LeaseExpiresAt.After(tx.now) {
			continue
		}
		result = append(result, interaction)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func (tx memoryTransaction) ListAssemblyDueInteractions(limit int) []sessionvo.Interaction {
	var result []sessionvo.Interaction
	for _, interaction := range tx.s.interactions {
		if interaction.EvidenceStatus != sessionvo.EvidenceAssembling ||
			interaction.ClosureManifest == nil || interaction.ClosureManifest.AssemblerDeadline == nil ||
			interaction.ClosureManifest.AssemblerDeadline.After(tx.now) {
			continue
		}
		result = append(result, interaction)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func (tx memoryTransaction) AppendProjection(mutation sessionvo.ProjectionMutation) {
	tx.s.nextProjection++
	tx.s.projections[tx.s.nextProjection] = mutation
}

func (tx memoryTransaction) ListAssemblyRevisions(interactionID string) []sessionvo.AssemblyRevision {
	var result []sessionvo.AssemblyRevision
	for _, revision := range tx.s.revisions {
		if revision.InteractionID == interactionID {
			result = append(result, revision)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RevisionNo < result[j].RevisionNo })
	return result
}

func (tx memoryTransaction) SaveAssemblyRevision(revision sessionvo.AssemblyRevision) {
	tx.s.revisions[revision.ID] = revision
}

func (s *Store) Lease(ctx context.Context, limit int, _ time.Duration) ([]iprojectionoutbox.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []iprojectionoutbox.Item
	for id, mutation := range s.projections {
		result = append(result, iprojectionoutbox.Item{
			ID: id, AggregateType: mutation.AggregateType, AggregateID: mutation.AggregateID,
			AggregateVersion: mutation.AggregateVersion,
			EventType:        mutation.EventType, EventID: mutation.EventID, Payload: mutation.Payload,
		})
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *Store) MarkDelivered(_ context.Context, item iprojectionoutbox.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.projections, item.ID)
	return nil
}

func (s *Store) MarkRetry(_ context.Context, _ iprojectionoutbox.Item, _ string, _ time.Time) error {
	return nil
}

func (s *Store) MoveToDLQ(_ context.Context, item iprojectionoutbox.Item, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.projections, item.ID)
	return nil
}

func (s *Store) PendingProjectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.projections)
}
