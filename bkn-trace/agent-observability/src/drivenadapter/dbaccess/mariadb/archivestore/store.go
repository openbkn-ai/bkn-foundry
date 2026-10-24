// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func (store *Store) Create(job archivesvc.Job) error {
	ids, err := json.Marshal(job.CandidateIDs)
	if err != nil {
		return err
	}
	payloads, err := json.Marshal([]archivesvc.Candidate{})
	if err != nil {
		return err
	}
	progress, err := encodeProgress(job.BatchProgress)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`INSERT INTO bkn_trace_archive_jobs (archive_job_id, archive_kind, range_from, range_to, archive_status, candidate_ids, candidate_payloads, candidate_count, manifest_ref, error_message, created_at, updated_at, batch_progress) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Kind, nullableTime(job.Range.From), job.Range.To.UTC(), job.Status, ids, payloads, job.CandidateCount, nullableString(job.ManifestRef), nullableString(job.ErrorMessage), job.CreatedAt.UTC(), job.UpdatedAt.UTC(), progress)
	return err
}

func (store *Store) Update(job archivesvc.Job) error {
	payloads, err := json.Marshal([]archivesvc.Candidate{})
	if err != nil {
		return err
	}
	if job.Status == observabilityvo.ArchiveStatusCleanupIncomplete {
		payloads, err = json.Marshal(job.Candidates)
		if err != nil {
			return err
		}
	}
	progress, err := encodeProgress(job.BatchProgress)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`UPDATE bkn_trace_archive_jobs SET archive_status=?, candidate_payloads=?, manifest_ref=?, error_message=?, updated_at=?, batch_progress=? WHERE archive_job_id=?`, job.Status, payloads, nullableString(job.ManifestRef), nullableString(job.ErrorMessage), job.UpdatedAt.UTC(), progress, job.ID)
	return err
}

func (store *Store) Latest(kind observabilityvo.ArchiveKind) (archivesvc.Job, bool) {
	row := store.db.QueryRow(`SELECT archive_job_id, range_from, range_to, archive_status, candidate_ids, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at, batch_progress FROM bkn_trace_archive_jobs WHERE archive_kind=? ORDER BY created_at DESC, archive_job_id DESC LIMIT 1`, kind)
	return scan(row, kind)
}

func (store *Store) Get(jobID string) (archivesvc.Job, bool) {
	row := store.db.QueryRow(`SELECT archive_kind, range_from, range_to, archive_status, candidate_ids, candidate_payloads, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at, batch_progress FROM bkn_trace_archive_jobs WHERE archive_job_id=?`, jobID)
	var kind string
	var rangeFrom sql.NullTime
	var rangeTo time.Time
	var status string
	var ids []byte
	var payloads []byte
	var count int
	var manifest, message string
	var createdAt, updatedAt time.Time
	var progressRaw []byte
	err := row.Scan(&kind, &rangeFrom, &rangeTo, &status, &ids, &payloads, &count, &manifest, &message, &createdAt, &updatedAt, &progressRaw)
	if err != nil {
		return archivesvc.Job{}, false
	}
	var candidateIDs []string
	var candidates []archivesvc.Candidate
	if json.Unmarshal(ids, &candidateIDs) != nil || json.Unmarshal(payloads, &candidates) != nil {
		return archivesvc.Job{}, false
	}
	progress, err := decodeProgress(progressRaw)
	if err != nil {
		return archivesvc.Job{}, false
	}
	job := archivesvc.Job{
		ID:             jobID,
		Kind:           observabilityvo.ArchiveKind(kind),
		Range:          observabilityvo.ArchiveRange{To: rangeTo.UTC()},
		Status:         observabilityvo.ArchiveStatus(status),
		CandidateIDs:   candidateIDs,
		Candidates:     candidates,
		CandidateCount: count,
		ManifestRef:    manifest,
		ErrorMessage:   message,
		CreatedAt:      createdAt.UTC(),
		UpdatedAt:      updatedAt.UTC(),
		BatchProgress:  progress,
	}
	if rangeFrom.Valid {
		job.Range.From = rangeFrom.Time.UTC()
	}
	return job, true
}

func (store *Store) List(kind observabilityvo.ArchiveKind, limit int) []archivesvc.Job {
	if limit <= 0 {
		return nil
	}
	rows, err := store.db.Query(`SELECT archive_job_id, range_from, range_to, archive_status, candidate_ids, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at, batch_progress FROM bkn_trace_archive_jobs WHERE archive_kind=? ORDER BY created_at DESC, archive_job_id DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]archivesvc.Job, 0, limit)
	for rows.Next() {
		var id, status string
		var rangeFrom sql.NullTime
		var rangeTo time.Time
		var ids []byte
		var count int
		var manifest, message string
		var createdAt, updatedAt time.Time
		var progressRaw []byte
		if err := rows.Scan(&id, &rangeFrom, &rangeTo, &status, &ids, &count, &manifest, &message, &createdAt, &updatedAt, &progressRaw); err != nil {
			return nil
		}
		var candidateIDs []string
		if json.Unmarshal(ids, &candidateIDs) != nil {
			return nil
		}
		progress, err := decodeProgress(progressRaw)
		if err != nil {
			return nil
		}
		job := archivesvc.Job{
			ID:             id,
			Kind:           kind,
			Range:          observabilityvo.ArchiveRange{To: rangeTo.UTC()},
			Status:         observabilityvo.ArchiveStatus(status),
			CandidateIDs:   candidateIDs,
			CandidateCount: count,
			ManifestRef:    manifest,
			ErrorMessage:   message,
			CreatedAt:      createdAt.UTC(),
			UpdatedAt:      updatedAt.UTC(),
			BatchProgress:  progress,
		}
		if rangeFrom.Valid {
			job.Range.From = rangeFrom.Time.UTC()
		}
		jobs = append(jobs, job)
	}
	if rows.Err() != nil {
		return nil
	}
	return jobs
}

func scan(row *sql.Row, kind observabilityvo.ArchiveKind) (archivesvc.Job, bool) {
	var id, status string
	var rangeFrom sql.NullTime
	var rangeTo time.Time
	var ids []byte
	var count int
	var manifest, message string
	var createdAt, updatedAt time.Time
	var progressRaw []byte
	if err := row.Scan(&id, &rangeFrom, &rangeTo, &status, &ids, &count, &manifest, &message, &createdAt, &updatedAt, &progressRaw); err != nil {
		return archivesvc.Job{}, false
	}
	var candidateIDs []string
	if err := json.Unmarshal(ids, &candidateIDs); err != nil {
		return archivesvc.Job{}, false
	}
	progress, err := decodeProgress(progressRaw)
	if err != nil {
		return archivesvc.Job{}, false
	}
	job := archivesvc.Job{
		ID:             id,
		Kind:           kind,
		Range:          observabilityvo.ArchiveRange{To: rangeTo.UTC()},
		Status:         observabilityvo.ArchiveStatus(status),
		CandidateIDs:   candidateIDs,
		CandidateCount: count,
		ManifestRef:    manifest,
		ErrorMessage:   message,
		CreatedAt:      createdAt.UTC(),
		UpdatedAt:      updatedAt.UTC(),
		BatchProgress:  progress,
	}
	if rangeFrom.Valid {
		job.Range.From = rangeFrom.Time.UTC()
	}
	return job, true
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

var _ archivesvc.Store = (*Store)(nil)
var _ archivesvc.KindLockStore = (*Store)(nil)

func encodeProgress(progress *archivesvc.BatchProgress) (any, error) {
	if progress == nil {
		return nil, nil
	}
	return json.Marshal(progress)
}

func decodeProgress(raw []byte) (*archivesvc.BatchProgress, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var progress *archivesvc.BatchProgress
	if err := json.Unmarshal(raw, &progress); err != nil {
		return nil, err
	}
	return progress, nil
}

// HasIncompleteCleanup checks all jobs, including older jobs hidden by Latest.
// SQL errors are returned so callers cannot accidentally treat failure as permission.
func (store *Store) HasIncompleteCleanup(ctx context.Context, kind observabilityvo.ArchiveKind) (bool, error) {
	var found bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bkn_trace_archive_jobs WHERE archive_kind=? AND archive_status=? LIMIT 1)`, kind, observabilityvo.ArchiveStatusCleanupIncomplete).Scan(&found)
	return found, err
}

// WithKindLock pins the connection: MariaDB named locks belong to sessions,
// not transactions. With both valid kinds locked, the pool still needs one
// connection for callbacks; finite pools therefore require at least three.
func (store *Store) WithKindLock(ctx context.Context, kind observabilityvo.ArchiveKind, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("archive lock callback is required")
	}
	if store.db.Stats().MaxOpenConnections > 0 && store.db.Stats().MaxOpenConnections < 3 {
		return fmt.Errorf("archive kind lock requires at least three database connections")
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("archive kind lock connection close failed")
		}
	}()
	var schema sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&schema); err != nil {
		return err
	}
	if !schema.Valid || schema.String == "" {
		return fmt.Errorf("archive kind lock requires a selected database")
	}
	name := archiveLockName(schema.String, kind)
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, name).Scan(&locked); err != nil {
		// A lost response may have acquired the lock; never return that session to pool.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return err
	}
	if !locked.Valid {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("archive kind lock returned NULL")
	}
	if locked.Int64 == 0 {
		return archivesvc.ErrArchiveBusy
	}
	if locked.Int64 != 1 {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("archive kind lock returned invalid result")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released sql.NullInt64
		err := conn.QueryRowContext(cleanup, `SELECT RELEASE_LOCK(?)`, name).Scan(&released)
		if err != nil || !released.Valid || released.Int64 != 1 {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			// Cleanup diagnostics must not turn already completed archive work into an HTTP error.
			log.Printf("archive kind lock release failed; database session discarded")
		}
	}()
	return fn()
}

func archiveLockName(schema string, kind observabilityvo.ArchiveKind) string {
	sum := sha256.Sum256([]byte(schema + "\x00" + string(kind)))
	//64 bytes also works with servers enforcing the MySQL name length limit.
	return fmt.Sprintf("bkn_arc_%x", sum[:28])
}
