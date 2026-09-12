// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (t *transaction) readEvidenceLedger(conversationID, interactionID string) (*sessionvo.EvidenceLedgerSnapshot, error) {
	rows, err := t.tx.QueryContext(t.ctx, `
		SELECT event_id, ingest_sequence, envelope
		FROM bkn_trace_evidence_event_ledger WHERE interaction_id=?
		ORDER BY ingest_sequence`, interactionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := &sessionvo.EvidenceLedgerSnapshot{Events: []sessionvo.EvidenceLedgerRecord{}}
	for rows.Next() {
		var record sessionvo.EvidenceLedgerRecord
		if err := rows.Scan(&record.EventID, &record.IngestSequence, &record.Envelope); err != nil {
			return nil, err
		}
		// Decode only for shape and scope checks. Return the original bytes,
		// including unknown fields and numeric representations, not this struct.
		var event ledgervo.Event
		if err := json.Unmarshal(record.Envelope, &event); err != nil {
			return nil, fmt.Errorf("%w: ledger.envelope", isessionstore.ErrInvalidEvidenceJSON)
		}
		if event.EventID != record.EventID || event.InteractionID != interactionID || event.ConversationID != conversationID {
			return nil, fmt.Errorf("%w: ledger.envelope", isessionstore.ErrEvidenceIdentityMismatch)
		}
		result.Events = append(result.Events, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
