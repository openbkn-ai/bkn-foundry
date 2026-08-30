-- Collapse the business-domain scope out of the Core schema.
--
-- Statement order matters. Every replacement index is created BEFORE the index
-- it replaces is dropped, so a database whose rows were unique only by
-- business_domain_id fails on the CREATE UNIQUE INDEX while the old index still
-- guards the table: the migration aborts with a duplicate-key error naming the
-- new index, nothing has been destroyed, and the operator can deduplicate and
-- re-run. Dropping first would commit the DDL, leave the table without any
-- uniqueness guard, and fail identically on every retry.
--
-- The replacement indexes therefore need distinct names; the tenant-scoped
-- names below are the ones the schema keeps.

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_generation_tenant
    ON bkn_trace_conversations (
        tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, generation
    );
CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_conversation_current_tenant
    ON bkn_trace_conversations (
        tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, current_slot
    );
CREATE INDEX IF NOT EXISTS idx_bkn_trace_conversation_list_tenant
    ON bkn_trace_conversations (tenant_id, updated_at, conversation_id);

DROP INDEX IF EXISTS uq_bkn_trace_conversation_generation ON bkn_trace_conversations;
DROP INDEX IF EXISTS uq_bkn_trace_conversation_current ON bkn_trace_conversations;
DROP INDEX IF EXISTS idx_bkn_trace_conversation_list ON bkn_trace_conversations;

ALTER TABLE bkn_trace_conversations
    DROP COLUMN IF EXISTS business_domain_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bkn_trace_idempotency_tenant
    ON bkn_trace_idempotency_records (
        scope, tenant_id, application_principal_id, effective_subject_type,
        effective_subject_id, delegation_id, external_conversation_key, idempotency_key
    );
DROP INDEX IF EXISTS uq_bkn_trace_idempotency ON bkn_trace_idempotency_records;
ALTER TABLE bkn_trace_idempotency_records
    DROP COLUMN IF EXISTS business_domain_id;

ALTER TABLE bkn_trace_receipts
    DROP COLUMN IF EXISTS business_domain_id;

CREATE INDEX IF NOT EXISTS idx_bkn_trace_ledger_interaction_tenant
    ON bkn_trace_evidence_event_ledger (tenant_id, interaction_id, ingest_sequence);
DROP INDEX IF EXISTS idx_bkn_trace_ledger_interaction ON bkn_trace_evidence_event_ledger;
ALTER TABLE bkn_trace_evidence_event_ledger
    DROP COLUMN IF EXISTS business_domain_id;

ALTER TABLE bkn_trace_event_conflicts
    DROP COLUMN IF EXISTS business_domain_id;

CREATE INDEX IF NOT EXISTS idx_provenance_projection_scope_tenant
    ON bkn_trace_ee_historical_provenance_projections (tenant_id, interaction_id);
DROP INDEX IF EXISTS idx_provenance_projection_scope ON bkn_trace_ee_historical_provenance_projections;
ALTER TABLE bkn_trace_ee_historical_provenance_projections
    DROP COLUMN IF EXISTS business_domain_id;
