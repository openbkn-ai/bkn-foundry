-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

-- Background archive runs. A run is submitted by an administrator and executed
-- by a worker under the existing per-kind named lock; batches stay in
-- bkn_trace_archive_jobs and link to their run through batch_progress.run_id.
CREATE TABLE IF NOT EXISTS bkn_trace_archive_runs (
    run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    archive_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    run_trigger VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_job_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NULL,
    batch_run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NULL,
    run_status VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    range_to DATETIME(6) NOT NULL,
    eligible_at_start BIGINT UNSIGNED NOT NULL DEFAULT 0,
    retry_count INT UNSIGNED NOT NULL DEFAULT 0,
    next_attempt_at DATETIME(6) NULL,
    last_failure_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    PRIMARY KEY (run_id),
    INDEX idx_bkn_trace_archive_run_kind_status (archive_kind, run_status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Trace interactions over the per-interaction archive budget. They stay in hot
-- storage, are excluded from selection while policy_version matches the
-- running image, and are listed with the run that met them.
CREATE TABLE IF NOT EXISTS bkn_trace_archive_retained (
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    run_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    stage VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    measured BIGINT NOT NULL,
    measured_at_least TINYINT(1) NOT NULL DEFAULT 0,
    budget BIGINT NOT NULL,
    unit VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    retained_at DATETIME(6) NOT NULL,
    PRIMARY KEY (interaction_id),
    INDEX idx_bkn_trace_archive_retained_run (run_id),
    INDEX idx_bkn_trace_archive_retained_policy (policy_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
