// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// SaveRecheckedRecordIntegrity completes only an explicitly rechecked, exhausted
// generation. It never resets history, forges a source change or races a live
// worker. Verdict and diagnostic completion commit atomically in lock order.
func (s *Store) SaveRecheckedRecordIntegrity(ctx context.Context, id string, stored sessionvo.StoredRecordIntegrity) (bool, error) {
	if err := isessionstore.ValidateRecordIntegrityRetryResult(isessionstore.RecordIntegrityRetry{SourceVersion: stored.SourceVersion}, &stored, ""); err != nil {
		return false, err
	}
	raw, err := isessionstore.MarshalStoredRecordIntegrity(stored)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	eligible, _, err := lockIntegrityRetry(ctx, tx, id, stored.SourceVersion)
	if err != nil || !eligible {
		return false, err
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT retry_status FROM bkn_trace_record_integrity_retries WHERE interaction_id=? AND source_version=? FOR UPDATE`, id, stored.SourceVersion).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != isessionstore.RecordIntegrityExhausted {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE bkn_trace_interactions SET record_integrity_json=? WHERE interaction_id=? AND record_integrity_version=? AND record_integrity_json IS NULL AND execution_status<>?`, string(raw), id, stored.SourceVersion, sessionvo.InteractionActive)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE bkn_trace_record_integrity_retries SET retry_status='succeeded',reason='',lease_token='' WHERE interaction_id=? AND source_version=?`, id, stored.SourceVersion)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
