-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

SET SCHEMA openbkn;

CREATE TABLE ontology_query_trace_outbox (
    outbox_id BIGINT IDENTITY(1,1) NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    schema_version VARCHAR(16) NOT NULL,
    tenant_id VARCHAR(128) NOT NULL,
    producer_id VARCHAR(128) NOT NULL,
    producer_stream_id VARCHAR(128) NOT NULL,
    producer_epoch BIGINT NOT NULL,
    producer_sequence BIGINT NOT NULL,
    envelope CLOB NOT NULL,
    status VARCHAR(20) DEFAULT 'pending' NOT NULL,
    state_version BIGINT DEFAULT 1 NOT NULL,
    attempts INTEGER DEFAULT 0 NOT NULL,
    available_at TIMESTAMP(6) NOT NULL,
    locked_until TIMESTAMP(6),
    lease_token VARCHAR(64),
    last_error_code VARCHAR(128),
    last_error_fingerprint CHAR(64),
    last_error_message VARCHAR(256),
    created_at TIMESTAMP(6) NOT NULL,
    updated_at TIMESTAMP(6) NOT NULL,
    delivered_at TIMESTAMP(6),
    abandoned_at TIMESTAMP(6),
    abandoned_by VARCHAR(128),
    abandon_reason_code VARCHAR(64),
    abandon_note VARCHAR(256),
    CONSTRAINT pk_ontology_query_trace_outbox PRIMARY KEY (outbox_id),
    CONSTRAINT uq_ontology_query_trace_event UNIQUE (event_id),
    CONSTRAINT uq_ontology_query_trace_stream_sequence UNIQUE (producer_stream_id, producer_epoch, producer_sequence)
);
CREATE INDEX idx_ontology_query_trace_pending ON ontology_query_trace_outbox (status, available_at, outbox_id);
CREATE INDEX idx_ontology_query_trace_stream_order ON ontology_query_trace_outbox (producer_stream_id, producer_epoch, status, producer_sequence, outbox_id);
CREATE INDEX idx_ontology_query_trace_tenant_status ON ontology_query_trace_outbox (tenant_id, status, updated_at, outbox_id);
CREATE INDEX idx_ontology_query_trace_oldest ON ontology_query_trace_outbox (status, created_at);

CREATE TABLE ontology_query_trace_producer_stream_state (
    producer_id VARCHAR(128) NOT NULL,
    producer_stream_id VARCHAR(128) NOT NULL,
    current_epoch BIGINT DEFAULT 0 NOT NULL,
    next_sequence BIGINT DEFAULT 1 NOT NULL,
    created_at TIMESTAMP(6) NOT NULL,
    updated_at TIMESTAMP(6) NOT NULL,
    CONSTRAINT pk_ontology_query_trace_stream_state PRIMARY KEY (producer_id, producer_stream_id)
);

CREATE TABLE ontology_query_trace_outbox_action_audit (
    action_id BIGINT IDENTITY(1,1) NOT NULL,
    outbox_id BIGINT NOT NULL,
    event_id VARCHAR(128) NOT NULL,
    tenant_id VARCHAR(128) NOT NULL,
    action_type VARCHAR(16) NOT NULL,
    from_status VARCHAR(20) NOT NULL,
    to_status VARCHAR(20) NOT NULL,
    reason_code VARCHAR(64) NOT NULL,
    reason_note VARCHAR(256),
    operator_id VARCHAR(128) NOT NULL,
    operator_type VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    expected_state_version BIGINT NOT NULL,
    result_status VARCHAR(20) NOT NULL,
    result_state_version BIGINT NOT NULL,
    result_at TIMESTAMP(6) NOT NULL,
    created_at TIMESTAMP(6) NOT NULL,
    CONSTRAINT pk_ontology_query_trace_outbox_action_audit PRIMARY KEY (action_id),
    CONSTRAINT uq_ontology_query_trace_outbox_action_idempotency UNIQUE (idempotency_key)
);
CREATE INDEX idx_ontology_query_trace_outbox_action_outbox ON ontology_query_trace_outbox_action_audit (outbox_id, action_id);
