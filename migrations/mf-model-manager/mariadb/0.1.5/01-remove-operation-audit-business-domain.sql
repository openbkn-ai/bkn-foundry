-- Operation-audit records are scoped by tenant only.
ALTER TABLE t_model_manager_operation_audit
  DROP INDEX IF EXISTS idx_model_audit_scope_time,
  DROP COLUMN IF EXISTS business_domain_id,
  ADD INDEX IF NOT EXISTS idx_model_audit_tenant_time (tenant_id, event_time);
