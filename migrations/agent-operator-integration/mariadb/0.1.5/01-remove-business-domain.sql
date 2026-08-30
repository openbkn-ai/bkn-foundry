-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Operation audits are scoped by tenant and authorization only.
ALTER TABLE t_execution_factory_operation_audit
  DROP INDEX IF EXISTS idx_execution_audit_scope_time,
  DROP COLUMN IF EXISTS business_domain_id,
  ADD INDEX IF NOT EXISTS idx_execution_audit_tenant_time (tenant_id, event_time, event_id);
