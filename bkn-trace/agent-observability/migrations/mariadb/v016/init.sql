CREATE TABLE IF NOT EXISTS bkn_trace_archive_jobs (
    archive_job_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    archive_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    range_from DATETIME(6) NULL,
    range_to DATETIME(6) NOT NULL,
    archive_status VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    candidate_ids LONGTEXT NOT NULL,
    candidate_payloads LONGTEXT NOT NULL,
    candidate_count BIGINT UNSIGNED NOT NULL,
    manifest_ref VARCHAR(1024) NULL,
    error_message VARCHAR(1024) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (archive_job_id),
    INDEX idx_bkn_trace_archive_job_tenant_kind (tenant_id, archive_kind, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
