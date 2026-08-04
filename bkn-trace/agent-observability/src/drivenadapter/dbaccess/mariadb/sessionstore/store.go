package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const transactionRetries = 4

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Migrate(ctx context.Context) error {
	for _, statement := range strings.Split(SchemaSQL(), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply BKN Trace migration: %w", err)
		}
	}
	return nil
}

func (s *Store) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	var lastErr error
	for attempt := 0; attempt < transactionRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		adapter := &transaction{ctx: ctx, tx: tx}
		if err := adapter.loadServerTime(); err != nil {
			_ = tx.Rollback()
			return err
		}
		callbackErr := fn(adapter)
		if callbackErr == nil {
			callbackErr = adapter.err
		}
		if callbackErr != nil {
			_ = tx.Rollback()
			if retryableTransactionError(callbackErr) {
				lastErr = callbackErr
				if err := waitForTransactionRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return callbackErr
		}
		if err := tx.Commit(); err != nil {
			if retryableTransactionError(err) {
				lastErr = err
				if waitErr := waitForTransactionRetry(ctx, attempt); waitErr != nil {
					return waitErr
				}
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("transaction retry budget exhausted: %w", lastErr)
}

func waitForTransactionRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(transactionRetryDelay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transactionRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= transactionRetries {
		attempt = transactionRetries - 1
	}
	maximum := 5 * time.Millisecond * time.Duration(1<<attempt)
	return time.Duration(rand.Int64N(int64(maximum) + 1))
}

func retryableTransactionError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1062, 1205, 1213:
		return true
	default:
		return false
	}
}

type transaction struct {
	ctx context.Context
	tx  *sql.Tx
	now time.Time
	err error
}

func (t *transaction) loadServerTime() error {
	if err := t.tx.QueryRowContext(t.ctx, "SELECT UTC_TIMESTAMP(6)").Scan(&t.now); err != nil {
		return fmt.Errorf("read MariaDB server time: %w", err)
	}
	t.now = t.now.UTC()
	return nil
}

func (t *transaction) Now() time.Time {
	return t.now
}

func (t *transaction) FindCurrentConversation(owner sessionvo.Owner, externalKey string) (sessionvo.Conversation, bool) {
	if t.err != nil {
		return sessionvo.Conversation{}, false
	}
	row := t.tx.QueryRowContext(t.ctx, conversationSelect+`
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND external_conversation_key=?
		ORDER BY generation DESC LIMIT 1 FOR UPDATE`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, externalKey,
	)
	return t.scanConversation(row)
}

func (t *transaction) FindConversation(conversationID string) (sessionvo.Conversation, bool) {
	if t.err != nil {
		return sessionvo.Conversation{}, false
	}
	return t.scanConversation(t.tx.QueryRowContext(t.ctx, conversationSelect+`
		WHERE conversation_id=? FOR UPDATE`, conversationID))
}

func (t *transaction) FindIdempotency(
	scope string,
	owner sessionvo.Owner,
	externalKey string,
	idempotencyKey string,
) (sessionvo.IdempotencyRecord, bool) {
	if t.err != nil {
		return sessionvo.IdempotencyRecord{}, false
	}
	var record sessionvo.IdempotencyRecord
	record.Scope = scope
	record.Owner = owner
	record.ExternalConversationKey = externalKey
	record.IdempotencyKey = idempotencyKey
	err := t.tx.QueryRowContext(t.ctx, `
		SELECT request_hash, resource_type, resource_id, created_at
		FROM bkn_trace_idempotency_records
		WHERE scope=? AND tenant_id=? AND business_domain_id=?
		  AND application_principal_id=? AND effective_subject_type=?
		  AND effective_subject_id=? AND delegation_id=? AND external_conversation_key=?
		  AND idempotency_key=? FOR UPDATE`,
		scope, owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID,
		externalKey, idempotencyKey,
	).Scan(&record.RequestHash, &record.ResourceType, &record.ResourceID, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.IdempotencyRecord{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.IdempotencyRecord{}, false
	}
	return record, true
}

func (t *transaction) SaveIdempotency(record sessionvo.IdempotencyRecord) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_idempotency_records (
			scope, tenant_id, business_domain_id, application_principal_id,
			effective_subject_type, effective_subject_id, delegation_id, external_conversation_key,
			idempotency_key, request_hash, resource_type, resource_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Scope, record.Owner.TenantID, record.Owner.BusinessDomainID,
		record.Owner.ApplicationPrincipalID, record.Owner.EffectiveSubjectType,
		record.Owner.EffectiveSubjectID, record.Owner.DelegationID, record.ExternalConversationKey,
		record.IdempotencyKey, record.RequestHash, record.ResourceType,
		record.ResourceID, record.CreatedAt,
	)
}

func (t *transaction) ListConversations(owner sessionvo.Owner, limit int) []sessionvo.Conversation {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, conversationSelect+`
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		ORDER BY updated_at DESC, conversation_id DESC LIMIT ?`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, limit,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Conversation
	for rows.Next() {
		value, scanErr := scanConversationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveConversation(conversation sessionvo.Conversation) {
	if t.err != nil {
		return
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_conversations WHERE conversation_id=?",
		conversation.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_conversations (
				conversation_id, tenant_id, business_domain_id, application_principal_id,
				agent_name, effective_subject_type, effective_subject_id, delegation_id,
				external_conversation_key, generation, status, one_shot, row_version,
				created_at, updated_at, closed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			conversation.ID, conversation.Owner.TenantID, conversation.Owner.BusinessDomainID,
			conversation.Owner.ApplicationPrincipalID, conversation.AgentName, conversation.Owner.EffectiveSubjectType,
			conversation.Owner.EffectiveSubjectID, conversation.Owner.DelegationID,
			conversation.ExternalConversationKey, conversation.Generation, conversation.Status,
			conversation.OneShot, conversation.RowVersion, conversation.CreatedAt,
			conversation.UpdatedAt, nullableTime(conversation.ClosedAt),
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_conversations SET agent_name=?, status=?, one_shot=?, row_version=?,
				updated_at=?, closed_at=? WHERE conversation_id=?`,
			conversation.AgentName, conversation.Status, conversation.OneShot, conversation.RowVersion,
			conversation.UpdatedAt, nullableTime(conversation.ClosedAt), conversation.ID,
		)
	}
}

func (t *transaction) FindActiveInteraction(conversationID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE conversation_id=? AND execution_status='active' LIMIT 1 FOR UPDATE`, conversationID))
}

func (t *transaction) FindInteraction(interactionID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE interaction_id=? FOR UPDATE`, interactionID))
}

func (t *transaction) PeekInteraction(interactionID string) (sessionvo.Interaction, bool) {
	if t.err != nil {
		return sessionvo.Interaction{}, false
	}
	return t.scanInteraction(t.tx.QueryRowContext(t.ctx, interactionSelect+`
		WHERE interaction_id=?`, interactionID))
}

func (t *transaction) NextInteractionOrdinal(conversationID string) uint64 {
	if t.err != nil {
		return 0
	}
	var ordinal uint64
	t.err = t.tx.QueryRowContext(t.ctx,
		"SELECT COALESCE(MAX(ordinal_no), 0) + 1 FROM bkn_trace_interactions WHERE conversation_id=? FOR UPDATE",
		conversationID,
	).Scan(&ordinal)
	return ordinal
}

func (t *transaction) SaveInteraction(interaction sessionvo.Interaction) {
	if t.err != nil {
		return
	}
	manifest := marshalJSON(interaction.ClosureManifest)
	var assemblerDeadline any
	if interaction.ClosureManifest != nil {
		assemblerDeadline = nullableTime(interaction.ClosureManifest.AssemblerDeadline)
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_interactions WHERE interaction_id=?",
		interaction.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_interactions (
				interaction_id, conversation_id, ordinal_no, execution_status, evidence_status,
				start_idempotency_key, terminal_idempotency_key, terminal_payload_hash,
				closure_manifest, assembler_deadline, lease_token, lease_epoch, lease_version, lease_expires_at,
				row_version, created_at, updated_at, terminal_at
			) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?,
				?, ?, ?, ?, ?, ?, ?, ?)`,
			interaction.ID, interaction.ConversationID, interaction.Ordinal,
			interaction.ExecutionStatus, interaction.EvidenceStatus, interaction.StartIdempotencyKey,
			interaction.TerminalIdempotencyKey, interaction.TerminalPayloadHash, manifest,
			assemblerDeadline, interaction.LeaseToken, interaction.LeaseEpoch, interaction.LeaseVersion,
			interaction.LeaseExpiresAt, interaction.RowVersion, interaction.CreatedAt,
			interaction.UpdatedAt, nullableTime(interaction.TerminalAt),
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_interactions SET execution_status=?, evidence_status=?,
				terminal_idempotency_key=NULLIF(?, ''), terminal_payload_hash=NULLIF(?, ''),
				closure_manifest=NULLIF(?, ''), assembler_deadline=?, lease_token=?, lease_epoch=?, lease_version=?,
				lease_expires_at=?, row_version=?, updated_at=?, terminal_at=?
			WHERE interaction_id=?`,
			interaction.ExecutionStatus, interaction.EvidenceStatus,
			interaction.TerminalIdempotencyKey, interaction.TerminalPayloadHash, manifest,
			assemblerDeadline, interaction.LeaseToken, interaction.LeaseEpoch, interaction.LeaseVersion,
			interaction.LeaseExpiresAt, interaction.RowVersion, interaction.UpdatedAt,
			nullableTime(interaction.TerminalAt), interaction.ID,
		)
	}
}

func (t *transaction) FindOperationByKey(interactionID, operationKey string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE interaction_id=? AND operation_key=? FOR UPDATE`, interactionID, operationKey))
}

func (t *transaction) FindOperation(operationID string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE operation_id=? FOR UPDATE`, operationID))
}

func (t *transaction) PeekOperation(operationID string) (sessionvo.Operation, bool) {
	if t.err != nil {
		return sessionvo.Operation{}, false
	}
	return t.scanOperation(t.tx.QueryRowContext(t.ctx, operationSelect+`
		WHERE operation_id=?`, operationID))
}

func (t *transaction) ListOperations(interactionID string) []sessionvo.Operation {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, operationSelect+`
		WHERE interaction_id=? ORDER BY operation_id`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Operation
	for rows.Next() {
		value, scanErr := scanOperationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveOperation(operation sessionvo.Operation) {
	if t.err != nil {
		return
	}
	causation := marshalJSON(operation.CausationEventIDs)
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_operations WHERE operation_id=?",
		operation.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_operations (
				operation_id, conversation_id, interaction_id, operation_key, tool_name,
				normalized_input_hash, parent_operation_id, causation_event_ids,
				attempt_no, attempt_status, retryable, row_version, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`,
			operation.ID, operation.ConversationID, operation.InteractionID, operation.OperationKey,
			operation.ToolName, operation.NormalizedInputHash, operation.ParentOperationID,
			causation, operation.Attempt, operation.AttemptStatus, operation.Retryable,
			operation.RowVersion, operation.CreatedAt, operation.UpdatedAt,
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_operations SET attempt_no=?, attempt_status=?, retryable=?,
				row_version=?, updated_at=? WHERE operation_id=?`,
			operation.Attempt, operation.AttemptStatus, operation.Retryable,
			operation.RowVersion, operation.UpdatedAt, operation.ID,
		)
	}
}

func (t *transaction) FindReceipt(receiptID string) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE receipt_id=? FOR UPDATE`, receiptID))
}

func (t *transaction) PeekReceipt(receiptID string) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE receipt_id=?`, receiptID))
}

func (t *transaction) FindReceiptByOperationAttempt(operationID string, attempt uint32) (sessionvo.Receipt, bool) {
	if t.err != nil {
		return sessionvo.Receipt{}, false
	}
	return t.scanReceipt(t.tx.QueryRowContext(t.ctx, receiptSelect+`
		WHERE operation_id=? AND attempt_no=? FOR UPDATE`, operationID, attempt))
}

func (t *transaction) ListReceipts(interactionID string) []sessionvo.Receipt {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, receiptSelect+`
		WHERE interaction_id=? ORDER BY receipt_id`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Receipt
	for rows.Next() {
		value, scanErr := scanReceiptRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveReceipt(receipt sessionvo.Receipt) {
	if t.err != nil {
		return
	}
	var exists int
	err := t.tx.QueryRowContext(t.ctx,
		"SELECT 1 FROM bkn_trace_receipts WHERE receipt_id=?",
		receipt.ID,
	).Scan(&exists)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, t.err = t.tx.ExecContext(t.ctx, `
			INSERT INTO bkn_trace_receipts (
				receipt_id, schema_version, tenant_id, business_domain_id,
				application_principal_id, effective_subject_type, effective_subject_id,
				delegation_id, conversation_id, interaction_id, operation_id, attempt_no,
				operation_key, tool_name, normalized_input_hash, receipt_status,
				evidence_durability, required_receipt, request_id, trace_id,
				causation_event_ids, observed_evidence_refs, business_refs, artifact_refs,
				partial_reasons, row_version, issued_at, terminal_at, payload_hash
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
				NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''))`,
			receipt.ID, receipt.SchemaVersion, receipt.Owner.TenantID, receipt.Owner.BusinessDomainID,
			receipt.Owner.ApplicationPrincipalID, receipt.Owner.EffectiveSubjectType,
			receipt.Owner.EffectiveSubjectID, receipt.Owner.DelegationID,
			receipt.ConversationID, receipt.InteractionID, receipt.OperationID, receipt.Attempt,
			receipt.OperationKey, receipt.ToolName, receipt.NormalizedInputHash,
			receipt.Status, receipt.EvidenceDurability, receipt.Required, receipt.RequestID,
			receipt.TraceID, marshalJSON(receipt.CausationEventIDs),
			marshalJSON(receipt.ObservedEvidenceRefs), marshalJSON(receipt.BusinessRefs),
			marshalJSON(receipt.ArtifactRefs), marshalJSON(receipt.PartialReasons),
			receipt.RowVersion, receipt.IssuedAt, nullableTime(receipt.TerminalAt), receipt.PayloadHash,
		)
	case err != nil:
		t.err = err
	default:
		_, t.err = t.tx.ExecContext(t.ctx, `
			UPDATE bkn_trace_receipts SET receipt_status=?, evidence_durability=?,
				request_id=NULLIF(?, ''), trace_id=NULLIF(?, ''), observed_evidence_refs=NULLIF(?, ''),
				business_refs=NULLIF(?, ''), artifact_refs=NULLIF(?, ''),
				partial_reasons=NULLIF(?, ''), row_version=?, terminal_at=?,
				payload_hash=NULLIF(?, '')
			WHERE receipt_id=?`,
			receipt.Status, receipt.EvidenceDurability, receipt.RequestID, receipt.TraceID,
			marshalJSON(receipt.ObservedEvidenceRefs), marshalJSON(receipt.BusinessRefs),
			marshalJSON(receipt.ArtifactRefs), marshalJSON(receipt.PartialReasons),
			receipt.RowVersion, nullableTime(receipt.TerminalAt), receipt.PayloadHash, receipt.ID,
		)
	}
}

func (t *transaction) ListRequests(owner sessionvo.Owner, limit int) []sessionvo.RequestSummary {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT request_id, MIN(conversation_id), MIN(interaction_id),
			COUNT(DISTINCT operation_id), COUNT(*),
			MAX(COALESCE(terminal_at, issued_at))
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id IS NOT NULL AND request_id<>''
		GROUP BY request_id
		ORDER BY MAX(COALESCE(terminal_at, issued_at)) DESC, request_id DESC
		LIMIT ?`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, limit,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.RequestSummary
	for rows.Next() {
		var value sessionvo.RequestSummary
		if err := rows.Scan(
			&value.RequestID, &value.ConversationID, &value.InteractionID,
			&value.OperationCount, &value.ReceiptCount, &value.UpdatedAt,
		); err != nil {
			t.err = err
			return nil
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.err = err
		return nil
	}
	if err := rows.Close(); err != nil {
		t.err = err
		return nil
	}
	for index := range result {
		result[index].TraceIDs = t.listRequestTraceIDs(owner, result[index].RequestID)
	}
	return result
}

func (t *transaction) FindRequest(owner sessionvo.Owner, requestID string) (sessionvo.RequestSummary, bool) {
	if t.err != nil {
		return sessionvo.RequestSummary{}, false
	}
	var value sessionvo.RequestSummary
	err := t.tx.QueryRowContext(t.ctx, `
		SELECT request_id, MIN(conversation_id), MIN(interaction_id),
			COUNT(DISTINCT operation_id), COUNT(*),
			MAX(COALESCE(terminal_at, issued_at))
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id=?
		GROUP BY request_id`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, requestID,
	).Scan(
		&value.RequestID, &value.ConversationID, &value.InteractionID,
		&value.OperationCount, &value.ReceiptCount, &value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.RequestSummary{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.RequestSummary{}, false
	}
	value.TraceIDs = t.listRequestTraceIDs(owner, requestID)
	return value, true
}

func (t *transaction) listRequestTraceIDs(owner sessionvo.Owner, requestID string) []string {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT DISTINCT trace_id
		FROM bkn_trace_receipts
		WHERE tenant_id=? AND business_domain_id=? AND application_principal_id=?
		  AND effective_subject_type=? AND effective_subject_id=? AND delegation_id=?
		  AND request_id=? AND trace_id IS NOT NULL AND trace_id<>''
		ORDER BY trace_id`,
		owner.TenantID, owner.BusinessDomainID, owner.ApplicationPrincipalID,
		owner.EffectiveSubjectType, owner.EffectiveSubjectID, owner.DelegationID, requestID,
	)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []string
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&traceID); err != nil {
			t.err = err
			return nil
		}
		result = append(result, traceID)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListExpiredActiveInteractions(limit int) []sessionvo.Interaction {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+`
		WHERE execution_status='active' AND lease_expires_at<=UTC_TIMESTAMP(6)
		ORDER BY lease_expires_at, interaction_id LIMIT ?`, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Interaction
	for rows.Next() {
		interaction, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, interaction)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListIdleOneShotConversations(
	cutoff time.Time,
	limit int,
) []sessionvo.Conversation {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, conversationSelect+`
		WHERE one_shot=TRUE AND status='active' AND updated_at<=?
		  AND NOT EXISTS (
			SELECT 1 FROM bkn_trace_interactions
			WHERE bkn_trace_interactions.conversation_id=bkn_trace_conversations.conversation_id
		  )
		ORDER BY updated_at, conversation_id LIMIT ? FOR UPDATE`, cutoff, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Conversation
	for rows.Next() {
		conversation, scanErr := scanConversationRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, conversation)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) ListAssemblyDueInteractions(limit int) []sessionvo.Interaction {
	if t.err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.tx.QueryContext(t.ctx, interactionSelect+`
		WHERE evidence_status='assembling' AND assembler_deadline<=UTC_TIMESTAMP(6)
		ORDER BY assembler_deadline, interaction_id LIMIT ?`, limit)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.Interaction
	for rows.Next() {
		interaction, scanErr := scanInteractionRows(rows)
		if scanErr != nil {
			t.err = scanErr
			return nil
		}
		result = append(result, interaction)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) AppendProjection(mutation sessionvo.ProjectionMutation) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_projection_outbox (
			aggregate_type, aggregate_id, aggregate_version, event_type, event_id, payload,
			status, attempts, available_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		mutation.AggregateType, mutation.AggregateID, mutation.AggregateVersion,
		mutation.EventType,
		mutation.EventID, mutation.Payload, t.now, t.now,
	)
}

func (t *transaction) ListAssemblyRevisions(interactionID string) []sessionvo.AssemblyRevision {
	if t.err != nil {
		return nil
	}
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT revision_id, revision_no, COALESCE(parent_revision_id, ''), interaction_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, COALESCE(partial_reasons, ''),
			trigger_type, created_at
		FROM bkn_trace_assembly_revisions
		WHERE interaction_id=? ORDER BY revision_no`, interactionID)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	var result []sessionvo.AssemblyRevision
	for rows.Next() {
		var value sessionvo.AssemblyRevision
		var receipts, events, reasons string
		if err := rows.Scan(
			&value.ID, &value.RevisionNo, &value.ParentRevisionID, &value.InteractionID,
			&value.CompletionManifestVersion, &receipts, &events, &value.ArtifactManifestHash,
			&value.Completeness, &reasons, &value.Trigger, &value.CreatedAt,
		); err != nil {
			t.err = err
			return nil
		}
		unmarshalJSON(receipts, &value.IncludedReceiptIDs)
		unmarshalJSON(events, &value.IncludedEventIDs)
		unmarshalJSON(reasons, &value.PartialReasons)
		result = append(result, value)
	}
	t.err = rows.Err()
	return result
}

func (t *transaction) SaveAssemblyRevision(revision sessionvo.AssemblyRevision) {
	if t.err != nil {
		return
	}
	_, t.err = t.tx.ExecContext(t.ctx, `
		INSERT INTO bkn_trace_assembly_revisions (
			revision_id, interaction_id, revision_no, parent_revision_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, partial_reasons,
			trigger_type, created_at
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		revision.ID, revision.InteractionID, revision.RevisionNo, revision.ParentRevisionID,
		revision.CompletionManifestVersion, marshalJSON(revision.IncludedReceiptIDs),
		marshalJSON(revision.IncludedEventIDs), revision.ArtifactManifestHash,
		revision.Completeness, marshalJSON(revision.PartialReasons),
		revision.Trigger, revision.CreatedAt,
	)
}

const conversationSelect = `SELECT conversation_id, tenant_id, business_domain_id,
	application_principal_id, agent_name, effective_subject_type, effective_subject_id,
	COALESCE(delegation_id, ''), external_conversation_key, generation, status,
	one_shot, row_version, created_at, updated_at, closed_at
	FROM bkn_trace_conversations`

type rowScanner interface {
	Scan(dest ...any) error
}

func (t *transaction) scanConversation(row rowScanner) (sessionvo.Conversation, bool) {
	value, err := scanConversationRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Conversation{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Conversation{}, false
	}
	return value, true
}

func scanConversationRows(row rowScanner) (sessionvo.Conversation, error) {
	var value sessionvo.Conversation
	var closedAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.Owner.TenantID, &value.Owner.BusinessDomainID,
		&value.Owner.ApplicationPrincipalID, &value.AgentName, &value.Owner.EffectiveSubjectType,
		&value.Owner.EffectiveSubjectID, &value.Owner.DelegationID,
		&value.ExternalConversationKey, &value.Generation, &value.Status,
		&value.OneShot, &value.RowVersion, &value.CreatedAt, &value.UpdatedAt, &closedAt,
	)
	if err != nil {
		return sessionvo.Conversation{}, err
	}
	if closedAt.Valid {
		value.ClosedAt = &closedAt.Time
	}
	return value, nil
}

const interactionSelect = `SELECT interaction_id, conversation_id, ordinal_no,
	execution_status, evidence_status, start_idempotency_key,
	COALESCE(terminal_idempotency_key, ''), COALESCE(terminal_payload_hash, ''),
	COALESCE(closure_manifest, ''), lease_token, lease_epoch, lease_version,
	lease_expires_at, row_version, created_at, updated_at, terminal_at
	FROM bkn_trace_interactions`

func (t *transaction) scanInteraction(row rowScanner) (sessionvo.Interaction, bool) {
	value, err := scanInteractionRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Interaction{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Interaction{}, false
	}
	return value, true
}

func scanInteractionRows(row rowScanner) (sessionvo.Interaction, error) {
	var value sessionvo.Interaction
	var manifest string
	var terminalAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.ConversationID, &value.Ordinal, &value.ExecutionStatus,
		&value.EvidenceStatus, &value.StartIdempotencyKey,
		&value.TerminalIdempotencyKey, &value.TerminalPayloadHash, &manifest,
		&value.LeaseToken, &value.LeaseEpoch, &value.LeaseVersion,
		&value.LeaseExpiresAt, &value.RowVersion, &value.CreatedAt, &value.UpdatedAt, &terminalAt,
	)
	if err != nil {
		return sessionvo.Interaction{}, err
	}
	if manifest != "" {
		var decoded sessionvo.ClosureManifest
		if err := json.Unmarshal([]byte(manifest), &decoded); err != nil {
			return sessionvo.Interaction{}, err
		}
		value.ClosureManifest = &decoded
	}
	if terminalAt.Valid {
		value.TerminalAt = &terminalAt.Time
	}
	return value, nil
}

const operationSelect = `SELECT operation_id, conversation_id, interaction_id,
	operation_key, tool_name, normalized_input_hash, COALESCE(parent_operation_id, ''),
	COALESCE(causation_event_ids, ''), attempt_no, attempt_status, retryable,
	row_version, created_at, updated_at FROM bkn_trace_operations`

func (t *transaction) scanOperation(row rowScanner) (sessionvo.Operation, bool) {
	value, err := scanOperationRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Operation{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Operation{}, false
	}
	return value, true
}

func scanOperationRows(row rowScanner) (sessionvo.Operation, error) {
	var value sessionvo.Operation
	var causation string
	err := row.Scan(
		&value.ID, &value.ConversationID, &value.InteractionID, &value.OperationKey,
		&value.ToolName, &value.NormalizedInputHash, &value.ParentOperationID, &causation,
		&value.Attempt, &value.AttemptStatus, &value.Retryable, &value.RowVersion,
		&value.CreatedAt, &value.UpdatedAt,
	)
	if err != nil {
		return sessionvo.Operation{}, err
	}
	unmarshalJSON(causation, &value.CausationEventIDs)
	return value, nil
}

const receiptSelect = `SELECT receipt_id, schema_version, tenant_id, business_domain_id,
	application_principal_id, effective_subject_type, effective_subject_id,
	COALESCE(delegation_id, ''), conversation_id, interaction_id, operation_id,
	attempt_no, operation_key, tool_name, normalized_input_hash, receipt_status,
	evidence_durability, required_receipt, COALESCE(request_id, ''), COALESCE(trace_id, ''),
	COALESCE(causation_event_ids, ''), COALESCE(observed_evidence_refs, ''),
	COALESCE(business_refs, ''), COALESCE(artifact_refs, ''), COALESCE(partial_reasons, ''),
	row_version, issued_at, terminal_at, COALESCE(payload_hash, '') FROM bkn_trace_receipts`

func (t *transaction) scanReceipt(row rowScanner) (sessionvo.Receipt, bool) {
	value, err := scanReceiptRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionvo.Receipt{}, false
	}
	if err != nil {
		t.err = err
		return sessionvo.Receipt{}, false
	}
	return value, true
}

func scanReceiptRows(row rowScanner) (sessionvo.Receipt, error) {
	var value sessionvo.Receipt
	var causation, evidence, business, artifacts, reasons string
	var terminalAt sql.NullTime
	err := row.Scan(
		&value.ID, &value.SchemaVersion, &value.Owner.TenantID, &value.Owner.BusinessDomainID,
		&value.Owner.ApplicationPrincipalID, &value.Owner.EffectiveSubjectType,
		&value.Owner.EffectiveSubjectID, &value.Owner.DelegationID,
		&value.ConversationID, &value.InteractionID, &value.OperationID, &value.Attempt,
		&value.OperationKey, &value.ToolName, &value.NormalizedInputHash,
		&value.Status, &value.EvidenceDurability, &value.Required,
		&value.RequestID, &value.TraceID, &causation, &evidence, &business, &artifacts,
		&reasons, &value.RowVersion, &value.IssuedAt, &terminalAt, &value.PayloadHash,
	)
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	unmarshalJSON(causation, &value.CausationEventIDs)
	unmarshalJSON(evidence, &value.ObservedEvidenceRefs)
	unmarshalJSON(business, &value.BusinessRefs)
	unmarshalJSON(artifacts, &value.ArtifactRefs)
	unmarshalJSON(reasons, &value.PartialReasons)
	if terminalAt.Valid {
		value.TerminalAt = &terminalAt.Time
	}
	return value, nil
}

func marshalJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return ""
	}
	return string(data)
}

func unmarshalJSON(data string, target any) {
	if data != "" {
		_ = json.Unmarshal([]byte(data), target)
	}
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
