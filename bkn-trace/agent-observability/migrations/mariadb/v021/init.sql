-- Historical provenance projections are task-scoped by interaction_id and
-- facts_hash. They must not retain tenant as an invented authorization scope.
DROP INDEX IF EXISTS idx_provenance_projection_scope_tenant ON bkn_trace_ee_historical_provenance_projections;
ALTER TABLE bkn_trace_ee_historical_provenance_projections
    DROP COLUMN IF EXISTS tenant_id;
