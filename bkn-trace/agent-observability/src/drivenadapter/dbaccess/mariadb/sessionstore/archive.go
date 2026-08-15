package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// TraceArchiveSource packages terminal Interaction facts from the Trace Core
// authority. It intentionally keeps a Conversation row as a light index after
// its completed Interaction package is removed from hot storage.
type TraceArchiveSource struct{ store *Store }

func NewTraceArchiveSource(store *Store) *TraceArchiveSource {
	return &TraceArchiveSource{store: store}
}

func (source *TraceArchiveSource) Freeze(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, archiveRange observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	if kind != observabilityvo.ArchiveKindTrace {
		return nil, nil
	}
	rows, err := source.store.db.QueryContext(ctx, `SELECT interaction_id FROM bkn_trace_interactions i JOIN bkn_trace_conversations c ON c.conversation_id=i.conversation_id WHERE c.tenant_id=? AND i.execution_status<>? AND i.terminal_at<? ORDER BY i.terminal_at ASC, i.interaction_id ASC`, tenantID, sessionvo.InteractionActive, archiveRange.To.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var candidates []archivesvc.Candidate
	for rows.Next() {
		var interactionID string
		if err := rows.Scan(&interactionID); err != nil {
			return nil, err
		}
		payload, occurredAt, err := source.packageInteraction(ctx, interactionID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, archivesvc.Candidate{ID: interactionID, OccurredAt: occurredAt, Payload: payload})
	}
	return candidates, rows.Err()
}

func (source *TraceArchiveSource) packageInteraction(ctx context.Context, interactionID string) ([]byte, time.Time, error) {
	var interaction sessionvo.Interaction
	var conversation sessionvo.Conversation
	var operations []sessionvo.Operation
	var receipts []sessionvo.Receipt
	var facts []sessionvo.OperationCallFact
	err := source.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		var ok bool
		interaction, ok = tx.PeekInteraction(interactionID)
		if !ok {
			return fmt.Errorf("interaction %s not found", interactionID)
		}
		conversation, ok = tx.PeekConversation(interaction.ConversationID)
		if !ok {
			return fmt.Errorf("conversation %s not found", interaction.ConversationID)
		}
		operations, receipts, facts = tx.ListOperations(interactionID), tx.ListReceipts(interactionID), tx.ListOperationCallFacts(interactionID)
		return nil
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	payload, err := json.Marshal(map[string]any{"conversation": conversation, "interaction": interaction, "operations": operations, "receipts": receipts, "call_facts": facts})
	if err != nil {
		return nil, time.Time{}, err
	}
	if interaction.TerminalAt == nil {
		return nil, time.Time{}, fmt.Errorf("terminal interaction %s has no terminal time", interactionID)
	}
	return payload, *interaction.TerminalAt, nil
}

func (source *TraceArchiveSource) Purge(ctx context.Context, kind observabilityvo.ArchiveKind, tenantID string, candidates []archivesvc.Candidate) error {
	if kind != observabilityvo.ArchiveKindTrace {
		return nil
	}
	tx, err := source.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, candidate := range candidates {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT i.interaction_id FROM bkn_trace_interactions i JOIN bkn_trace_conversations c ON c.conversation_id=i.conversation_id WHERE i.interaction_id=? AND c.tenant_id=? AND i.execution_status<>? FOR UPDATE`, candidate.ID, tenantID, sessionvo.InteractionActive).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM bkn_trace_operation_call_facts WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_receipts WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_operations WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_assembly_revisions WHERE interaction_id=?`,
			`DELETE FROM bkn_trace_interactions WHERE interaction_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
