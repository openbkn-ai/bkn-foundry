-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Add Eval tables for upgrades from 0.2.1 and environments already at 0.2.2/05.
-- Keep definitions in sync with init.sql. Existing tables and data are preserved.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_eval_suite (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_suite_kn (kn_id),
    description VARCHAR(1000) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_suite_version (
    id VARCHAR(128) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    members_json JSON NOT NULL,
    members_sha256 CHAR(64) NOT NULL,
    published_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_suite_version (kn_id, suite_id, version),
    CONSTRAINT fk_eval_suite_version_suite FOREIGN KEY (suite_id) REFERENCES t_bkn_eval_suite (id),
    tags_json JSON NOT NULL,
    name_snapshot VARCHAR(255) NOT NULL,
    description_snapshot VARCHAR(1000) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    title VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_case_kn (kn_id),
    archived_at DATETIME(6) NULL,
    archived_by VARCHAR(128) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_version (
    id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    review_status VARCHAR(32) NOT NULL,
    source_json JSON NOT NULL,
    input_spec_json JSON NOT NULL,
    execution_spec_json JSON NOT NULL,
    assertions_json JSON NOT NULL,
    expected_ciphertext LONGBLOB NOT NULL,
    expected_nonce VARBINARY(32) NOT NULL,
    expected_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_case_version (case_id, version),
    KEY idx_eval_case_version_kn (kn_id, review_status),
    CONSTRAINT fk_eval_case_version_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id),
    title_snapshot VARCHAR(255) NOT NULL,
    expected_source_json JSON NOT NULL,
    category VARCHAR(32) NOT NULL,
    risk_level VARCHAR(32) NOT NULL,
    diagnostic_spec_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_sha256 CHAR(64) NOT NULL,
    scope_kind VARCHAR(32) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    suite_version INT NOT NULL,
    target_branch VARCHAR(128) NOT NULL,
    data_baseline_id VARCHAR(128) NOT NULL,
    runner_type VARCHAR(64) NOT NULL,
    selection_snapshot_json JSON NOT NULL,
    selection_sha256 CHAR(64) NOT NULL,
    execution_status VARCHAR(32) NOT NULL,
    phase VARCHAR(32) NULL,
    quality_outcome VARCHAR(32) NULL,
    target_snapshot_json JSON NULL,
    target_snapshot_sha256 CHAR(64) NULL,
    planned_cases INT NOT NULL DEFAULT 0,
    completed_cases INT NOT NULL DEFAULT 0,
    planned_attempts INT NOT NULL DEFAULT 0,
    completed_attempts INT NOT NULL DEFAULT 0,
    result_revision INT NULL,
    result_checksum CHAR(64) NULL,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_run_idempotency (kn_id, idempotency_key),
    KEY idx_eval_run_kn_created (kn_id, created_at, id),
    trigger_type VARCHAR(16) NOT NULL DEFAULT 'manual',
    run_plan_id VARCHAR(36) NULL,
    scheduled_at DATETIME(6) NULL,
    KEY idx_eval_run_plan_id (kn_id, run_plan_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_credential (
    run_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    ciphertext LONGBLOB NOT NULL,
    nonce VARBINARY(32) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (run_id),
    KEY idx_eval_run_credential_expiry (expires_at),
    CONSTRAINT fk_eval_run_credential_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_run (
    id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    case_version_id VARCHAR(128) NOT NULL,
    attempt_no INT NOT NULL,
    execution_status VARCHAR(32) NOT NULL,
    phase VARCHAR(32) NULL,
    quality_outcome VARCHAR(32) NULL,
    interaction_id VARCHAR(128) NULL,
    interaction_intent_key VARCHAR(128) NULL,
    client_invocation_id CHAR(64) NULL,
    observation_json JSON NULL,
    observation_sha256 CHAR(64) NULL,
    evidence_refs_json JSON NULL,
    evidence_refs_sha256 CHAR(64) NULL,
    duration_ms BIGINT NULL,
    created_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_case_run_attempt (run_id, case_version_id, attempt_no),
    KEY idx_eval_case_run_page (run_id, created_at, id),
    CONSTRAINT fk_eval_case_run_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_assertion_result (
    id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    assertion_id VARCHAR(128) NOT NULL,
    evaluator_version VARCHAR(64) NOT NULL,
    assertion_type VARCHAR(64) NOT NULL,
    outcome VARCHAR(32) NOT NULL,
    message TEXT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_assertion_once (case_run_id, assertion_id, evaluator_version),
    CONSTRAINT fk_eval_assertion_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_finding (
    id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    category VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    detail_json JSON NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_finding_case_run (case_run_id),
    CONSTRAINT fk_eval_finding_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id),
    first_result_revision INT NOT NULL DEFAULT 1,
    last_result_revision INT NULL,
    assertion_id VARCHAR(128) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_job (
    id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NULL,
    job_type VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_retry_at DATETIME(6) NOT NULL,
    lease_owner VARCHAR(128) NULL,
    lease_until DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_job_once (job_type, idempotency_key),
    KEY idx_eval_job_claim (status, next_retry_at, lease_until, created_at, id),
    CONSTRAINT fk_eval_job_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_draft (
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    title VARCHAR(255) NOT NULL,
    category VARCHAR(32) NOT NULL,
    risk_level VARCHAR(32) NOT NULL,
    source_json JSON NOT NULL,
    input_spec_json JSON NOT NULL,
    execution_spec_json JSON NOT NULL,
    assertions_json JSON NOT NULL,
    expected_source_json JSON NOT NULL,
    expected_ciphertext LONGBLOB NOT NULL,
    expected_nonce VARBINARY(32) NOT NULL,
    expected_sha256 CHAR(64) NOT NULL,
    diagnostic_spec_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (case_id),
    KEY idx_eval_case_draft_kn_status (kn_id, status, updated_at, case_id),
    CONSTRAINT fk_eval_case_draft_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_gold_access_audit (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    draft_revision INT NOT NULL,
    principal_id VARCHAR(128) NOT NULL,
    accessed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_gold_audit_case (kn_id, case_id, accessed_at),
    KEY idx_eval_gold_audit_principal (kn_id, principal_id, accessed_at),
    CONSTRAINT fk_eval_gold_audit_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_config (
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    rules_json JSON NOT NULL,
    rules_sha256 CHAR(64) NOT NULL,
    published_version INT NOT NULL DEFAULT 0,
    published_revision INT NOT NULL DEFAULT 0,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_config_version (
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    rules_json JSON NOT NULL,
    rules_sha256 CHAR(64) NOT NULL,
    published_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_result_revision (
    run_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    result_checksum CHAR(64) NOT NULL,
    quality_outcome VARCHAR(32) NOT NULL,
    target_snapshot_sha256 CHAR(64) NOT NULL,
    members_json JSON NOT NULL,
    members_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (run_id, revision),
    CONSTRAINT fk_eval_result_revision_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_target_scan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    resources_json JSON NOT NULL,
    resources_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_scan_kn (kn_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_generation_plan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    scan_id VARCHAR(36) NOT NULL,
    template_version INT NOT NULL,
    template_sha256 CHAR(64) NOT NULL,
    revision INT NOT NULL,
    selection_json JSON NOT NULL,
    selection_sha256 CHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_plan_kn (kn_id, created_at),
    CONSTRAINT fk_eval_plan_scan FOREIGN KEY (scan_id) REFERENCES t_bkn_eval_target_scan (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_generation_job (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    plan_id VARCHAR(36) NOT NULL,
    status VARCHAR(20) NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_generation_plan_once (plan_id),
    KEY idx_eval_generation_kn (kn_id, created_at),
    CONSTRAINT fk_eval_generation_plan FOREIGN KEY (plan_id) REFERENCES t_bkn_eval_generation_plan (id),
    credential_ciphertext LONGBLOB NULL,
    credential_nonce VARBINARY(32) NULL,
    lease_owner VARCHAR(36) NULL,
    lease_until DATETIME(6) NULL,
    error_code VARCHAR(80) NULL,
    updated_at DATETIME(6) NULL,
    KEY idx_eval_generation_claim (status, lease_until, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_candidate (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    job_id VARCHAR(36) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    rule_revision INT NOT NULL,
    definition_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL,
    expected_ciphertext LONGBLOB NULL,
    expected_nonce VARBINARY(32) NULL,
    expected_sha256 CHAR(64) NULL,
    status VARCHAR(20) NOT NULL,
    case_id VARCHAR(128) NULL,
    reason VARCHAR(255) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_candidate_target_rule (job_id, target_id, rule_id),
    KEY idx_eval_candidate_kn (kn_id, job_id, status),
    CONSTRAINT fk_eval_candidate_job FOREIGN KEY (job_id) REFERENCES t_bkn_eval_generation_job (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_grant (
    kn_id VARCHAR(128) NOT NULL,
    account_id VARCHAR(128) NOT NULL,
    operation VARCHAR(32) NOT NULL,
    granted_by VARCHAR(128) NOT NULL,
    granted_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id, account_id, operation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_origin (
    kn_id VARCHAR(128) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    rule_revision INT NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    KEY idx_eval_template_origin_case (case_id),
    CONSTRAINT fk_eval_template_origin_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id),
    PRIMARY KEY (kn_id, target_id, rule_id, case_id),
    UNIQUE KEY uk_eval_template_origin_case (case_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_state (
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    enabled TINYINT(1) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (case_id),
    KEY idx_eval_case_state_kn (kn_id, enabled),
    CONSTRAINT fk_eval_case_state_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_plan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    name VARCHAR(128) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    version_policy VARCHAR(16) NOT NULL,
    suite_version INT NULL,
    schedule_kind VARCHAR(16) NOT NULL,
    time_of_day CHAR(5) NOT NULL,
    weekday TINYINT NULL,
    day_of_month TINYINT NULL,
    timezone VARCHAR(64) NOT NULL,
    overlap_policy VARCHAR(16) NOT NULL,
    enabled TINYINT(1) NOT NULL,
    next_run_at DATETIME(6) NULL,
    last_triggered_at DATETIME(6) NULL,
    last_run_id VARCHAR(36) NULL,
    last_status VARCHAR(16) NULL,
    lease_owner VARCHAR(64) NULL,
    lease_expires_at DATETIME(6) NULL,
    revision INT NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_run_plan_name (kn_id, name),
    KEY idx_eval_run_plan_due (enabled, next_run_at, lease_expires_at),
    KEY idx_eval_run_plan_kn (kn_id, updated_at),
    CONSTRAINT fk_eval_run_plan_suite FOREIGN KEY (suite_id) REFERENCES t_bkn_eval_suite (id),
    last_error_code VARCHAR(64) NULL,
    last_error_message VARCHAR(512) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_plan_authorization (
    plan_id VARCHAR(36) NOT NULL PRIMARY KEY,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    refresh_ciphertext BLOB NULL,
    refresh_nonce BINARY(12) NULL,
    authorized_at DATETIME(6) NULL,
    pending_ciphertext BLOB NULL,
    pending_nonce BINARY(12) NULL,
    state_sha256 CHAR(64) NULL,
    pending_until DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT fk_eval_plan_authorization FOREIGN KEY (plan_id) REFERENCES t_bkn_eval_run_plan(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_overview_scope (
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    excluded_kinds_json JSON NOT NULL,
    excluded_resources_json JSON NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS t_bkn_eval_root_cause_analysis (
    id CHAR(64) NOT NULL PRIMARY KEY,
    kn_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    result_revision INT NOT NULL,
    context_sha256 CHAR(64) NOT NULL,
    context_json JSON NOT NULL,
    contains_gold BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL,
    result_json JSON NULL,
    error_code VARCHAR(64) NULL,
    agent_run_id VARCHAR(128) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_eval_analysis_history (kn_id, requested_by, run_id, case_run_id, result_revision, created_at, id),
    CONSTRAINT fk_eval_analysis_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
