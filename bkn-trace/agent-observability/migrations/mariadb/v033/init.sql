-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- Trace/Evidence control-plane projection. v032 remains the immutable
-- Evidence admission history; v033 stores only mutable control state,
-- operation convergence facts, live endpoint leases and acknowledgements.

CREATE TABLE IF NOT EXISTS bkn_trace_capture_control_state (
    singleton_id TINYINT UNSIGNED NOT NULL,
    current_revision BIGINT UNSIGNED NOT NULL,
    desired_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    last_stable_revision BIGINT UNSIGNED NOT NULL,
    active_operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    coverage_gap BOOLEAN NOT NULL DEFAULT FALSE,
    coverage_gap_reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    coverage_gap_started_at DATETIME(6) NULL,
    coverage_gap_updated_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (singleton_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_capture_operations (
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    policy_revision BIGINT UNSIGNED NOT NULL,
    requested_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    expected_revision BIGINT UNSIGNED NOT NULL,
    phase VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    compensation_revision BIGINT UNSIGNED NULL,
    restored_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    lease_owner VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    lease_token BIGINT UNSIGNED NOT NULL DEFAULT 0,
    lease_expires_at DATETIME(6) NULL,
    convergence_deadline DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    terminal_at DATETIME(6) NULL,
    PRIMARY KEY (operation_id),
    KEY idx_bkn_trace_capture_operation_revision (policy_revision),
    KEY idx_bkn_trace_capture_operation_phase (phase, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_capture_endpoint_leases (
    endpoint_kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    instance_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workload_identity VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    process_boot_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    observed_revision BIGINT UNSIGNED NOT NULL,
    ready BOOLEAN NOT NULL,
    heartbeat_at DATETIME(6) NOT NULL,
    lease_expires_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (endpoint_kind, instance_id),
    KEY idx_bkn_trace_capture_endpoint_lease_expiry (lease_expires_at, ready)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_capture_operation_acknowledgements (
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    endpoint_kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    instance_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workload_identity VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    process_boot_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    policy_revision BIGINT UNSIGNED NOT NULL,
    ready BOOLEAN NOT NULL,
    ack_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    acknowledged_at DATETIME(6) NULL,
    exported_count BIGINT UNSIGNED NULL,
    dropped_count BIGINT UNSIGNED NULL,
    unaccounted_count BIGINT UNSIGNED NULL,
    trace_disposition VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_accepted_sequence BIGINT UNSIGNED NULL,
    published_count BIGINT UNSIGNED NULL,
    queue_empty BOOLEAN NULL,
    evidence_disposition VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    gap_reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    PRIMARY KEY (operation_id, endpoint_kind, instance_id),
    KEY idx_bkn_trace_capture_ack_operation (operation_id, policy_revision, ack_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_capture_operation_events (
    event_sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    phase VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    lease_token BIGINT UNSIGNED NOT NULL DEFAULT 0,
    error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    gap_reason VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    recorded_at DATETIME(6) NOT NULL,
    PRIMARY KEY (event_sequence),
    KEY idx_bkn_trace_capture_event_operation (operation_id, event_sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
