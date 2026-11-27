// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivestore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

const runColumns = "run_id, archive_kind, run_trigger, COALESCE(source_job_id,''), COALESCE(batch_run_id,''), run_status, range_to, eligible_at_start, retry_count, next_attempt_at, COALESCE(last_failure_code,''), created_at, started_at, updated_at, finished_at"

func (store *Store) CreateRun(ctx context.Context, run archivesvc.Run) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO bkn_trace_archive_runs (run_id, archive_kind, run_trigger, source_job_id, batch_run_id, run_status, range_to, eligible_at_start, retry_count, next_attempt_at, last_failure_code, created_at, started_at, updated_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.Kind, run.Trigger, nullableString(run.SourceJobID), nullableString(run.BatchRunID), run.Status, run.RangeTo.UTC(), run.EligibleAtStart, run.RetryCount,
		nullableTime(run.NextAttemptAt), nullableString(run.LastFailureCode), run.CreatedAt.UTC(), nullableTime(run.StartedAt), run.UpdatedAt.UTC(), nullableTime(run.FinishedAt))
	return err
}

func (store *Store) UpdateRun(ctx context.Context, run archivesvc.Run) error {
	result, err := store.db.ExecContext(ctx, `UPDATE bkn_trace_archive_runs SET run_status=?, retry_count=?, next_attempt_at=?, last_failure_code=?, started_at=?, updated_at=?, finished_at=? WHERE run_id=?`,
		run.Status, run.RetryCount, nullableTime(run.NextAttemptAt), nullableString(run.LastFailureCode), nullableTime(run.StartedAt), run.UpdatedAt.UTC(), nullableTime(run.FinishedAt), run.ID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		// MariaDB reports 0 for an identical row; confirm the run exists.
		if _, found, getErr := store.GetRun(ctx, run.ID); getErr != nil {
			return getErr
		} else if !found {
			return archivesvc.ErrRunNotFound
		}
	}
	return nil
}

func (store *Store) GetRun(ctx context.Context, id string) (archivesvc.Run, bool, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM bkn_trace_archive_runs WHERE run_id=?`, id))
}

func (store *Store) ActiveRun(ctx context.Context, kind observabilityvo.ArchiveKind) (archivesvc.Run, bool, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM bkn_trace_archive_runs WHERE archive_kind=? AND run_status IN (?, ?, ?) ORDER BY created_at ASC, run_id ASC LIMIT 1`,
		kind, archivesvc.RunQueued, archivesvc.RunRunning, archivesvc.RunRetryWait))
}

func (store *Store) LatestRun(ctx context.Context, kind observabilityvo.ArchiveKind) (archivesvc.Run, bool, error) {
	return scanRun(store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM bkn_trace_archive_runs WHERE archive_kind=? ORDER BY created_at DESC, run_id DESC LIMIT 1`, kind))
}

func batchRunPattern(batchRunID string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(batchRunID) + `\_%`
}

// LatestBatchProgress reads only progress, never the frozen payloads, so the
// overview can poll it while a large batch is in flight.
func (store *Store) LatestBatchProgress(ctx context.Context, batchRunID string) (*archivesvc.BatchProgress, error) {
	var raw []byte
	err := store.db.QueryRowContext(ctx, `SELECT batch_progress FROM bkn_trace_archive_jobs WHERE archive_job_id LIKE ? ORDER BY archive_job_id DESC LIMIT 1`, batchRunPattern(batchRunID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeProgress(raw)
}

// LatestBatch finds the newest job of a batch run. Job IDs are the run ID plus
// a zero-padded batch number, so the primary key orders them.
func (store *Store) LatestBatch(ctx context.Context, batchRunID string) (archivesvc.Job, bool, error) {
	var id string
	err := store.db.QueryRowContext(ctx, `SELECT archive_job_id FROM bkn_trace_archive_jobs WHERE archive_job_id LIKE ? ORDER BY archive_job_id DESC LIMIT 1`, batchRunPattern(batchRunID)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return archivesvc.Job{}, false, nil
	}
	if err != nil {
		return archivesvc.Job{}, false, err
	}
	job, ok := store.Get(id)
	if !ok {
		return archivesvc.Job{}, false, errors.New("archive batch could not be read")
	}
	return job, true, nil
}

func (store *Store) RetainInteraction(ctx context.Context, item archivesvc.RetainedInteraction) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO bkn_trace_archive_retained (interaction_id, run_id, policy_version, stage, measured, measured_at_least, budget, unit, retained_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE run_id=VALUES(run_id), policy_version=VALUES(policy_version), stage=VALUES(stage), measured=VALUES(measured), measured_at_least=VALUES(measured_at_least), budget=VALUES(budget), unit=VALUES(unit), retained_at=VALUES(retained_at)`,
		item.InteractionID, item.RunID, item.PolicyVersion, item.Stage, item.Measured, item.AtLeast, item.Budget, item.Unit, item.RetainedAt.UTC())
	return err
}

func (store *Store) RetainedIDs(ctx context.Context, policy string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT interaction_id FROM bkn_trace_archive_retained WHERE policy_version=?`, policy)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (store *Store) ListRetained(ctx context.Context, runID, policy string, limit int) ([]archivesvc.RetainedInteraction, error) {
	query := `SELECT interaction_id, run_id, policy_version, stage, measured, measured_at_least, budget, unit, retained_at FROM bkn_trace_archive_retained WHERE (?='' OR run_id=?) AND (?='' OR policy_version=?) ORDER BY retained_at DESC, interaction_id ASC`
	args := []any{runID, runID, policy, policy}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]archivesvc.RetainedInteraction, 0)
	for rows.Next() {
		var item archivesvc.RetainedInteraction
		if err := rows.Scan(&item.InteractionID, &item.RunID, &item.PolicyVersion, &item.Stage, &item.Measured, &item.AtLeast, &item.Budget, &item.Unit, &item.RetainedAt); err != nil {
			return nil, err
		}
		item.RetainedAt = item.RetainedAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRun(row *sql.Row) (archivesvc.Run, bool, error) {
	var run archivesvc.Run
	var kind, trigger, status string
	var next, started, finished sql.NullTime
	err := row.Scan(&run.ID, &kind, &trigger, &run.SourceJobID, &run.BatchRunID, &status, &run.RangeTo, &run.EligibleAtStart, &run.RetryCount, &next, &run.LastFailureCode, &run.CreatedAt, &started, &run.UpdatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return archivesvc.Run{}, false, nil
	}
	if err != nil {
		return archivesvc.Run{}, false, err
	}
	run.Kind, run.Trigger, run.Status = observabilityvo.ArchiveKind(kind), archivesvc.RunTrigger(trigger), archivesvc.RunStatus(status)
	run.RangeTo, run.CreatedAt, run.UpdatedAt = run.RangeTo.UTC(), run.CreatedAt.UTC(), run.UpdatedAt.UTC()
	run.NextAttemptAt, run.StartedAt, run.FinishedAt = utcOrZero(next), utcOrZero(started), utcOrZero(finished)
	return run, true, nil
}

func utcOrZero(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

var _ archivesvc.RunStore = (*Store)(nil)
