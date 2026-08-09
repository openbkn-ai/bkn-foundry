package sessionsvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icoremetrics"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var ErrInvalidOwner = errors.New("trusted conversation owner is incomplete")

const (
	maxOperationsPerInteraction   = 128
	maxClaimsPerInteraction       = 32
	maxEvidenceRefsPerInteraction = 2048
	defaultAssemblyTimeout        = 5 * time.Minute
)

type Options struct {
	Now                     func() time.Time
	NewID                   func(prefix string) string
	EvidenceCollectionState func() string
	AssemblyTimeout         time.Duration
	Metrics                 icoremetrics.Recorder
}

type Service struct {
	store                   isessionstore.Store
	now                     func() time.Time
	newID                   func(string) string
	evidenceCollectionState func() string
	assemblyTimeout         time.Duration
	metrics                 icoremetrics.Recorder
}

func New(store isessionstore.Store, options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newID := options.NewID
	if newID == nil {
		newID = randomID
	}
	evidenceCollectionState := options.EvidenceCollectionState
	if evidenceCollectionState == nil {
		evidenceCollectionState = func() string { return "enabled" }
	}
	metrics := options.Metrics
	if metrics == nil {
		metrics = icoremetrics.Noop{}
	}
	assemblyTimeout := options.AssemblyTimeout
	if assemblyTimeout <= 0 {
		assemblyTimeout = defaultAssemblyTimeout
	}
	return &Service{
		store: store, now: now, newID: newID,
		evidenceCollectionState: evidenceCollectionState,
		assemblyTimeout:         assemblyTimeout,
		metrics:                 metrics,
	}
}

type EnsureConversationCommand struct {
	Owner                   sessionvo.Owner
	ExternalConversationKey string
	IdempotencyKey          string
	OneShot                 bool
}

type CloseConversationCommand struct {
	Owner          sessionvo.Owner
	ConversationID string
	IdempotencyKey string
}

type ResumeConversationCommand struct {
	Owner          sessionvo.Owner
	ConversationID string
}

type StartInteractionCommand struct {
	Owner          sessionvo.Owner
	ConversationID string
	IdempotencyKey string
	RequestHash    string
	AgentName      string
	LeaseDuration  time.Duration
}

type TerminateInteractionCommand struct {
	Owner                  sessionvo.Owner
	InteractionID          string
	Status                 sessionvo.InteractionStatus
	TerminalIdempotencyKey string
	LeaseToken             string
	LeaseEpoch             uint64
	Manifest               sessionvo.ClosureManifest
	DeriveManifest         bool
}

type EnsureOperationCommand struct {
	Owner             sessionvo.Owner
	ConversationID    string
	InteractionID     string
	OperationKey      string
	ToolName          string
	Protocol          sessionvo.OperationProtocol
	SourceModule      string
	Input             sessionvo.PayloadEnvelope
	ParentOperationID string
	CausationEventIDs []string
	Required          bool
	LeaseToken        string
	LeaseEpoch        uint64
}

type EnsureOperationResult struct {
	Operation sessionvo.Operation
	Receipt   sessionvo.Receipt
	Created   bool
	Execute   bool
}

type StartAttemptCommand struct {
	Owner       sessionvo.Owner
	OperationID string
	LeaseToken  string
	LeaseEpoch  uint64
}

type FinishAttemptCommand struct {
	Owner                sessionvo.Owner
	OperationID          string
	Attempt              uint32
	ReceiptID            string
	Output               sessionvo.PayloadEnvelope
	Error                sessionvo.PayloadEnvelope
	EvidenceDurability   sessionvo.EvidenceDurability
	Retryable            bool
	RequestID            string
	TraceID              string
	SpanID               string
	ObservedEvidenceRefs []string
	BusinessRefs         []sessionvo.BusinessRef
	ArtifactRefs         []string
	PartialReasons       []string
}

func (s *Service) EnsureCurrentConversation(ctx context.Context, command EnsureConversationCommand) (sessionvo.Conversation, error) {
	if err := validateOwner(command.Owner); err != nil {
		return sessionvo.Conversation{}, err
	}
	if command.ExternalConversationKey == "" {
		return sessionvo.Conversation{}, domainError(CodeConversationRequired, "external conversation key is required")
	}

	var result sessionvo.Conversation
	created := false
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		current, found := tx.FindCurrentConversation(command.Owner, command.ExternalConversationKey)
		if found && current.Status == sessionvo.ConversationActive {
			result = current
			return nil
		}

		generation := uint64(1)
		if found {
			generation = current.Generation + 1
		}
		now := tx.Now()
		result = sessionvo.Conversation{
			ID:                      s.newID("conv"),
			Owner:                   command.Owner,
			ExternalConversationKey: command.ExternalConversationKey,
			Generation:              generation,
			Status:                  sessionvo.ConversationActive,
			OneShot:                 command.OneShot,
			RowVersion:              1,
			CreatedAt:               now,
			UpdatedAt:               now,
		}
		created = true
		tx.SaveConversation(result)
		return s.appendProjection(tx, "conversation", result.ID, "conversation.created", result)
	})
	s.observeLifecycleError(err)
	if err == nil && created {
		s.metrics.Increment(icoremetrics.ConversationsTotal)
	}
	return result, err
}

func (s *Service) CreateNewGeneration(ctx context.Context, command EnsureConversationCommand) (sessionvo.Conversation, error) {
	if err := validateOwner(command.Owner); err != nil {
		return sessionvo.Conversation{}, err
	}
	if command.ExternalConversationKey == "" {
		return sessionvo.Conversation{}, domainError(CodeConversationRequired, "external conversation key is required")
	}
	if command.IdempotencyKey == "" {
		return sessionvo.Conversation{}, domainError(CodeConversationRequired, "idempotency key is required")
	}
	const idempotencyScope = "conversation.create_new_generation"
	requestHash := hashValue(struct {
		ExternalConversationKey string `json:"external_conversation_key"`
		OneShot                 bool   `json:"one_shot"`
	}{
		ExternalConversationKey: command.ExternalConversationKey,
		OneShot:                 command.OneShot,
	})
	var result sessionvo.Conversation
	created := false
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		if record, found := tx.FindIdempotency(
			idempotencyScope, command.Owner, command.ExternalConversationKey, command.IdempotencyKey,
		); found {
			if record.RequestHash != requestHash || record.ResourceType != "conversation" {
				return domainError(CodeIdempotencyConflict, "idempotency key was already used with a different request")
			}
			conversation, found := tx.FindConversation(record.ResourceID)
			if !found {
				return domainError(CodeConversationNotFound, "idempotency result conversation was not found")
			}
			result = conversation
			return nil
		}
		current, found := tx.FindCurrentConversation(command.Owner, command.ExternalConversationKey)
		generation := uint64(1)
		now := tx.Now()
		if found {
			if current.Status == sessionvo.ConversationActive {
				if _, active := tx.FindActiveInteraction(current.ID); active {
					return domainError(CodeInteractionInProgress, "complete or cancel the active interaction before creating a new generation")
				}
				current.Status = sessionvo.ConversationClosed
				current.RowVersion++
				current.UpdatedAt = now
				current.ClosedAt = &now
				tx.SaveConversation(current)
				if err := s.appendProjection(tx, "conversation", current.ID, "conversation.closed", current); err != nil {
					return err
				}
			}
			generation = current.Generation + 1
		}
		result = sessionvo.Conversation{
			ID: s.newID("conv"), Owner: command.Owner,
			ExternalConversationKey: command.ExternalConversationKey,
			Generation:              generation, Status: sessionvo.ConversationActive,
			OneShot: command.OneShot, RowVersion: 1, CreatedAt: now, UpdatedAt: now,
		}
		created = true
		tx.SaveConversation(result)
		tx.SaveIdempotency(sessionvo.IdempotencyRecord{
			Scope: idempotencyScope, Owner: command.Owner,
			ExternalConversationKey: command.ExternalConversationKey,
			IdempotencyKey:          command.IdempotencyKey, RequestHash: requestHash,
			ResourceType: "conversation", ResourceID: result.ID, CreatedAt: now,
		})
		return s.appendProjection(tx, "conversation", result.ID, "conversation.created", result)
	})
	s.observeLifecycleError(err)
	if err == nil && created {
		s.metrics.Increment(icoremetrics.ConversationsTotal)
	}
	return result, err
}

func (s *Service) GetConversation(ctx context.Context, owner sessionvo.Owner, conversationID string) (sessionvo.Conversation, error) {
	var result sessionvo.Conversation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversation, err := ownedConversation(tx, owner, conversationID)
		if err != nil {
			return err
		}
		result = conversation
		return nil
	})
	s.observeLifecycleError(err)
	return result, err
}

func (s *Service) ResumeConversation(ctx context.Context, command ResumeConversationCommand) (sessionvo.Conversation, error) {
	var result sessionvo.Conversation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversation, err := ownedConversation(tx, command.Owner, command.ConversationID)
		if err != nil {
			return err
		}
		switch conversation.Status {
		case sessionvo.ConversationActive:
			result = conversation
			return nil
		case sessionvo.ConversationExpired:
			return domainError(CodeConversationExpired, "conversation expired; create or resume another conversation")
		default:
			return domainError(CodeConversationClosed, "conversation is closed; create a new generation")
		}
	})
	return result, err
}

func (s *Service) ListConversations(ctx context.Context, owner sessionvo.Owner, limit int) ([]sessionvo.Conversation, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var result []sessionvo.Conversation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		result = tx.ListConversations(owner, limit)
		return nil
	})
	return result, err
}

func (s *Service) ListRequests(ctx context.Context, owner sessionvo.Owner, limit int) ([]sessionvo.RequestSummary, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var result []sessionvo.RequestSummary
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		result = tx.ListRequests(owner, limit)
		return nil
	})
	return result, err
}

func (s *Service) GetRequest(ctx context.Context, owner sessionvo.Owner, requestID string) (sessionvo.RequestSummary, error) {
	if err := validateOwner(owner); err != nil {
		return sessionvo.RequestSummary{}, err
	}
	var result sessionvo.RequestSummary
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		value, found := tx.FindRequest(owner, requestID)
		if !found {
			return domainError(CodeResourceNotDisclosed, "request was not found in the authorized scope")
		}
		result = value
		return nil
	})
	return result, err
}

func (s *Service) GetInteraction(ctx context.Context, owner sessionvo.Owner, interactionID string) (sessionvo.Interaction, error) {
	var result sessionvo.Interaction
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		interaction, found := tx.PeekInteraction(interactionID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, interaction.ConversationID); err != nil {
			return err
		}
		result = interaction
		return nil
	})
	return result, err
}

func (s *Service) GetOperation(ctx context.Context, owner sessionvo.Owner, operationID string) (sessionvo.Operation, error) {
	var result sessionvo.Operation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operation, found := tx.PeekOperation(operationID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, operation.ConversationID); err != nil {
			return err
		}
		result = operation
		return nil
	})
	return result, err
}

func (s *Service) ListOperationCallFacts(
	ctx context.Context,
	owner sessionvo.Owner,
	interactionID string,
) ([]sessionvo.OperationCallFact, error) {
	var result []sessionvo.OperationCallFact
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		interaction, found := tx.PeekInteraction(interactionID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, interaction.ConversationID); err != nil {
			return err
		}
		result = tx.ListOperationCallFacts(interactionID)
		if result == nil {
			result = []sessionvo.OperationCallFact{}
		}
		return nil
	})
	return result, err
}

func (s *Service) GetOperationCallFact(
	ctx context.Context,
	owner sessionvo.Owner,
	operationID string,
	attempt uint32,
) (sessionvo.OperationCallFact, error) {
	var result sessionvo.OperationCallFact
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operation, found := tx.PeekOperation(operationID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, operation.ConversationID); err != nil {
			return err
		}
		fact, found := tx.FindOperationCallFact(operationID, attempt)
		if !found {
			return resourceNotDisclosed()
		}
		result = fact
		return nil
	})
	return result, err
}

func (s *Service) GetReceipt(ctx context.Context, owner sessionvo.Owner, receiptID string) (sessionvo.Receipt, error) {
	var result sessionvo.Receipt
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		receipt, found := tx.PeekReceipt(receiptID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, receipt.ConversationID); err != nil {
			return err
		}
		result = receipt
		return nil
	})
	return result, err
}

func (s *Service) CloseConversation(ctx context.Context, command CloseConversationCommand) (sessionvo.Conversation, error) {
	var result sessionvo.Conversation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversation, err := ownedConversation(tx, command.Owner, command.ConversationID)
		if err != nil {
			return err
		}
		if conversation.Status == sessionvo.ConversationClosed {
			result = conversation
			return nil
		}
		if conversation.Status == sessionvo.ConversationExpired {
			return domainError(CodeConversationExpired, "expired conversation cannot be closed")
		}
		if _, found := tx.FindActiveInteraction(conversation.ID); found {
			return domainError(CodeInteractionInProgress, "complete or cancel the active interaction before closing the conversation")
		}
		now := tx.Now()
		conversation.Status = sessionvo.ConversationClosed
		conversation.RowVersion++
		conversation.UpdatedAt = now
		conversation.ClosedAt = &now
		tx.SaveConversation(conversation)
		if err := s.appendProjection(tx, "conversation", conversation.ID, "conversation.closed", conversation); err != nil {
			return err
		}
		result = conversation
		return nil
	})
	s.observeLifecycleError(err)
	return result, err
}

func (s *Service) StartInteraction(ctx context.Context, command StartInteractionCommand) (sessionvo.Interaction, error) {
	if command.IdempotencyKey == "" {
		return sessionvo.Interaction{}, domainError(CodeInteractionRequired, "idempotency key is required")
	}
	agentName := strings.TrimSpace(command.AgentName)
	if utf8.RuneCountInString(agentName) > 128 {
		return sessionvo.Interaction{}, domainError(CodeAgentNameInvalid, "agent_name must not exceed 128 characters")
	}
	var result sessionvo.Interaction
	created := false
	requestHash := command.RequestHash
	if requestHash == "" {
		requestHash = hashValue(struct{}{})
	}
	const idempotencyScope = "interaction.start"
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		conversation, err := ownedConversation(tx, command.Owner, command.ConversationID)
		if err != nil {
			return err
		}
		if err := requireActiveConversation(conversation); err != nil {
			return err
		}
		if conversation.AgentName != "" && agentName != "" && conversation.AgentName != agentName {
			return domainError(CodeAgentNameConflict, "agent_name does not match the conversation declaration")
		}
		if record, found := tx.FindIdempotency(
			idempotencyScope, command.Owner, conversation.ID, command.IdempotencyKey,
		); found {
			if record.RequestHash != requestHash || record.ResourceType != "interaction" {
				return domainError(CodeIdempotencyConflict, "idempotency key was already used with a different request")
			}
			interaction, found := tx.FindInteraction(record.ResourceID)
			if !found {
				return domainError(CodeInteractionRequired, "idempotency result interaction was not found")
			}
			result = interaction
			return nil
		}
		// Keep replay semantics for rows created before idempotency records existed.
		if interaction, found := tx.FindInteractionByStartKey(conversation.ID, command.IdempotencyKey); found {
			result = interaction
			return nil
		}
		if conversation.AgentName == "" && agentName != "" {
			conversation.AgentName = agentName
			conversation.RowVersion++
			conversation.UpdatedAt = tx.Now()
			tx.SaveConversation(conversation)
			if err := s.appendProjection(tx, "conversation", conversation.ID, "conversation.agent_name_declared", conversation); err != nil {
				return err
			}
		}
		if active, found := tx.FindActiveInteraction(conversation.ID); found {
			return &DomainError{
				Code: CodeInteractionInProgress, Message: "the conversation already has an active interaction",
				CurrentStatus: string(active.ExecutionStatus), CurrentInteractionID: active.ID,
			}
		}
		leaseDuration := command.LeaseDuration
		if leaseDuration <= 0 {
			leaseDuration = 5 * time.Minute
		}
		nextOrdinal := tx.NextInteractionOrdinal(conversation.ID)
		now := tx.Now()
		result = sessionvo.Interaction{
			ID:                  s.newID("int"),
			ConversationID:      conversation.ID,
			Ordinal:             nextOrdinal,
			ExecutionStatus:     sessionvo.InteractionActive,
			EvidenceStatus:      sessionvo.EvidenceNotApplicable,
			StartIdempotencyKey: command.IdempotencyKey,
			LeaseToken:          s.newID("lease"),
			LeaseEpoch:          1,
			LeaseVersion:        1,
			LeaseExpiresAt:      now.Add(leaseDuration),
			RowVersion:          1,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		created = true
		tx.SaveInteraction(result)
		tx.SaveIdempotency(sessionvo.IdempotencyRecord{
			Scope: idempotencyScope, Owner: command.Owner, ExternalConversationKey: conversation.ID,
			IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash,
			ResourceType: "interaction", ResourceID: result.ID, CreatedAt: now,
		})
		return s.appendProjection(tx, "interaction", result.ID, "interaction.started", result)
	})
	s.observeLifecycleError(err)
	if err == nil && created {
		s.metrics.Increment(icoremetrics.InteractionsTotal)
	}
	return result, err
}

func (s *Service) TerminateInteraction(ctx context.Context, command TerminateInteractionCommand) (sessionvo.Interaction, error) {
	if !validTerminalStatus(command.Status) {
		return sessionvo.Interaction{}, domainError(CodeInteractionTerminal, "requested status is not terminal")
	}
	if !command.DeriveManifest && (command.LeaseToken == "" || command.LeaseEpoch == 0) {
		return sessionvo.Interaction{}, domainError(CodeTerminalConflict, "interaction lease token and epoch are required")
	}
	if command.TerminalIdempotencyKey == "" {
		return sessionvo.Interaction{}, domainError(CodeIdempotencyConflict, "terminal idempotency key is required")
	}
	if s.evidenceCollectionState() == "not_collected_due_to_license" {
		command.Manifest.SystemPartialReasons = appendUnique(
			command.Manifest.SystemPartialReasons, "not_collected_due_to_license",
		)
	}
	var result sessionvo.Interaction
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		interactionRef, found := tx.PeekInteraction(command.InteractionID)
		if !found {
			return resourceNotDisclosed()
		}
		conversation, err := ownedConversation(tx, command.Owner, interactionRef.ConversationID)
		if err != nil {
			return err
		}
		interaction, found := tx.FindInteraction(command.InteractionID)
		if !found || interaction.ConversationID != conversation.ID {
			return resourceNotDisclosed()
		}
		if interaction.IsTerminal() && command.DeriveManifest {
			if managedTerminalReplayMatches(interaction, command) {
				result = interaction
				return nil
			}
			return &DomainError{
				Code: CodeTerminalConflict, Message: "another terminal transition already won",
				CurrentStatus: string(interaction.ExecutionStatus),
			}
		}
		if command.DeriveManifest {
			command.LeaseToken = interaction.LeaseToken
			command.LeaseEpoch = interaction.LeaseEpoch
			command.Manifest = deriveClosureManifest(tx, interaction.ID, command.Manifest)
		}
		if command.Manifest.AssemblerDeadline == nil && hasRequiredPendingReceipt(tx, command.Manifest) {
			deadline := tx.Now().Add(s.assemblyTimeout)
			command.Manifest.AssemblerDeadline = &deadline
		}
		payloadHash := hashValue(struct {
			Status   sessionvo.InteractionStatus
			Manifest sessionvo.ClosureManifest
		}{command.Status, command.Manifest})
		if interaction.IsTerminal() {
			if interaction.TerminalIdempotencyKey == command.TerminalIdempotencyKey &&
				interaction.TerminalPayloadHash == payloadHash {
				result = interaction
				return nil
			}
			return &DomainError{
				Code: CodeTerminalConflict, Message: "another terminal transition already won",
				CurrentStatus: string(interaction.ExecutionStatus),
			}
		}
		if !interaction.LeaseExpiresAt.After(tx.Now()) {
			return &DomainError{
				Code: CodeTerminalConflict, Message: "interaction lease has expired",
				CurrentStatus: string(interaction.ExecutionStatus),
			}
		}
		if command.LeaseToken != interaction.LeaseToken || command.LeaseEpoch != interaction.LeaseEpoch {
			return &DomainError{
				Code: CodeTerminalConflict, Message: "stale interaction lease was fenced",
				CurrentStatus: string(interaction.ExecutionStatus),
			}
		}
		if err := validateClosureManifest(tx, interaction, command.Manifest); err != nil {
			return err
		}
		now := tx.Now()
		interaction.ExecutionStatus = command.Status
		interaction.EvidenceStatus = evidenceStatusAtTermination(tx, command.Manifest)
		interaction.TerminalIdempotencyKey = command.TerminalIdempotencyKey
		interaction.TerminalPayloadHash = payloadHash
		interaction.ClosureManifest = &command.Manifest
		interaction.RowVersion++
		interaction.LeaseVersion++
		interaction.UpdatedAt = now
		interaction.TerminalAt = &now
		tx.SaveInteraction(interaction)
		if interaction.EvidenceStatus == sessionvo.EvidenceComplete ||
			interaction.EvidenceStatus == sessionvo.EvidencePartial ||
			interaction.EvidenceStatus == sessionvo.EvidenceFailed {
			trigger := "completion"
			if command.Manifest.AssemblerDeadline != nil && !command.Manifest.AssemblerDeadline.After(tx.Now()) {
				trigger = "deadline"
			}
			if err := s.freezeAssemblyRevision(tx, interaction, command.Manifest, trigger); err != nil {
				return err
			}
		}
		if err := s.appendProjection(tx, "interaction", interaction.ID, "interaction."+string(command.Status), interaction); err != nil {
			return err
		}
		if conversation.OneShot {
			conversation.Status = sessionvo.ConversationClosed
			conversation.RowVersion++
			conversation.UpdatedAt = now
			conversation.ClosedAt = &now
			tx.SaveConversation(conversation)
			if err := s.appendProjection(tx, "conversation", conversation.ID, "conversation.closed", conversation); err != nil {
				return err
			}
		}
		result = interaction
		return nil
	})
	s.observeLifecycleError(err)
	return result, err
}

func managedTerminalReplayMatches(
	interaction sessionvo.Interaction,
	command TerminateInteractionCommand,
) bool {
	if interaction.TerminalIdempotencyKey != command.TerminalIdempotencyKey ||
		interaction.ExecutionStatus != command.Status || interaction.ClosureManifest == nil {
		return false
	}
	committed := interaction.ClosureManifest
	return committed.Version == command.Manifest.Version &&
		committed.AnswerArtifactRef == command.Manifest.AnswerArtifactRef &&
		committed.CompletionReason == command.Manifest.CompletionReason &&
		sameStrings(committed.Claims, command.Manifest.Claims)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func deriveClosureManifest(
	tx isessionstore.Transaction,
	interactionID string,
	manifest sessionvo.ClosureManifest,
) sessionvo.ClosureManifest {
	receipts := tx.ListReceipts(interactionID)
	requiredByOperation := make(map[string]bool, len(receipts))
	manifest.ExpectedReceipts = make([]sessionvo.ExpectedReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		requiredByOperation[receipt.OperationID] = receipt.Required
		manifest.ExpectedReceipts = append(manifest.ExpectedReceipts, sessionvo.ExpectedReceipt{
			ReceiptID: receipt.ID,
			Required:  receipt.Required,
		})
	}
	operations := tx.ListOperations(interactionID)
	manifest.ExpectedOperations = make([]sessionvo.ExpectedOperation, 0, len(operations))
	for _, operation := range operations {
		manifest.ExpectedOperations = append(manifest.ExpectedOperations, sessionvo.ExpectedOperation{
			OperationID: operation.ID,
			Required:    requiredByOperation[operation.ID],
		})
	}
	return manifest
}

func (s *Service) EnsureOperation(
	ctx context.Context,
	command EnsureOperationCommand,
) (sessionvo.Operation, sessionvo.Receipt, error) {
	result, err := s.EnsureOperationWithDisposition(ctx, command)
	return result.Operation, result.Receipt, err
}

func (s *Service) EnsureOperationWithDisposition(
	ctx context.Context,
	command EnsureOperationCommand,
) (EnsureOperationResult, error) {
	if command.Protocol == "" {
		command.Protocol = sessionvo.ProtocolInternal
	}
	if command.SourceModule == "" && command.Protocol == sessionvo.ProtocolInternal {
		command.SourceModule = "agent-observability"
	}
	if command.OperationKey == "" || command.ToolName == "" ||
		!command.Protocol.IsValid() || strings.TrimSpace(command.SourceModule) == "" {
		return EnsureOperationResult{}, domainError(
			CodeOperationRequired,
			"operation key, tool name, protocol, source module and input are required",
		)
	}
	command.CausationEventIDs = canonicalStringSet(command.CausationEventIDs)
	var input sessionvo.PayloadEnvelope
	var err error
	input, err = sessionvo.NormalizePayloadEnvelope(command.Input)
	if err != nil {
		return EnsureOperationResult{}, domainError(CodeOperationRequired, "operation input envelope is invalid")
	}
	var operation sessionvo.Operation
	var receipt sessionvo.Receipt
	created := false
	execute := false
	err = s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operation = sessionvo.Operation{}
		receipt = sessionvo.Receipt{}
		created = false
		execute = false
		conversation, err := ownedConversation(tx, command.Owner, command.ConversationID)
		if err != nil {
			return err
		}
		if err := requireActiveConversation(conversation); err != nil {
			return err
		}
		interaction, found := tx.FindInteraction(command.InteractionID)
		if !found || interaction.ConversationID != conversation.ID {
			return resourceNotDisclosed()
		}
		if interaction.ExecutionStatus != sessionvo.InteractionActive ||
			!interaction.LeaseExpiresAt.After(tx.Now()) {
			return domainError(CodeInteractionTerminal, "interaction is already terminal")
		}
		if command.LeaseToken == "" || command.LeaseEpoch == 0 ||
			command.LeaseToken != interaction.LeaseToken ||
			command.LeaseEpoch != interaction.LeaseEpoch {
			return domainError(CodeTerminalConflict, "stale interaction lease was fenced")
		}
		if command.ParentOperationID != "" {
			parent, found := tx.FindOperation(command.ParentOperationID)
			if !found {
				return resourceNotDisclosed()
			}
			if _, err := ownedConversation(tx, command.Owner, parent.ConversationID); err != nil {
				return err
			}
			if parent.InteractionID != interaction.ID {
				return resourceNotDisclosed()
			}
		}
		// Capacity is checked before the lease is renewed, and only for a key that
		// would create a new operation. Renewing first leaves a full interaction
		// holding a fresh lease on every rejected call: the reaper then never sees
		// it expire, and a caller that keeps retrying — as it is documented to —
		// keeps it alive forever. The in-memory store makes that permanent, since
		// it has no rollback and keeps the renewal written by the failed call.
		if _, found := tx.FindOperationByKey(interaction.ID, command.OperationKey); !found &&
			len(tx.ListOperations(interaction.ID)) >= maxOperationsPerInteraction {
			return domainError(CodeOperationRequired, "interaction operation limit of 128 was reached")
		}
		renewInteractionLease(tx, &interaction)
		if existing, found := tx.FindOperationByKey(interaction.ID, command.OperationKey); found {
			if existing.ToolName != command.ToolName ||
				existing.ParentOperationID != command.ParentOperationID ||
				!slices.Equal(existing.CausationEventIDs, command.CausationEventIDs) {
				return domainError(CodeIdempotencyConflict, "operation key was already used with different input")
			}
			existingReceipt, receiptFound := tx.FindReceiptByOperationAttempt(existing.ID, existing.Attempt)
			if !receiptFound {
				return errors.New("operation receipt invariant violated")
			}
			if existingReceipt.Required != command.Required {
				return domainError(CodeIdempotencyConflict, "operation key was already used with different input")
			}
			factAttempt := existing.Attempt
			if existing.AttemptStatus == sessionvo.AttemptReady {
				if factAttempt <= 1 {
					return errors.New("ready operation attempt invariant violated")
				}
				factAttempt--
			}
			existingFact, found := tx.FindOperationCallFact(existing.ID, factAttempt)
			if !found || existingFact.Protocol != command.Protocol ||
				existingFact.SourceModule != command.SourceModule ||
				!payloadEnvelopeEqual(existingFact.Input, input) {
				return domainError(CodeIdempotencyConflict, "operation key was already used with different input")
			}
			if existing.AttemptStatus == sessionvo.AttemptReady {
				now := tx.Now()
				existing.AttemptStatus = sessionvo.AttemptPending
				existing.RowVersion++
				existing.UpdatedAt = now
				tx.SaveOperation(existing)
				if input.Mode == sessionvo.PayloadReferenced {
					existingReceipt.ArtifactRefs = appendUnique(existingReceipt.ArtifactRefs, input.Ref)
					tx.SaveReceipt(existingReceipt)
				}
				tx.SaveOperationCallFact(sessionvo.OperationCallFact{
					OperationID: existing.ID, Attempt: existing.Attempt,
					ConversationID: existing.ConversationID, InteractionID: existing.InteractionID,
					ReceiptID: existingReceipt.ID, ToolName: existing.ToolName,
					Protocol: command.Protocol, SourceModule: command.SourceModule, Input: input,
					ParentOperationID: existing.ParentOperationID,
					StartedAt:         now, Status: sessionvo.AttemptPending,
				})
				if err := s.appendProjection(
					tx, "operation", existing.ID, "operation.attempt.started", existing,
				); err != nil {
					return err
				}
				execute = true
			}
			operation, receipt = existing, existingReceipt
			return nil
		}
		now := tx.Now()
		operation = sessionvo.Operation{
			ID: s.newID("op"), ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationKey: command.OperationKey, ToolName: command.ToolName,
			ParentOperationID: command.ParentOperationID,
			CausationEventIDs: append([]string(nil), command.CausationEventIDs...),
			Attempt:           1, AttemptStatus: sessionvo.AttemptPending, RowVersion: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		receipt = sessionvo.Receipt{
			ID: s.newID("rcpt"), SchemaVersion: "3.0.0", Owner: command.Owner,
			ConversationID: conversation.ID, InteractionID: interaction.ID,
			OperationID: operation.ID, Attempt: 1, OperationKey: command.OperationKey,
			ToolName: command.ToolName,
			Status:   sessionvo.ReceiptPending, EvidenceDurability: sessionvo.DurabilityPending,
			Required:             command.Required,
			CausationEventIDs:    cloneStrings(command.CausationEventIDs),
			ObservedEvidenceRefs: []string{},
			BusinessRefs:         []sessionvo.BusinessRef{},
			ArtifactRefs:         payloadArtifactRefs(input),
			PartialReasons:       []string{},
			RowVersion:           1,
			IssuedAt:             now,
		}
		created = true
		execute = true
		tx.SaveOperation(operation)
		tx.SaveReceipt(receipt)
		tx.SaveOperationCallFact(sessionvo.OperationCallFact{
			OperationID: operation.ID, Attempt: operation.Attempt,
			ConversationID: operation.ConversationID, InteractionID: operation.InteractionID,
			ReceiptID: receipt.ID, ToolName: operation.ToolName,
			Protocol: command.Protocol, SourceModule: command.SourceModule, Input: input,
			ParentOperationID: operation.ParentOperationID,
			StartedAt:         now, Status: sessionvo.AttemptPending,
		})
		if err := s.appendProjection(tx, "operation", operation.ID, "operation.started", operation); err != nil {
			return err
		}
		if err := s.appendProjection(tx, "receipt", receipt.ID, "receipt.started", receipt); err != nil {
			return err
		}
		interaction.EvidenceStatus = sessionvo.EvidenceAssembling
		interaction.RowVersion++
		interaction.UpdatedAt = now
		tx.SaveInteraction(interaction)
		return s.appendProjection(
			tx, "interaction", interaction.ID, "interaction.evidence.assembling",
			interaction,
		)
	})
	s.observeLifecycleError(err)
	return EnsureOperationResult{
		Operation: operation, Receipt: receipt, Created: created, Execute: execute,
	}, err
}

func (s *Service) StartOperationAttempt(ctx context.Context, command StartAttemptCommand) (sessionvo.Operation, sessionvo.Receipt, error) {
	var operation sessionvo.Operation
	var receipt sessionvo.Receipt
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operationRef, found := tx.PeekOperation(command.OperationID)
		if !found {
			return resourceNotDisclosed()
		}
		conversation, err := ownedConversation(tx, command.Owner, operationRef.ConversationID)
		if err != nil {
			return err
		}
		if err := requireActiveConversation(conversation); err != nil {
			return err
		}
		interaction, found := tx.FindInteraction(operationRef.InteractionID)
		if !found {
			return resourceNotDisclosed()
		}
		current, found := tx.FindOperation(command.OperationID)
		if !found || current.ConversationID != conversation.ID ||
			current.InteractionID != interaction.ID {
			return resourceNotDisclosed()
		}
		if interaction.ExecutionStatus != sessionvo.InteractionActive ||
			!interaction.LeaseExpiresAt.After(tx.Now()) {
			return domainError(CodeInteractionTerminal, "interaction is already terminal or its lease expired")
		}
		if command.LeaseToken == "" || command.LeaseEpoch == 0 ||
			command.LeaseToken != interaction.LeaseToken ||
			command.LeaseEpoch != interaction.LeaseEpoch {
			return domainError(CodeTerminalConflict, "stale interaction lease was fenced")
		}
		renewInteractionLease(tx, &interaction)
		previousReceipt, found := tx.FindReceiptByOperationAttempt(current.ID, current.Attempt)
		if !found || previousReceipt.Status != sessionvo.ReceiptFailed || !current.Retryable {
			return domainError(CodeReceiptPending, "the current attempt has not failed retryably")
		}
		now := tx.Now()
		current.Attempt++
		current.AttemptStatus = sessionvo.AttemptReady
		current.Retryable = false
		current.RowVersion++
		current.UpdatedAt = now
		receipt = sessionvo.Receipt{
			ID: s.newID("rcpt"), SchemaVersion: "3.0.0", Owner: command.Owner,
			ConversationID: current.ConversationID, InteractionID: current.InteractionID,
			OperationID: current.ID, Attempt: current.Attempt, OperationKey: current.OperationKey,
			ToolName: current.ToolName,
			Status:   sessionvo.ReceiptPending, EvidenceDurability: sessionvo.DurabilityPending,
			Required:             previousReceipt.Required,
			CausationEventIDs:    cloneStrings(previousReceipt.CausationEventIDs),
			ObservedEvidenceRefs: []string{},
			BusinessRefs:         []sessionvo.BusinessRef{},
			ArtifactRefs:         []string{},
			PartialReasons:       []string{},
			RowVersion:           1,
			IssuedAt:             now,
		}
		tx.SaveOperation(current)
		tx.SaveReceipt(receipt)
		if err := s.appendProjection(tx, "operation", current.ID, "operation.attempt.ready", current); err != nil {
			return err
		}
		if err := s.appendProjection(tx, "receipt", receipt.ID, "receipt.started", receipt); err != nil {
			return err
		}
		operation = current
		return nil
	})
	s.observeLifecycleError(err)
	return operation, receipt, err
}

func renewInteractionLease(tx isessionstore.Transaction, interaction *sessionvo.Interaction) {
	interaction.LeaseExpiresAt = tx.Now().Add(5 * time.Minute)
	interaction.LeaseVersion++
	interaction.RowVersion++
	interaction.UpdatedAt = tx.Now()
	tx.SaveInteraction(*interaction)
}

func (s *Service) CompleteOperationAttempt(ctx context.Context, command FinishAttemptCommand) (sessionvo.Operation, sessionvo.Receipt, error) {
	if command.EvidenceDurability == "" {
		command.EvidenceDurability = sessionvo.DurabilityPending
	}
	return s.finishOperationAttempt(ctx, command, sessionvo.ReceiptCompleted)
}

func (s *Service) FailOperationAttempt(ctx context.Context, command FinishAttemptCommand) (sessionvo.Operation, sessionvo.Receipt, error) {
	if command.EvidenceDurability == "" {
		command.EvidenceDurability = sessionvo.DurabilityFailed
	}
	return s.finishOperationAttempt(ctx, command, sessionvo.ReceiptFailed)
}

func (s *Service) finishOperationAttempt(ctx context.Context, command FinishAttemptCommand, status sessionvo.ReceiptStatus) (sessionvo.Operation, sessionvo.Receipt, error) {
	var terminalPayload sessionvo.PayloadEnvelope
	if status == sessionvo.ReceiptCompleted && (command.Output.Mode == "" || command.Error.Mode != "") {
		return sessionvo.Operation{}, sessionvo.Receipt{}, domainError(CodeOperationRequired, "completed attempt requires output only")
	}
	if status == sessionvo.ReceiptFailed && (command.Error.Mode == "" || command.Output.Mode != "") {
		return sessionvo.Operation{}, sessionvo.Receipt{}, domainError(CodeOperationRequired, "failed attempt requires error only")
	}
	terminalPayload = command.Output
	if status == sessionvo.ReceiptFailed {
		terminalPayload = command.Error
	}
	terminalPayload, err := sessionvo.NormalizePayloadEnvelope(terminalPayload)
	if err != nil {
		return sessionvo.Operation{}, sessionvo.Receipt{}, domainError(CodeOperationRequired, "operation terminal payload envelope is invalid")
	}
	if command.RequestID == "" || !validTraceID(command.TraceID) {
		return sessionvo.Operation{}, sessionvo.Receipt{}, domainError(
			CodeOperationRequired,
			"receipt request ID and valid trace ID are required",
		)
	}
	if !validEvidenceDurability(command.EvidenceDurability) {
		return sessionvo.Operation{}, sessionvo.Receipt{}, domainError(
			CodeOperationRequired,
			"receipt evidence durability is invalid",
		)
	}
	var operation sessionvo.Operation
	var receipt sessionvo.Receipt
	err = s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		operationRef, found := tx.PeekOperation(command.OperationID)
		if !found {
			return resourceNotDisclosed()
		}
		conversation, err := ownedConversation(tx, command.Owner, operationRef.ConversationID)
		if err != nil {
			return err
		}
		interaction, found := tx.FindInteraction(operationRef.InteractionID)
		if !found {
			return resourceNotDisclosed()
		}
		current, found := tx.FindOperation(command.OperationID)
		if !found || current.ConversationID != conversation.ID ||
			current.InteractionID != interaction.ID {
			return resourceNotDisclosed()
		}
		if interaction.ExecutionStatus == sessionvo.InteractionActive &&
			!interaction.LeaseExpiresAt.After(tx.Now()) {
			return domainError(CodeInteractionTerminal, "interaction lease has expired")
		}
		claimedReceipt, found := tx.FindReceipt(command.ReceiptID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, command.Owner, claimedReceipt.ConversationID); err != nil {
			return err
		}
		if claimedReceipt.ConversationID != conversation.ID ||
			claimedReceipt.InteractionID != interaction.ID ||
			claimedReceipt.OperationID != current.ID {
			return resourceNotDisclosed()
		}
		if current.Attempt != command.Attempt {
			return domainError(CodeIdempotencyConflict, "operation attempt or receipt does not match")
		}
		currentReceipt, found := tx.FindReceiptByOperationAttempt(current.ID, command.Attempt)
		if !found {
			return resourceNotDisclosed()
		}
		if currentReceipt.ID != claimedReceipt.ID {
			return domainError(CodeIdempotencyConflict, "operation attempt or receipt does not match")
		}
		if current.AttemptStatus == sessionvo.AttemptReady {
			return domainError(CodeReceiptPending, "operation attempt has not been claimed for execution")
		}
		callFact, found := tx.FindOperationCallFact(current.ID, command.Attempt)
		if !found {
			return errors.New("operation call fact invariant violated")
		}
		retryable := coreRetryableFailure(current, command, status)
		if currentReceipt.Status != sessionvo.ReceiptPending {
			if receiptTerminalMatches(currentReceipt, current, callFact, terminalPayload, command, status, retryable) {
				operation, receipt = current, currentReceipt
				return nil
			}
			return domainError(CodeIdempotencyConflict, "receipt terminal payload conflicts with the durable result")
		}
		for _, ref := range command.BusinessRefs {
			if !ref.IsCanonicalForBusinessDomain(command.Owner.BusinessDomainID) {
				return domainError(
					CodeOperationRequired,
					"receipt business_refs contains an invalid typed business reference",
				)
			}
		}
		if evidenceReferenceCount(tx.ListReceipts(interaction.ID), currentReceipt.ID, command.ObservedEvidenceRefs) >
			maxEvidenceRefsPerInteraction {
			return domainError(CodeOperationRequired, "interaction evidence reference limit of 2048 was exceeded")
		}
		now := tx.Now()
		currentReceipt.Status = status
		currentReceipt.EvidenceDurability = command.EvidenceDurability
		currentReceipt.RequestID = command.RequestID
		currentReceipt.TraceID = command.TraceID
		currentReceipt.ObservedEvidenceRefs = cloneStrings(command.ObservedEvidenceRefs)
		currentReceipt.BusinessRefs = cloneBusinessRefs(command.BusinessRefs)
		currentReceipt.ArtifactRefs = effectiveArtifactRefs(command.ArtifactRefs, callFact.Input, terminalPayload)
		currentReceipt.PartialReasons = effectivePartialReasons(
			command.PartialReasons, callFact.Input, terminalPayload, command.EvidenceDurability,
		)
		currentReceipt.TerminalAt = &now
		currentReceipt.RowVersion++
		current.AttemptStatus = sessionvo.AttemptCompleted
		if status == sessionvo.ReceiptFailed {
			current.AttemptStatus = sessionvo.AttemptFailed
			current.Retryable = retryable
		}
		current.RowVersion++
		current.UpdatedAt = now
		if status == sessionvo.ReceiptCompleted {
			callFact.Output = &terminalPayload
			callFact.Error = nil
		} else {
			callFact.Output = nil
			callFact.Error = &terminalPayload
		}
		callFact.RequestID = command.RequestID
		callFact.TraceID = command.TraceID
		callFact.SpanID = command.SpanID
		callFact.FinishedAt = &now
		callFact.Status = current.AttemptStatus
		callFact.Retryable = retryable
		tx.SaveOperationCallFact(callFact)
		tx.SaveOperation(current)
		tx.SaveReceipt(currentReceipt)
		if err := s.appendProjection(tx, "operation", current.ID, "operation.attempt."+string(status), current); err != nil {
			return err
		}
		if err := s.appendProjection(
			tx, "receipt", currentReceipt.ID, "receipt."+string(status), currentReceipt,
		); err != nil {
			return err
		}
		if found && interaction.IsTerminal() && interaction.ClosureManifest != nil &&
			manifestContainsReceipt(*interaction.ClosureManifest, currentReceipt.ID) {
			nextEvidenceStatus := evidenceStatusAtTermination(tx, *interaction.ClosureManifest)
			if nextEvidenceStatus != sessionvo.EvidenceAssembling {
				interaction.EvidenceStatus = nextEvidenceStatus
				interaction.RowVersion++
				interaction.UpdatedAt = now
				tx.SaveInteraction(interaction)
				if err := s.freezeAssemblyRevision(tx, interaction, *interaction.ClosureManifest, "late_receipt"); err != nil {
					return err
				}
				if err := s.appendProjection(tx, "interaction", interaction.ID, "interaction.evidence.revised", interaction); err != nil {
					return err
				}
			}
		}
		operation, receipt = current, currentReceipt
		return nil
	})
	s.observeLifecycleError(err)
	return operation, receipt, err
}

func validTraceID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 {
		return false
	}
	return slices.ContainsFunc(decoded, func(value byte) bool { return value != 0 })
}

// Retryable is a trusted adapter observation. Core validates the failed
// terminal state and durability but does not independently classify failures.
func coreRetryableFailure(
	_ sessionvo.Operation,
	command FinishAttemptCommand,
	status sessionvo.ReceiptStatus,
) bool {
	return status == sessionvo.ReceiptFailed &&
		command.EvidenceDurability == sessionvo.DurabilityFailed &&
		command.Retryable
}

func receiptTerminalMatches(
	receipt sessionvo.Receipt,
	operation sessionvo.Operation,
	callFact sessionvo.OperationCallFact,
	terminalPayload sessionvo.PayloadEnvelope,
	command FinishAttemptCommand,
	status sessionvo.ReceiptStatus,
	retryable bool,
) bool {
	return receipt.Status == status &&
		terminalPayloadMatches(callFact, terminalPayload, status) &&
		receipt.EvidenceDurability == command.EvidenceDurability &&
		receipt.RequestID == command.RequestID &&
		receipt.TraceID == command.TraceID &&
		callFact.SpanID == command.SpanID &&
		slices.Equal(receipt.ObservedEvidenceRefs, command.ObservedEvidenceRefs) &&
		businessRefsEqual(receipt.BusinessRefs, command.BusinessRefs) &&
		slices.Equal(receipt.ArtifactRefs, effectiveArtifactRefs(command.ArtifactRefs, callFact.Input, terminalPayload)) &&
		slices.Equal(receipt.PartialReasons, effectivePartialReasons(
			command.PartialReasons, callFact.Input, terminalPayload, command.EvidenceDurability,
		)) &&
		(status != sessionvo.ReceiptFailed || operation.Retryable == retryable)
}

func effectiveArtifactRefs(
	declared []string,
	input sessionvo.PayloadEnvelope,
	terminal sessionvo.PayloadEnvelope,
) []string {
	result := cloneStrings(declared)
	if input.Mode == sessionvo.PayloadReferenced {
		result = appendUnique(result, input.Ref)
	}
	if terminal.Mode == sessionvo.PayloadReferenced {
		result = appendUnique(result, terminal.Ref)
	}
	return result
}

func effectivePartialReasons(
	declared []string,
	input sessionvo.PayloadEnvelope,
	terminal sessionvo.PayloadEnvelope,
	durability sessionvo.EvidenceDurability,
) []string {
	result := cloneStrings(declared)
	if input.Mode == sessionvo.PayloadOmitted {
		result = appendUnique(result, input.OmittedReason)
	}
	if terminal.Mode == sessionvo.PayloadOmitted {
		result = appendUnique(result, terminal.OmittedReason)
	}
	if durability == sessionvo.DurabilityFailed {
		result = appendUnique(result, "evidence_durability_failed")
	}
	return result
}

func terminalPayloadMatches(
	fact sessionvo.OperationCallFact,
	payload sessionvo.PayloadEnvelope,
	status sessionvo.ReceiptStatus,
) bool {
	if status == sessionvo.ReceiptCompleted {
		return fact.Output != nil && fact.Error == nil && payloadEnvelopeEqual(*fact.Output, payload)
	}
	return fact.Error != nil && fact.Output == nil && payloadEnvelopeEqual(*fact.Error, payload)
}

func payloadEnvelopeEqual(left, right sessionvo.PayloadEnvelope) bool {
	if left.Mode == sessionvo.PayloadOmitted || right.Mode == sessionvo.PayloadOmitted {
		return false
	}
	return left.Mode == right.Mode && left.MediaType == right.MediaType &&
		left.ByteLength == right.ByteLength && bytes.Equal(left.Inline, right.Inline) &&
		left.Ref == right.Ref && left.OmittedReason == right.OmittedReason
}

func canonicalStringSet(values []string) []string {
	result := cloneStrings(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func businessRefsEqual(left, right []sessionvo.BusinessRef) bool {
	return slices.EqualFunc(left, right, func(a, b sessionvo.BusinessRef) bool {
		if a.RefType != b.RefType || a.RefID != b.RefID ||
			a.BusinessDomainID != b.BusinessDomainID || a.Version != b.Version ||
			a.DisplayHint != b.DisplayHint {
			return false
		}
		if a.AsOf == nil || b.AsOf == nil {
			return a.AsOf == nil && b.AsOf == nil
		}
		return a.AsOf.Equal(*b.AsOf)
	})
}

func validEvidenceDurability(value sessionvo.EvidenceDurability) bool {
	switch value {
	case sessionvo.DurabilityPending, sessionvo.DurabilityDurable, sessionvo.DurabilityFailed:
		return true
	default:
		return false
	}
}

func manifestContainsReceipt(manifest sessionvo.ClosureManifest, receiptID string) bool {
	for _, expected := range manifest.ExpectedReceipts {
		if expected.ReceiptID == receiptID {
			return true
		}
	}
	return false
}

func (s *Service) AbandonExpiredInteractions(ctx context.Context, limit int) ([]sessionvo.Interaction, error) {
	var abandoned []sessionvo.Interaction
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for _, interaction := range tx.ListExpiredActiveInteractions(limit) {
			conversation, found := tx.FindConversation(interaction.ConversationID)
			if !found {
				continue
			}
			current, found := tx.FindInteraction(interaction.ID)
			if !found || current.ExecutionStatus != sessionvo.InteractionActive ||
				current.LeaseVersion != interaction.LeaseVersion || current.LeaseExpiresAt.After(tx.Now()) {
				continue
			}
			now := tx.Now()
			manifest := deriveClosureManifest(tx, current.ID, sessionvo.ClosureManifest{
				Version: "3.0.0", CompletionReason: "interaction_abandoned",
			})
			if hasRequiredPendingReceipt(tx, manifest) {
				manifest.AssemblerDeadline = &now
			}
			current.ExecutionStatus = sessionvo.InteractionAbandoned
			current.EvidenceStatus = evidenceStatusAtTermination(tx, manifest)
			current.TerminalIdempotencyKey = "lease-expired:" + current.LeaseToken
			current.TerminalPayloadHash = hashValue(current.LeaseVersion)
			current.RowVersion++
			current.LeaseVersion++
			current.UpdatedAt = now
			current.TerminalAt = &now
			current.ClosureManifest = &manifest
			tx.SaveInteraction(current)
			if current.EvidenceStatus != sessionvo.EvidenceAssembling {
				if err := s.freezeAssemblyRevision(tx, current, manifest, "lease_expired"); err != nil {
					return err
				}
			}
			if err := s.appendProjection(tx, "interaction", current.ID, "interaction.abandoned", current); err != nil {
				return err
			}
			if conversation.OneShot && conversation.Status == sessionvo.ConversationActive {
				conversation.Status = sessionvo.ConversationClosed
				conversation.RowVersion++
				conversation.UpdatedAt = now
				conversation.ClosedAt = &now
				tx.SaveConversation(conversation)
				if err := s.appendProjection(
					tx, "conversation", conversation.ID, "conversation.closed", conversation,
				); err != nil {
					return err
				}
			}
			abandoned = append(abandoned, current)
		}
		return nil
	})
	s.observeLifecycleError(err)
	if err == nil {
		s.metrics.Add(icoremetrics.InteractionsAbandonedTotal, uint64(len(abandoned)))
	}
	return abandoned, err
}

func (s *Service) ExpireIdleOneShotConversations(
	ctx context.Context,
	idleTTL time.Duration,
	limit int,
) ([]sessionvo.Conversation, error) {
	if idleTTL <= 0 {
		idleTTL = 15 * time.Minute
	}
	var expired []sessionvo.Conversation
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		cutoff := tx.Now().Add(-idleTTL)
		for _, candidate := range tx.ListIdleOneShotConversations(cutoff, limit) {
			current, found := tx.FindConversation(candidate.ID)
			if !found || !current.OneShot || current.Status != sessionvo.ConversationActive ||
				current.UpdatedAt.After(cutoff) {
				continue
			}
			if _, active := tx.FindActiveInteraction(current.ID); active {
				continue
			}
			now := tx.Now()
			current.Status = sessionvo.ConversationExpired
			current.RowVersion++
			current.UpdatedAt = now
			current.ClosedAt = &now
			tx.SaveConversation(current)
			if err := s.appendProjection(
				tx, "conversation", current.ID, "conversation.expired", current,
			); err != nil {
				return err
			}
			expired = append(expired, current)
		}
		return nil
	})
	s.observeLifecycleError(err)
	return expired, err
}

func (s *Service) ListAssemblyRevisions(ctx context.Context, owner sessionvo.Owner, interactionID string) ([]sessionvo.AssemblyRevision, error) {
	var result []sessionvo.AssemblyRevision
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		interaction, found := tx.PeekInteraction(interactionID)
		if !found {
			return resourceNotDisclosed()
		}
		if _, err := ownedConversation(tx, owner, interaction.ConversationID); err != nil {
			return err
		}
		result = tx.ListAssemblyRevisions(interactionID)
		return nil
	})
	return result, err
}

func (s *Service) AssembleDueInteractions(ctx context.Context, limit int) ([]sessionvo.Interaction, error) {
	var assembled []sessionvo.Interaction
	err := s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for _, interaction := range tx.ListAssemblyDueInteractions(limit) {
			if _, found := tx.FindConversation(interaction.ConversationID); !found {
				continue
			}
			current, found := tx.FindInteraction(interaction.ID)
			if !found || current.EvidenceStatus != sessionvo.EvidenceAssembling ||
				current.ClosureManifest == nil || current.ClosureManifest.AssemblerDeadline == nil ||
				current.ClosureManifest.AssemblerDeadline.After(tx.Now()) {
				continue
			}
			current.EvidenceStatus = sessionvo.EvidencePartial
			if deadline := current.ClosureManifest.AssemblerDeadline; deadline != nil {
				lag := tx.Now().Sub(*deadline).Seconds()
				if lag < 0 {
					lag = 0
				}
				s.metrics.Set(icoremetrics.AssemblyLagSeconds, lag)
			}
			current.RowVersion++
			current.UpdatedAt = tx.Now()
			tx.SaveInteraction(current)
			if err := s.freezeAssemblyRevision(tx, current, *current.ClosureManifest, "deadline"); err != nil {
				return err
			}
			if err := s.appendProjection(tx, "interaction", current.ID, "interaction.evidence.partial", current); err != nil {
				return err
			}
			assembled = append(assembled, current)
		}
		return nil
	})
	s.observeLifecycleError(err)
	return assembled, err
}

func (s *Service) freezeAssemblyRevision(tx isessionstore.Transaction, interaction sessionvo.Interaction, manifest sessionvo.ClosureManifest, trigger string) error {
	existing := tx.ListAssemblyRevisions(interaction.ID)
	revisionNo := uint64(len(existing) + 1)
	parentID := ""
	if len(existing) > 0 {
		parentID = existing[len(existing)-1].ID
	}
	receiptIDs := make([]string, 0, len(manifest.ExpectedReceipts))
	eventIDs := make([]string, 0)
	artifactRefs := make([]string, 0)
	partialReasons := cloneStrings(manifest.SystemPartialReasons)
	for _, expected := range manifest.ExpectedReceipts {
		receiptIDs = append(receiptIDs, expected.ReceiptID)
		receipt, found := tx.FindReceipt(expected.ReceiptID)
		if !found {
			partialReasons = append(partialReasons, "receipt_missing:"+expected.ReceiptID)
			continue
		}
		for _, eventID := range receipt.ObservedEvidenceRefs {
			eventIDs = appendUnique(eventIDs, eventID)
		}
		artifactRefs = append(artifactRefs, receipt.ArtifactRefs...)
		partialReasons = append(partialReasons, receipt.PartialReasons...)
		if expected.Required && receipt.EvidenceDurability != sessionvo.DurabilityDurable {
			partialReasons = append(partialReasons, "receipt_not_durable:"+expected.ReceiptID)
		}
	}
	revision := sessionvo.AssemblyRevision{
		ID: s.newID("rev"), RevisionNo: revisionNo, ParentRevisionID: parentID,
		InteractionID: interaction.ID, CompletionManifestVersion: manifest.Version,
		IncludedReceiptIDs: receiptIDs, IncludedEventIDs: eventIDs,
		ArtifactManifestHash: hashValue(struct {
			Claims       []string
			EventIDs     []string
			ArtifactRefs []string
		}{manifest.Claims, eventIDs, artifactRefs}),
		Completeness: interaction.EvidenceStatus, PartialReasons: partialReasons,
		Trigger: trigger, CreatedAt: tx.Now(),
	}
	tx.SaveAssemblyRevision(revision)
	return s.appendProjection(tx, "assembly_revision", revision.ID, "assembly.revision.created", revision)
}

func validateClosureManifest(tx isessionstore.Transaction, interaction sessionvo.Interaction, manifest sessionvo.ClosureManifest) error {
	if len(manifest.Claims) > maxClaimsPerInteraction {
		return domainError(CodeClosureManifestInvalid, "closure manifest claim limit of 32 was exceeded")
	}
	registeredOperations := tx.ListOperations(interaction.ID)
	registeredReceipts := tx.ListReceipts(interaction.ID)
	if len(manifest.ExpectedOperations) != len(registeredOperations) ||
		len(manifest.ExpectedReceipts) != len(registeredReceipts) {
		return domainError(CodeClosureManifestInvalid, "closure manifest must list every registered operation and receipt")
	}
	operationRequired := make(map[string]bool, len(registeredReceipts))
	for _, receipt := range registeredReceipts {
		operationRequired[receipt.OperationID] = receipt.Required
	}
	seenOperations := make(map[string]bool, len(manifest.ExpectedOperations))
	for _, expected := range manifest.ExpectedOperations {
		if expected.OperationID == "" || seenOperations[expected.OperationID] {
			return domainError(CodeClosureManifestInvalid, "closure manifest contains a duplicate or empty operation")
		}
		seenOperations[expected.OperationID] = true
		operation, found := tx.FindOperation(expected.OperationID)
		if !found || operation.InteractionID != interaction.ID ||
			operationRequired[operation.ID] != expected.Required {
			return domainError(CodeClosureManifestInvalid, "closure manifest operation does not belong to the interaction")
		}
	}
	seenReceipts := make(map[string]bool, len(manifest.ExpectedReceipts))
	for _, expected := range manifest.ExpectedReceipts {
		if expected.ReceiptID == "" || seenReceipts[expected.ReceiptID] {
			return domainError(CodeClosureManifestInvalid, "closure manifest contains a duplicate or empty receipt")
		}
		seenReceipts[expected.ReceiptID] = true
		receipt, found := tx.FindReceipt(expected.ReceiptID)
		if !found || receipt.InteractionID != interaction.ID || receipt.Required != expected.Required {
			return domainError(CodeClosureManifestInvalid, "closure manifest receipt is unknown, foreign, or changes required semantics")
		}
	}
	return nil
}

func evidenceReferenceCount(receipts []sessionvo.Receipt, replacingReceiptID string, replacement []string) int {
	unique := make(map[string]struct{})
	for _, receipt := range receipts {
		references := receipt.ObservedEvidenceRefs
		if receipt.ID == replacingReceiptID {
			references = replacement
		}
		for _, reference := range references {
			if reference != "" {
				unique[reference] = struct{}{}
			}
		}
	}
	return len(unique)
}

func ownedConversation(tx isessionstore.Transaction, owner sessionvo.Owner, conversationID string) (sessionvo.Conversation, error) {
	conversation, found := tx.FindConversation(conversationID)
	if !found {
		return sessionvo.Conversation{}, domainError(
			CodeResourceNotDisclosed,
			"request was not found in the authorized scope",
		)
	}
	if !conversation.Owner.Equal(owner) {
		return sessionvo.Conversation{}, domainError(
			CodeResourceNotDisclosed,
			"request was not found in the authorized scope",
		)
	}
	return conversation, nil
}

func resourceNotDisclosed() error {
	return domainError(CodeResourceNotDisclosed, "request was not found in the authorized scope")
}

func requireActiveConversation(conversation sessionvo.Conversation) error {
	switch conversation.Status {
	case sessionvo.ConversationActive:
		return nil
	case sessionvo.ConversationExpired:
		return domainError(CodeConversationExpired, "conversation expired; create or resume another conversation")
	default:
		return domainError(CodeConversationClosed, "conversation is closed; create a new generation")
	}
}

func validTerminalStatus(status sessionvo.InteractionStatus) bool {
	switch status {
	case sessionvo.InteractionCompleted, sessionvo.InteractionFailed, sessionvo.InteractionCanceled,
		sessionvo.InteractionHandedOff, sessionvo.InteractionAbandoned:
		return true
	default:
		return false
	}
}

func evidenceStatusAtTermination(tx isessionstore.Transaction, manifest sessionvo.ClosureManifest) sessionvo.EvidenceStatus {
	if len(manifest.SystemPartialReasons) > 0 {
		return sessionvo.EvidencePartial
	}
	if len(manifest.ExpectedReceipts) == 0 && len(manifest.Claims) == 0 {
		return sessionvo.EvidenceNotApplicable
	}
	for _, expected := range manifest.ExpectedReceipts {
		receipt, found := tx.FindReceipt(expected.ReceiptID)
		if !found || (expected.Required && receipt.EvidenceDurability == sessionvo.DurabilityPending) {
			if manifest.AssemblerDeadline != nil && !manifest.AssemblerDeadline.After(tx.Now()) {
				return sessionvo.EvidencePartial
			}
			return sessionvo.EvidenceAssembling
		}
		if expected.Required && receipt.EvidenceDurability == sessionvo.DurabilityFailed {
			return sessionvo.EvidencePartial
		}
		if expected.Required && len(receipt.PartialReasons) > 0 {
			return sessionvo.EvidencePartial
		}
	}
	return sessionvo.EvidenceComplete
}

func hasRequiredPendingReceipt(tx isessionstore.Transaction, manifest sessionvo.ClosureManifest) bool {
	for _, expected := range manifest.ExpectedReceipts {
		if !expected.Required {
			continue
		}
		receipt, found := tx.FindReceipt(expected.ReceiptID)
		if !found || receipt.EvidenceDurability == sessionvo.DurabilityPending {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func payloadArtifactRefs(payload sessionvo.PayloadEnvelope) []string {
	if payload.Mode == sessionvo.PayloadReferenced {
		return []string{payload.Ref}
	}
	return []string{}
}

func cloneStrings(values []string) []string {
	return append(make([]string, 0, len(values)), values...)
}

func cloneBusinessRefs(values []sessionvo.BusinessRef) []sessionvo.BusinessRef {
	return append(make([]sessionvo.BusinessRef, 0, len(values)), values...)
}

func hashValue(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) appendProjection(tx isessionstore.Transaction, aggregateType, aggregateID, eventType string, value any) error {
	projectionValue := value
	if receipt, ok := value.(sessionvo.Receipt); ok {
		projectionValue = sessionvo.NewReceiptProjectionDocument(receipt)
	}
	payload, err := json.Marshal(projectionValue)
	if err != nil {
		return err
	}
	var aggregateVersion uint64
	switch typed := value.(type) {
	case sessionvo.Conversation:
		aggregateVersion = typed.RowVersion
	case sessionvo.Interaction:
		aggregateVersion = typed.RowVersion
	case sessionvo.Operation:
		aggregateVersion = typed.RowVersion
	case sessionvo.Receipt:
		aggregateVersion = typed.RowVersion
	case sessionvo.AssemblyRevision:
		aggregateVersion = typed.RevisionNo
	}
	if aggregateVersion == 0 {
		return errors.New("projection aggregate version is required")
	}
	tx.AppendProjection(sessionvo.ProjectionMutation{
		EventID: s.newID("evt"), AggregateType: aggregateType,
		AggregateID: aggregateID, AggregateVersion: aggregateVersion,
		EventType: eventType, Payload: payload,
	})
	return nil
}

func validateOwner(owner sessionvo.Owner) error {
	if owner.TenantID == "" || owner.BusinessDomainID == "" ||
		owner.ApplicationPrincipalID == "" || owner.EffectiveSubjectID == "" ||
		(owner.EffectiveSubjectType != sessionvo.SubjectUser && owner.EffectiveSubjectType != sessionvo.SubjectService) {
		return ErrInvalidOwner
	}
	return nil
}

func (s *Service) observeLifecycleError(err error) {
	if err == nil {
		return
	}
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		s.metrics.Increment(icoremetrics.SessionRejectionsTotal)
		switch domainErr.Code {
		case CodeInteractionInProgress, CodeIdempotencyConflict, CodeTerminalConflict:
			s.metrics.Increment(icoremetrics.SessionTransitionConflictsTotal)
		}
		return
	}
	s.metrics.Increment(icoremetrics.SessionStoreErrorsTotal)
}

func randomID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(value[:])
}
