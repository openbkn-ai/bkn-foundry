CREATE TABLE IF NOT EXISTS bkn_trace_operation_call_facts (
    operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempt_no INT UNSIGNED NOT NULL,
    conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    interaction_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    receipt_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    tool_name VARCHAR(255) NOT NULL,
    protocol VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_module VARCHAR(128) NOT NULL,
    parent_operation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    input_payload LONGTEXT NOT NULL,
    output_payload LONGTEXT NULL,
    error_payload LONGTEXT NULL,
    request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
    trace_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    span_id VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (operation_id, attempt_no),
    INDEX idx_bkn_trace_operation_call_fact_interaction (interaction_id, started_at, operation_id, attempt_no),
    INDEX idx_bkn_trace_operation_call_fact_trace (trace_id, started_at, operation_id, attempt_no),
    CONSTRAINT fk_bkn_trace_operation_call_fact_operation
        FOREIGN KEY (operation_id) REFERENCES bkn_trace_operations (operation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE bkn_trace_operations
    DROP COLUMN IF EXISTS normalized_input_hash;

ALTER TABLE bkn_trace_receipts
    DROP COLUMN IF EXISTS normalized_input_hash;

ALTER TABLE bkn_trace_receipts
    DROP COLUMN IF EXISTS payload_hash;
