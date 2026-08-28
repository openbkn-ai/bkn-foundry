-- Operation-audit records are scoped by tenant only.
ALTER TABLE t_vega_operation_audit
    DROP INDEX IF EXISTS idx_vega_audit_domain_time,
    DROP COLUMN IF EXISTS business_domain_id;
