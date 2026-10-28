-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

-- Per-status ID scans cover eligibility without reading/sorting payload rows.
-- Keep the existing time-oriented pending index for other access paths.
CREATE INDEX IF NOT EXISTS idx_bkn_trace_projection_claim_order
    ON bkn_trace_projection_outbox (status, outbox_id, available_at, locked_until);
