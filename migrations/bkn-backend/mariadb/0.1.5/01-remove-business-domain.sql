-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Knowledge networks are no longer partitioned by business domain.
ALTER TABLE t_knowledge_network
  DROP COLUMN IF EXISTS f_business_domain;

-- Operation audit scope is resource authorization only.
ALTER TABLE t_operation_audit
  DROP INDEX IF EXISTS idx_bkn_audit_tenant_time,
  DROP INDEX IF EXISTS idx_bkn_audit_domain_network_time,
  DROP COLUMN IF EXISTS business_domain_id,
  DROP COLUMN IF EXISTS tenant_id,
  ADD INDEX IF NOT EXISTS idx_bkn_audit_network_time (knowledge_network_id, event_time, event_id);
