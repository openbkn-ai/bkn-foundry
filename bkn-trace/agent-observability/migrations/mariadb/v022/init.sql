-- Collapse the tenant scope out of the Core schema without weakening the
-- uniqueness guards before collision detection. Replacement indexes are
-- created first, so rows that only differ by tenant make the migration fail
-- before any tenant-scoped index or column is removed.

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_generation
    ON bkn_trace_conversations (
        application_principal_id, effective_subject_type, effective_subject_id,
        delegation_id, external_conversation_key, generation
    );
CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_current
    ON bkn_trace_conversations (
        application_principal_id, effective_subject_type, effective_subject_id,
        delegation_id, external_conversation_key, current_slot
    );
CREATE INDEX IF NOT EXISTS idx_bkn_trace_conversation_list
    ON bkn_trace_conversations (
        application_principal_id, effective_subject_type, effective_subject_id,
        updated_at, conversation_id
    );

DROP INDEX IF EXISTS uq_bkn_trace_conversation_generation_tenant ON bkn_trace_conversations;
DROP INDEX IF EXISTS uq_bkn_trace_conversation_current_tenant ON bkn_trace_conversations;
DROP INDEX IF EXISTS idx_bkn_trace_conversation_list_tenant ON bkn_trace_conversations;
ALTER TABLE bkn_trace_conversations DROP COLUMN IF EXISTS tenant_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_idempotency
    ON bkn_trace_idempotency_records (
        scope, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key,
        idempotency_key
    );
DROP INDEX IF EXISTS uq_bkn_trace_idempotency_tenant ON bkn_trace_idempotency_records;
ALTER TABLE bkn_trace_idempotency_records DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE bkn_trace_receipts DROP COLUMN IF EXISTS tenant_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_event_stream_sequence_global
    ON bkn_trace_evidence_event_ledger (
        producer_stream_id, producer_epoch, producer_sequence
    );
CREATE INDEX IF NOT EXISTS idx_bkn_trace_ledger_interaction
    ON bkn_trace_evidence_event_ledger (interaction_id, ingest_sequence);
DROP INDEX IF EXISTS uq_bkn_trace_event_stream_sequence ON bkn_trace_evidence_event_ledger;
DROP INDEX IF EXISTS idx_bkn_trace_ledger_interaction_tenant ON bkn_trace_evidence_event_ledger;
ALTER TABLE bkn_trace_evidence_event_ledger DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE bkn_trace_event_conflicts DROP COLUMN IF EXISTS tenant_id;

CREATE INDEX IF NOT EXISTS idx_bkn_trace_archive_job_kind
    ON bkn_trace_archive_jobs (archive_kind, created_at);
DROP INDEX IF EXISTS idx_bkn_trace_archive_job_tenant_kind ON bkn_trace_archive_jobs;
ALTER TABLE bkn_trace_archive_jobs DROP COLUMN IF EXISTS tenant_id;
