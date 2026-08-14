package archivestore

import (
	"database/sql"
	"encoding/json"
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
	_, err = store.db.Exec(`INSERT INTO bkn_trace_archive_jobs (archive_job_id, tenant_id, archive_kind, range_from, range_to, archive_status, candidate_ids, candidate_count, manifest_ref, error_message, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.TenantID, job.Kind, nullableTime(job.Range.From), job.Range.To.UTC(), job.Status, ids, job.CandidateCount, nullableString(job.ManifestRef), nullableString(job.ErrorMessage), job.CreatedAt.UTC(), job.UpdatedAt.UTC())
	return err
}

func (store *Store) Update(job archivesvc.Job) error {
	_, err := store.db.Exec(`UPDATE bkn_trace_archive_jobs SET archive_status=?, manifest_ref=?, error_message=?, updated_at=? WHERE archive_job_id=?`, job.Status, nullableString(job.ManifestRef), nullableString(job.ErrorMessage), job.UpdatedAt.UTC(), job.ID)
	return err
}

func (store *Store) Latest(tenantID string, kind observabilityvo.ArchiveKind) (archivesvc.Job, bool) {
	row := store.db.QueryRow(`SELECT archive_job_id, range_from, range_to, archive_status, candidate_ids, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at FROM bkn_trace_archive_jobs WHERE tenant_id=? AND archive_kind=? ORDER BY created_at DESC LIMIT 1`, tenantID, kind)
	return scan(row, tenantID, kind)
}

func (store *Store) Get(jobID string) (archivesvc.Job, bool) {
	row := store.db.QueryRow(`SELECT tenant_id, archive_kind, range_from, range_to, archive_status, candidate_ids, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at FROM bkn_trace_archive_jobs WHERE archive_job_id=?`, jobID)
	var tenantID, kind string
	var rangeFrom sql.NullTime
	var rangeTo time.Time
	var status string
	var ids []byte
	var count int
	var manifest, message string
	var createdAt, updatedAt time.Time
	err := row.Scan(&tenantID, &kind, &rangeFrom, &rangeTo, &status, &ids, &count, &manifest, &message, &createdAt, &updatedAt)
	if err != nil {
		return archivesvc.Job{}, false
	}
	var candidateIDs []string
	if json.Unmarshal(ids, &candidateIDs) != nil {
		return archivesvc.Job{}, false
	}
	job := archivesvc.Job{ID: jobID, TenantID: tenantID, Kind: observabilityvo.ArchiveKind(kind), Range: observabilityvo.ArchiveRange{To: rangeTo.UTC()}, Status: observabilityvo.ArchiveStatus(status), CandidateIDs: candidateIDs, CandidateCount: count, ManifestRef: manifest, ErrorMessage: message, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
	if rangeFrom.Valid {
		job.Range.From = rangeFrom.Time.UTC()
	}
	return job, true
}

func (store *Store) List(tenantID string, kind observabilityvo.ArchiveKind, limit int) []archivesvc.Job {
	if limit <= 0 {
		return nil
	}
	rows, err := store.db.Query(`SELECT archive_job_id, range_from, range_to, archive_status, candidate_ids, candidate_count, COALESCE(manifest_ref,''), COALESCE(error_message,''), created_at, updated_at FROM bkn_trace_archive_jobs WHERE tenant_id=? AND archive_kind=? ORDER BY created_at DESC LIMIT ?`, tenantID, kind, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	jobs := make([]archivesvc.Job, 0, limit)
	for rows.Next() {
		var id, status string
		var rangeFrom sql.NullTime
		var rangeTo time.Time
		var ids []byte
		var count int
		var manifest, message string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &rangeFrom, &rangeTo, &status, &ids, &count, &manifest, &message, &createdAt, &updatedAt); err != nil {
			return nil
		}
		var candidateIDs []string
		if json.Unmarshal(ids, &candidateIDs) != nil {
			return nil
		}
		job := archivesvc.Job{ID: id, TenantID: tenantID, Kind: kind, Range: observabilityvo.ArchiveRange{To: rangeTo.UTC()}, Status: observabilityvo.ArchiveStatus(status), CandidateIDs: candidateIDs, CandidateCount: count, ManifestRef: manifest, ErrorMessage: message, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
		if rangeFrom.Valid {
			job.Range.From = rangeFrom.Time.UTC()
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func scan(row *sql.Row, tenantID string, kind observabilityvo.ArchiveKind) (archivesvc.Job, bool) {
	var id, status string
	var rangeFrom sql.NullTime
	var rangeTo time.Time
	var ids []byte
	var count int
	var manifest, message string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&id, &rangeFrom, &rangeTo, &status, &ids, &count, &manifest, &message, &createdAt, &updatedAt); err != nil {
		return archivesvc.Job{}, false
	}
	var candidateIDs []string
	if err := json.Unmarshal(ids, &candidateIDs); err != nil {
		return archivesvc.Job{}, false
	}
	job := archivesvc.Job{ID: id, TenantID: tenantID, Kind: kind, Range: observabilityvo.ArchiveRange{To: rangeTo.UTC()}, Status: observabilityvo.ArchiveStatus(status), CandidateIDs: candidateIDs, CandidateCount: count, ManifestRef: manifest, ErrorMessage: message, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
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
