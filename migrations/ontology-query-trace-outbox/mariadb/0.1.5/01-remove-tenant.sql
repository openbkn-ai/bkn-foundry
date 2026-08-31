-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE ontology_query_trace_outbox
    DROP INDEX IF EXISTS idx_ontology_query_trace_tenant_status,
    DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE ontology_query_trace_outbox_action_audit
    DROP COLUMN IF EXISTS tenant_id;
