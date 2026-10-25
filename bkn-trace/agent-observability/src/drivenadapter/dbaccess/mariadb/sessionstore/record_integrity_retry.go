// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.RecordIntegrityRetryStore = (*Store)(nil)

const integrityRetryColumns = `interaction_id,source_version,retry_status,attempts,first_attempt_at,last_attempt_at,next_attempt_at,lease_until,lease_token,reason,last_failure_reason`

func scanIntegrityRetry(row *sql.Row) (isessionstore.RecordIntegrityRetry, bool, error) {
	var r isessionstore.RecordIntegrityRetry
	e := row.Scan(&r.InteractionID, &r.SourceVersion, &r.Status, &r.Attempts, &r.FirstAttemptAt, &r.LastAttemptAt, &r.NextAttemptAt, &r.LeaseUntil, &r.Token, &r.Reason, &r.LastFailureReason)
	if errors.Is(e, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, e == nil, e
}
func (s *Store) GetRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	return scanIntegrityRetry(s.db.QueryRowContext(ctx, `SELECT `+integrityRetryColumns+` FROM bkn_trace_record_integrity_retries WHERE interaction_id=? AND source_version=?`, id, version))
}

// Every mutation locks the interaction before its version-specific retry row.
// Server time is read after locks, so contention cannot extend an expired lease.
func lockIntegrityRetry(ctx context.Context, tx *sql.Tx, id string, version uint64) (bool, bool, error) {
	var current uint64
	var status string
	var verdict sql.NullString
	e := tx.QueryRowContext(ctx, `SELECT record_integrity_version,execution_status,record_integrity_json FROM bkn_trace_interactions WHERE interaction_id=? FOR UPDATE`, id).Scan(&current, &status, &verdict)
	if errors.Is(e, sql.ErrNoRows) {
		return false, false, nil
	}
	if e != nil {
		return false, false, e
	}
	return current == version && status != string(sessionvo.InteractionActive) && !verdict.Valid, current != version, nil
}
func retryServerTime(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	e := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now)
	return now.UTC(), e
}
func saveIntegrityRetry(ctx context.Context, tx *sql.Tx, r isessionstore.RecordIntegrityRetry) error {
	_, e := tx.ExecContext(ctx, `UPDATE bkn_trace_record_integrity_retries SET retry_status=?,attempts=?,first_attempt_at=?,last_attempt_at=?,next_attempt_at=?,lease_until=?,lease_token=?,reason=?,last_failure_reason=? WHERE interaction_id=? AND source_version=?`, r.Status, r.Attempts, r.FirstAttemptAt, r.LastAttemptAt, r.NextAttemptAt, r.LeaseUntil, r.Token, r.Reason, r.LastFailureReason, r.InteractionID, r.SourceVersion)
	return e
}
func (s *Store) ClaimRecordIntegrityRetry(ctx context.Context, id string, version uint64) (isessionstore.RecordIntegrityRetry, bool, error) {
	var empty isessionstore.RecordIntegrityRetry
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return empty, false, e
	}
	defer func() { _ = tx.Rollback() }()
	ok, _, e := lockIntegrityRetry(ctx, tx, id, version)
	if e != nil || !ok {
		return empty, false, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE bkn_trace_record_integrity_retries SET retry_status='superseded',reason='source_changed',lease_token='' WHERE interaction_id=? AND source_version<>? AND retry_status IN ('running','retry_wait')`, id, version); e != nil {
		return empty, false, e
	}
	r, exists, e := scanIntegrityRetry(tx.QueryRowContext(ctx, `SELECT `+integrityRetryColumns+` FROM bkn_trace_record_integrity_retries WHERE interaction_id=? AND source_version=? FOR UPDATE`, id, version))
	if e != nil {
		return empty, false, e
	}
	now, e := retryServerTime(ctx, tx)
	if e != nil {
		return empty, false, e
	}
	if exists {
		if !isessionstore.RecordIntegrityRetryDue(r, now) {
			return r, false, nil
		}
		if r.Status == isessionstore.RecordIntegrityRunning {
			r.LastFailureReason = "lease_expired"
		}
		if why := isessionstore.RecordIntegrityExhaustion(r, now); why != "" {
			r.Status = isessionstore.RecordIntegrityExhausted
			r.Reason = why
			r.Token = ""
			if e = saveIntegrityRetry(ctx, tx, r); e != nil {
				return empty, false, e
			}
			return r, false, tx.Commit()
		}
	} else {
		r = isessionstore.RecordIntegrityRetry{InteractionID: id, SourceVersion: version, FirstAttemptAt: now}
	}
	if r.Status == isessionstore.RecordIntegrityRunning {
		r.Reason = "lease_expired"
		r.LastFailureReason = "lease_expired"
	}
	r.Status = isessionstore.RecordIntegrityRunning
	r.Attempts++
	r.LastAttemptAt = now
	r.NextAttemptAt = now
	r.LeaseUntil = now.Add(isessionstore.RecordIntegrityLeaseDuration)
	r.Token = rand.Text()
	if exists {
		e = saveIntegrityRetry(ctx, tx, r)
	} else {
		_, e = tx.ExecContext(ctx, `INSERT INTO bkn_trace_record_integrity_retries (`+integrityRetryColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, r.InteractionID, r.SourceVersion, r.Status, r.Attempts, r.FirstAttemptAt, r.LastAttemptAt, r.NextAttemptAt, r.LeaseUntil, r.Token, r.Reason, r.LastFailureReason)
	}
	if e != nil {
		return empty, false, e
	}
	if e = tx.Commit(); e != nil {
		return empty, false, e
	}
	return r, true, nil
}
func (s *Store) FinishRecordIntegrityRetry(ctx context.Context, claim isessionstore.RecordIntegrityRetry, stored *sessionvo.StoredRecordIntegrity, reason string) (bool, error) {
	if e := isessionstore.ValidateRecordIntegrityRetryResult(claim, stored, reason); e != nil {
		return false, e
	}
	var raw []byte
	var e error
	if stored != nil {
		raw, e = isessionstore.MarshalStoredRecordIntegrity(*stored)
		if e != nil {
			return false, e
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer func() { _ = tx.Rollback() }()
	ok, changed, e := lockIntegrityRetry(ctx, tx, claim.InteractionID, claim.SourceVersion)
	if e != nil || (!ok && !changed) {
		return false, e
	}
	r, found, e := scanIntegrityRetry(tx.QueryRowContext(ctx, `SELECT `+integrityRetryColumns+` FROM bkn_trace_record_integrity_retries WHERE interaction_id=? AND source_version=? FOR UPDATE`, claim.InteractionID, claim.SourceVersion))
	if e != nil {
		return false, e
	}
	now, e := retryServerTime(ctx, tx)
	if e != nil {
		return false, e
	}
	if !found || claim.Token == "" || r.Token != claim.Token || r.Status != isessionstore.RecordIntegrityRunning || !now.Before(r.LeaseUntil) {
		return false, nil
	}
	if changed {
		r.Status = isessionstore.RecordIntegritySuperseded
		r.Reason = "source_changed"
		r.Token = ""
		if e = saveIntegrityRetry(ctx, tx, r); e != nil {
			return false, e
		}
		return false, tx.Commit()
	}
	// The retry deadline gates new claims, not a successful result from a
	// still-valid lease. Failed attempts apply exhaustion below.
	if stored != nil {
		res, e := tx.ExecContext(ctx, `UPDATE bkn_trace_interactions SET record_integrity_json=? WHERE interaction_id=? AND record_integrity_version=? AND record_integrity_json IS NULL AND execution_status<>?`, string(raw), claim.InteractionID, claim.SourceVersion, sessionvo.InteractionActive)
		if e != nil {
			return false, e
		}
		n, e := res.RowsAffected()
		if e != nil || n != 1 {
			return false, e
		}
		r.Status = isessionstore.RecordIntegritySucceeded
		r.Reason = ""
	} else {
		r.Reason = reason
		r.LastFailureReason = reason
		r.Status = isessionstore.RecordIntegrityRetryWait
		r.NextAttemptAt = now.Add(isessionstore.RecordIntegrityBackoff(r.Attempts))
		if why := isessionstore.RecordIntegrityExhaustion(r, now); why != "" {
			r.Status = isessionstore.RecordIntegrityExhausted
			r.Reason = why
		}
	}
	r.Token = ""
	if e = saveIntegrityRetry(ctx, tx, r); e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return true, nil
}
