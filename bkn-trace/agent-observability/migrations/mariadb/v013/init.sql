CREATE TABLE IF NOT EXISTS bkn_trace_conversations (
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
	application_principal_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
	agent_name VARCHAR(128) NOT NULL DEFAULT '',
    actor_name_snapshot VARCHAR(255) NOT NULL DEFAULT '',
    creation_auth_method VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'unknown',
    effective_subject_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_subject_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    delegation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    external_conversation_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    one_shot BOOLEAN NOT NULL DEFAULT FALSE,
    current_slot TINYINT AS (CASE WHEN status = 'active' THEN 1 ELSE NULL END) PERSISTENT,
    row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    closed_at DATETIME(6) NULL,
    PRIMARY KEY (conversation_id),
    CONSTRAINT uq_bkn_trace_conversation_generation UNIQUE (
        tenant_id, business_domain_id, application_principal_id,
        effective_subject_type, effective_subject_id, delegation_id,
        external_conversation_key, generation
    ),
    CONSTRAINT uq_bkn_trace_conversation_current UNIQUE (
        tenant_id, business_domain_id, application_principal_id,
        effective_subject_type, effective_subject_id, delegation_id,
        external_conversation_key, current_slot
    ),
    INDEX idx_bkn_trace_conversation_list (
        tenant_id, business_domain_id, updated_at, conversation_id
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bkn_trace_conversations
	ADD COLUMN IF NOT EXISTS agent_name VARCHAR(128) NOT NULL DEFAULT '' AFTER application_principal_id;

ALTER TABLE bkn_trace_conversations
	ADD COLUMN IF NOT EXISTS actor_name_snapshot VARCHAR(255) NOT NULL DEFAULT '' AFTER agent_name;

ALTER TABLE bkn_trace_conversations
	ADD COLUMN IF NOT EXISTS creation_auth_method VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'unknown' AFTER actor_name_snapshot;

CREATE TABLE IF NOT EXISTS bkn_trace_idempotency_records (
    idempotency_record_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    scope VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    application_principal_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_subject_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_subject_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    delegation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    external_conversation_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    idempotency_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    resource_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    resource_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (idempotency_record_id),
    CONSTRAINT uq_bkn_trace_idempotency UNIQUE (
        scope, tenant_id, business_domain_id, application_principal_id,
        effective_subject_type, effective_subject_id, delegation_id,
        external_conversation_key, idempotency_key
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_interactions (
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    ordinal_no BIGINT UNSIGNED NOT NULL,
    execution_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    evidence_status VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    active_slot TINYINT AS (CASE WHEN execution_status = 'active' THEN 1 ELSE NULL END) PERSISTENT,
    start_idempotency_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    terminal_idempotency_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    terminal_payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    closure_manifest LONGTEXT NULL,
    assembler_deadline DATETIME(6) NULL,
    lease_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    lease_epoch BIGINT UNSIGNED NOT NULL,
    lease_version BIGINT UNSIGNED NOT NULL,
    lease_expires_at DATETIME(6) NOT NULL,
    row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    terminal_at DATETIME(6) NULL,
    PRIMARY KEY (interaction_id),
    CONSTRAINT fk_bkn_trace_interaction_conversation
        FOREIGN KEY (conversation_id) REFERENCES bkn_trace_conversations (conversation_id),
    CONSTRAINT uq_bkn_trace_interaction_ordinal UNIQUE (conversation_id, ordinal_no),
    CONSTRAINT uq_bkn_trace_interaction_active UNIQUE (conversation_id, active_slot),
    CONSTRAINT uq_bkn_trace_interaction_start_idempotency UNIQUE (conversation_id, start_idempotency_key),
    INDEX idx_bkn_trace_interaction_lease (execution_status, lease_expires_at, lease_version),
    INDEX idx_bkn_trace_interaction_assembly (evidence_status, assembler_deadline, interaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_operations (
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    operation_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tool_name VARCHAR(255) NOT NULL,
    parent_operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    causation_event_ids LONGTEXT NULL,
    attempt_no INT UNSIGNED NOT NULL,
    attempt_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (operation_id),
    CONSTRAINT fk_bkn_trace_operation_interaction
        FOREIGN KEY (interaction_id) REFERENCES bkn_trace_interactions (interaction_id),
    CONSTRAINT uq_bkn_trace_operation_key UNIQUE (interaction_id, operation_key),
    INDEX idx_bkn_trace_operation_conversation (conversation_id, interaction_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_receipts (
    receipt_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    schema_version VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    application_principal_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_subject_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    effective_subject_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    delegation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempt_no INT UNSIGNED NOT NULL,
    operation_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tool_name VARCHAR(255) NOT NULL,
    receipt_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    evidence_durability VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    required_receipt BOOLEAN NOT NULL,
    request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    trace_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    causation_event_ids LONGTEXT NULL,
    observed_evidence_refs LONGTEXT NULL,
    business_refs LONGTEXT NULL,
    artifact_refs LONGTEXT NULL,
    partial_reasons LONGTEXT NULL,
    row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    issued_at DATETIME(6) NOT NULL,
    terminal_at DATETIME(6) NULL,
    PRIMARY KEY (receipt_id),
    CONSTRAINT fk_bkn_trace_receipt_operation
        FOREIGN KEY (operation_id) REFERENCES bkn_trace_operations (operation_id),
    CONSTRAINT uq_bkn_trace_receipt_attempt UNIQUE (operation_id, attempt_no),
    INDEX idx_bkn_trace_receipt_interaction (interaction_id, issued_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_event_ledger (
    ingest_sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    immutable_record_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    schema_version VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_type VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    attempt_no INT UNSIGNED NULL,
    request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    trace_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    span_id VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    producer_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_stream_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    producer_epoch BIGINT UNSIGNED NOT NULL,
    producer_sequence BIGINT UNSIGNED NOT NULL,
    causality_status VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    missing_causation_event_ids LONGTEXT NULL,
    started_at DATETIME(6) NOT NULL,
    observed_at DATETIME(6) NOT NULL,
    emitted_at DATETIME(6) NOT NULL,
    ingested_at DATETIME(6) NOT NULL,
    envelope LONGTEXT NOT NULL,
    PRIMARY KEY (ingest_sequence),
    CONSTRAINT uq_bkn_trace_event_id UNIQUE (event_id),
    CONSTRAINT uq_bkn_trace_event_stream_sequence UNIQUE (
        tenant_id, producer_stream_id, producer_epoch, producer_sequence
    ),
    INDEX idx_bkn_trace_ledger_interaction (tenant_id, business_domain_id, interaction_id, ingest_sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_projection_outbox (
    outbox_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    aggregate_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    aggregate_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    aggregate_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    event_type VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload LONGTEXT NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
    attempts INT UNSIGNED NOT NULL DEFAULT 0,
    available_at DATETIME(6) NOT NULL,
    locked_until DATETIME(6) NULL,
    lease_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    last_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    delivered_at DATETIME(6) NULL,
    PRIMARY KEY (outbox_id),
    CONSTRAINT uq_bkn_trace_projection_event UNIQUE (event_id, event_type),
    INDEX idx_bkn_trace_projection_pending (status, available_at, outbox_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bkn_trace_receipts
    ADD COLUMN IF NOT EXISTS row_version BIGINT UNSIGNED NOT NULL DEFAULT 1
    AFTER partial_reasons;

ALTER TABLE bkn_trace_projection_outbox
    ADD COLUMN IF NOT EXISTS aggregate_version BIGINT UNSIGNED NOT NULL DEFAULT 1
    AFTER aggregate_id;

CREATE TABLE IF NOT EXISTS bkn_trace_assembly_revisions (
    revision_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    revision_no BIGINT UNSIGNED NOT NULL,
    parent_revision_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    completion_manifest_version VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    included_receipt_ids LONGTEXT NOT NULL,
    included_event_ids LONGTEXT NOT NULL,
    artifact_manifest_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    assembly_completeness VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    partial_reasons LONGTEXT NULL,
    trigger_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (revision_id),
    CONSTRAINT uq_bkn_trace_assembly_revision UNIQUE (interaction_id, revision_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_event_conflicts (
    conflict_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    existing_payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    conflicting_payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    envelope LONGTEXT NOT NULL,
    detected_at DATETIME(6) NOT NULL,
    PRIMARY KEY (conflict_id),
    INDEX idx_bkn_trace_event_conflict (event_id, detected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_projection_checkpoints (
    projector_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    index_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    last_outbox_id BIGINT UNSIGNED NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (projector_id, index_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_dlq (
    dlq_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    source_kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    failure_class VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempts INT UNSIGNED NOT NULL,
    last_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload LONGTEXT NOT NULL,
    failed_at DATETIME(6) NOT NULL,
    replayed_at DATETIME(6) NULL,
    replayed_by VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    PRIMARY KEY (dlq_id),
    CONSTRAINT uq_bkn_trace_dlq_source UNIQUE (source_kind, source_id),
    INDEX idx_bkn_trace_dlq_pending (replayed_at, failed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bkn_trace_dlq_replay_audit (
    replay_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    dlq_id BIGINT UNSIGNED NOT NULL,
    source_kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    replayed_by VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    reason VARCHAR(512) NOT NULL,
    repair_version VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    replayed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (replay_id),
    INDEX idx_bkn_trace_dlq_replay (dlq_id, replayed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bkn_trace_evidence_event_ledger
    ADD COLUMN IF NOT EXISTS started_at DATETIME(6) NULL AFTER missing_causation_event_ids;

UPDATE bkn_trace_evidence_event_ledger
SET started_at = observed_at
WHERE started_at IS NULL;

ALTER TABLE bkn_trace_evidence_event_ledger
    MODIFY COLUMN started_at DATETIME(6) NOT NULL;
