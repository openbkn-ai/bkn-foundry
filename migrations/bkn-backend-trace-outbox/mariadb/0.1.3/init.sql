-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

CREATE TABLE IF NOT EXISTS bkn_backend_trace_outbox (
    outbox_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_type VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    schema_version VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_stream_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_epoch BIGINT UNSIGNED NOT NULL,
    producer_sequence BIGINT UNSIGNED NOT NULL,
    envelope LONGTEXT NOT NULL,
    status VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
    state_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    attempts INT UNSIGNED NOT NULL DEFAULT 0,
    available_at DATETIME(6) NOT NULL,
    locked_until DATETIME(6) NULL,
    lease_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_error_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_error_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_error_message VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    delivered_at DATETIME(6) NULL,
    abandoned_at DATETIME(6) NULL,
    abandoned_by VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    abandon_reason_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    abandon_note VARCHAR(256) NULL,
    PRIMARY KEY (outbox_id),
    UNIQUE KEY uq_bkn_backend_trace_event (event_id),
    UNIQUE KEY uq_bkn_backend_trace_stream_sequence (producer_stream_id, producer_epoch, producer_sequence),
    INDEX idx_bkn_backend_trace_pending (status, available_at, outbox_id),
    INDEX idx_bkn_backend_trace_stream_order (producer_stream_id, producer_epoch, status, producer_sequence, outbox_id),
    INDEX idx_bkn_backend_trace_tenant_status (tenant_id, status, updated_at, outbox_id),
    INDEX idx_bkn_backend_trace_oldest (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_backend_trace_producer_stream_state (
    producer_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_stream_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    current_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0,
    next_sequence BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (producer_id, producer_stream_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_backend_trace_outbox_action_audit (
    action_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    outbox_id BIGINT UNSIGNED NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    tenant_id VARCHAR(128) NOT NULL,
    action_type VARCHAR(16) NOT NULL,
    from_status VARCHAR(20) NOT NULL,
    to_status VARCHAR(20) NOT NULL,
    reason_code VARCHAR(64) NOT NULL,
    reason_note VARCHAR(256) NULL,
    operator_id VARCHAR(128) NOT NULL,
    operator_type VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    expected_state_version BIGINT UNSIGNED NOT NULL,
    result_status VARCHAR(20) NOT NULL,
    result_state_version BIGINT UNSIGNED NOT NULL,
    result_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (action_id),
    UNIQUE KEY uq_bkn_backend_trace_outbox_action_idempotency (idempotency_key),
    INDEX idx_bkn_backend_trace_outbox_action_outbox (outbox_id, action_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
