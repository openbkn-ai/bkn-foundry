-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

-- Nonunique: retries and concurrent receipts remain independent facts.
CREATE INDEX IF NOT EXISTS idx_bkn_trace_receipt_trace_root
    ON bkn_trace_receipts (trace_id, issued_at, receipt_id);
