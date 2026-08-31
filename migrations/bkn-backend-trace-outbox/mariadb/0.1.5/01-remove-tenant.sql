-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE bkn_backend_trace_outbox
    DROP INDEX IF EXISTS idx_bkn_backend_trace_tenant_status,
    DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE bkn_backend_trace_outbox_action_audit
    DROP COLUMN IF EXISTS tenant_id;
