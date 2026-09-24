-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- C6 read-side Evidence Kafka admission history and durable transport decisions.
-- Policy/instance/closure writes remain owned by the frozen C1 control plane.
CREATE TABLE IF NOT EXISTS bkn_trace_capture_policy_revisions (
    revision BIGINT UNSIGNED NOT NULL,
    admission_enabled BOOLEAN NOT NULL,
    recorded_at DATETIME(6) NOT NULL,
    PRIMARY KEY (revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_producer_instance_registrations (
    producer_instance_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    policy_revision BIGINT UNSIGNED NOT NULL,
    process_boot_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    registration_state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    registered_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6) NULL,
    PRIMARY KEY (producer_instance_id, policy_revision),
    KEY idx_bkn_trace_instance_policy (policy_revision, registration_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_producer_closure_watermarks (
    producer_instance_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    policy_revision BIGINT UNSIGNED NOT NULL,
    last_accepted_sequence BIGINT UNSIGNED NOT NULL,
    closed_at DATETIME(6) NOT NULL,
    acknowledged_at DATETIME(6) NOT NULL,
    PRIMARY KEY (producer_instance_id, policy_revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_ingest_rejections (
    topic VARCHAR(249) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    partition_id INT NOT NULL,
    offset_id BIGINT NOT NULL,
    broker_log_append_at DATETIME(6) NOT NULL,
    record_key_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    record_value_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    record_value_bytes INT UNSIGNED NOT NULL,
    header_presence_bitmap BIGINT UNSIGNED NOT NULL,
    event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    producer_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    producer_stream_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    migration_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    reason_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    first_detected_at DATETIME(6) NOT NULL,
    last_detected_at DATETIME(6) NOT NULL,
    delivery_count BIGINT UNSIGNED NOT NULL DEFAULT 1,
    PRIMARY KEY (topic, partition_id, offset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bkn_trace_event_conflicts
    ADD COLUMN IF NOT EXISTS topic VARCHAR(249) CHARACTER SET ascii COLLATE ascii_bin NULL,
    ADD COLUMN IF NOT EXISTS partition_id INT NULL,
    ADD COLUMN IF NOT EXISTS offset_id BIGINT NULL,
    ADD COLUMN IF NOT EXISTS reason_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    ADD COLUMN IF NOT EXISTS incoming_immutable_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_event_conflict_kafka_coordinate
    ON bkn_trace_event_conflicts (topic, partition_id, offset_id);
