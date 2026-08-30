DROP INDEX IF EXISTS uq_bkn_trace_conversation_generation ON bkn_trace_conversations;
DROP INDEX IF EXISTS uq_bkn_trace_conversation_current ON bkn_trace_conversations;
DROP INDEX IF EXISTS idx_bkn_trace_conversation_list ON bkn_trace_conversations;

ALTER TABLE bkn_trace_conversations
    DROP COLUMN IF EXISTS business_domain_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_generation
    ON bkn_trace_conversations (
        tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, generation
    );
CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_current
    ON bkn_trace_conversations (
        tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, current_slot
    );
CREATE INDEX IF NOT EXISTS idx_bkn_trace_conversation_list
    ON bkn_trace_conversations (tenant_id, updated_at, conversation_id);

DROP INDEX IF EXISTS uq_bkn_trace_idempotency ON bkn_trace_idempotency_records;
ALTER TABLE bkn_trace_idempotency_records
    DROP COLUMN IF EXISTS business_domain_id;
CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_idempotency
    ON bkn_trace_idempotency_records (
        scope, tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, idempotency_key
    );

ALTER TABLE bkn_trace_receipts
    DROP COLUMN IF EXISTS business_domain_id;

DROP INDEX IF EXISTS idx_bkn_trace_ledger_interaction ON bkn_trace_evidence_event_ledger;
ALTER TABLE bkn_trace_evidence_event_ledger
    DROP COLUMN IF EXISTS business_domain_id;
CREATE INDEX IF NOT EXISTS idx_bkn_trace_ledger_interaction
    ON bkn_trace_evidence_event_ledger (tenant_id, interaction_id, ingest_sequence);

ALTER TABLE bkn_trace_event_conflicts
    DROP COLUMN IF EXISTS business_domain_id;

DROP INDEX IF EXISTS idx_provenance_projection_scope ON bkn_trace_ee_historical_provenance_projections;
ALTER TABLE bkn_trace_ee_historical_provenance_projections
    DROP COLUMN IF EXISTS business_domain_id;
CREATE INDEX IF NOT EXISTS idx_provenance_projection_scope
    ON bkn_trace_ee_historical_provenance_projections (tenant_id, interaction_id);
