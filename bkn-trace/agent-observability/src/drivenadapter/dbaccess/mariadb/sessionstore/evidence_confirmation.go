// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.EvidenceConfirmationTransaction = (*transaction)(nil)

func (t *transaction) ReadEvidenceEventsByIDs(ids []string) ([]ledgervo.Event, error) {
	if t.err != nil {
		return nil, t.err
	}
	if len(ids) > sessionvo.MaxExpectedEvidenceEvents {
		return nil, isessionstore.ErrEvidenceConfirmationLimit
	}
	if len(ids) == 0 {
		return []ledgervo.Event{}, nil
	}
	args := make([]any, len(ids)+1)
	args[0] = sessionvo.MaxEvidenceConfirmationBytes
	for i, id := range ids {
		args[i+1] = id
	}
	// Ledger writes have committed before this non-locking, primary-key read.
	rows, err := t.tx.QueryContext(t.ctx, `SELECT event_id, payload_hash, OCTET_LENGTH(envelope),
		CASE WHEN SUM(OCTET_LENGTH(envelope)) OVER () <= ? THEN envelope ELSE NULL END
		FROM bkn_trace_evidence_event_ledger WHERE event_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		t.err = err
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]ledgervo.Event, 0, len(ids))
	totalBytes := 0
	for rows.Next() {
		var id, hash string
		var raw []byte
		var envelopeBytes int
		if err := rows.Scan(&id, &hash, &envelopeBytes, &raw); err != nil {
			t.err = err
			return nil, err
		}
		totalBytes += envelopeBytes
		if raw == nil || totalBytes > sessionvo.MaxEvidenceConfirmationBytes {
			return nil, isessionstore.ErrEvidenceConfirmationLimit
		}
		var event ledgervo.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("%w: confirmation.ledger", isessionstore.ErrInvalidEvidenceJSON)
		}
		if event.EventID != id || event.PayloadHash != hash {
			return nil, fmt.Errorf("%w: confirmation.ledger", isessionstore.ErrEvidenceIdentityMismatch)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		t.err = err
		return nil, err
	}
	return result, nil
}
