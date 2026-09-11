// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.EvidenceSnapshotReader = (*Store)(nil)

// ReadEvidenceSnapshot uses only non-locking SELECTs in one InnoDB snapshot.
// Existing lifecycle transactions retain their read-committed/write behavior.
// Ledger rows share this read boundary; external artifact bodies do not.
func (s *Store) ReadEvidenceSnapshot(ctx context.Context, id string) (sessionvo.EvidenceSnapshot, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	reader := &transaction{ctx: ctx, tx: tx, strictEvidenceJSON: true}
	interaction, found := reader.PeekInteraction(id)
	if reader.err != nil {
		return sessionvo.EvidenceSnapshot{}, false, reader.err
	}
	if !found {
		return sessionvo.EvidenceSnapshot{}, false, nil
	}
	operations := reader.ListOperations(id)
	receipts := reader.ListReceipts(id)
	facts := reader.ListOperationCallFacts(id)
	revisions := reader.ListAssemblyRevisions(id)
	if reader.err != nil {
		return sessionvo.EvidenceSnapshot{}, false, reader.err
	}
	result, err := sessionvo.CopyEvidenceSnapshot(interaction, operations, receipts, facts, revisions)
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	result.Ledger, err = reader.readEvidenceLedger(interaction.ConversationID, id)
	if err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sessionvo.EvidenceSnapshot{}, false, err
	}
	return result, true, nil
}
