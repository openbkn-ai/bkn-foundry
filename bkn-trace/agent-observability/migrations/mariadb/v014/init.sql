CREATE TABLE IF NOT EXISTS bkn_trace_log_source_coverage (
    source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    deployment_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    coverage_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    dropped_records BIGINT UNSIGNED NOT NULL DEFAULT 0,
    first_observed_at DATETIME(6) NULL,
    last_observed_at DATETIME(6) NULL,
    recovered_at DATETIME(6) NULL,
    row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (source_id, deployment_id),
    INDEX idx_bkn_trace_log_source_coverage_state (coverage_state, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
