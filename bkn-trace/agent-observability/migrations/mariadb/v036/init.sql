-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

-- Cover conversation existence probes without fetching large call payload rows.
-- These non-unique indexes do not change identity, ownership, or filtering.
CREATE INDEX IF NOT EXISTS idx_bkn_trace_receipt_conversation_summary
    ON bkn_trace_receipts (conversation_id, trace_id, request_id);

CREATE INDEX IF NOT EXISTS idx_bkn_trace_call_fact_conversation_summary
    ON bkn_trace_operation_call_facts (conversation_id, interaction_id);
