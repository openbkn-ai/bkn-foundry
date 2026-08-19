package isessionstore

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type Transaction interface {
	Now() time.Time
	FindCurrentConversation(owner sessionvo.Owner, externalKey string) (sessionvo.Conversation, bool)
	PeekConversation(conversationID string) (sessionvo.Conversation, bool)
	ListConversationsByIDs(conversationIDs []string) map[string]sessionvo.Conversation
	FindConversation(conversationID string) (sessionvo.Conversation, bool)
	FindIdempotency(scope string, owner sessionvo.Owner, externalKey, idempotencyKey string) (sessionvo.IdempotencyRecord, bool)
	ListConversations(owner sessionvo.Owner, limit int) []sessionvo.Conversation
	SaveConversation(conversation sessionvo.Conversation)
	SaveIdempotency(record sessionvo.IdempotencyRecord)
	FindActiveInteraction(conversationID string) (sessionvo.Interaction, bool)
	FindInteractionByStartKey(conversationID, idempotencyKey string) (sessionvo.Interaction, bool)
	PeekInteraction(interactionID string) (sessionvo.Interaction, bool)
	FindInteraction(interactionID string) (sessionvo.Interaction, bool)
	ListInteractions(conversationID string) []sessionvo.Interaction
	ListInteractionPage(query InteractionPageQuery) InteractionPage
	NextInteractionOrdinal(conversationID string) uint64
	SaveInteraction(interaction sessionvo.Interaction)
	FindOperationByKey(interactionID, operationKey string) (sessionvo.Operation, bool)
	PeekOperation(operationID string) (sessionvo.Operation, bool)
	FindOperation(operationID string) (sessionvo.Operation, bool)
	ListOperations(interactionID string) []sessionvo.Operation
	SaveOperation(operation sessionvo.Operation)
	FindOperationCallFact(operationID string, attempt uint32) (sessionvo.OperationCallFact, bool)
	ListOperationCallFacts(interactionID string) []sessionvo.OperationCallFact
	ListOperationCallFactsByTraceID(traceID string) []sessionvo.OperationCallFact
	ListFirstOperationSourceModulesByTraceIDs(traceIDs []string) map[string]string
	SaveOperationCallFact(fact sessionvo.OperationCallFact)
	PeekReceipt(receiptID string) (sessionvo.Receipt, bool)
	FindReceipt(receiptID string) (sessionvo.Receipt, bool)
	FindReceiptByOperationAttempt(operationID string, attempt uint32) (sessionvo.Receipt, bool)
	ListReceipts(interactionID string) []sessionvo.Receipt
	SaveReceipt(receipt sessionvo.Receipt)
	ListRequests(owner sessionvo.Owner, limit int) []sessionvo.RequestSummary
	FindRequest(owner sessionvo.Owner, requestID string) (sessionvo.RequestSummary, bool)
	ListIdleOneShotConversations(cutoff time.Time, limit int) []sessionvo.Conversation
	ListExpiredActiveInteractions(limit int) []sessionvo.Interaction
	ListAssemblyDueInteractions(limit int) []sessionvo.Interaction
	ListAssemblyRevisions(interactionID string) []sessionvo.AssemblyRevision
	SaveAssemblyRevision(revision sessionvo.AssemblyRevision)
	AppendProjection(mutation sessionvo.ProjectionMutation)
}

// InteractionPageQuery keeps conversation-scoped interaction pagination at
// the lifecycle store. The interaction ordinal is the managed chronological
// order, while callers present the resulting page newest first.
type InteractionPageQuery struct {
	ConversationID string
	Limit          int
	Offset         int
	AfterOrdinal   uint64
}

type InteractionPage struct {
	Entries []sessionvo.Interaction
	Total   int
	HasMore bool
}

type Store interface {
	WithinTransaction(ctx context.Context, fn func(Transaction) error) error
}
