-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

-- Internal derived metadata; neither column changes lifecycle row versions.
ALTER TABLE bkn_trace_interactions
    ADD COLUMN IF NOT EXISTS record_integrity_version BIGINT UNSIGNED NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS record_integrity_json LONGTEXT NULL,
    ADD COLUMN IF NOT EXISTS record_integrity_pending TINYINT AS (IF(execution_status <> 'active' AND record_integrity_json IS NULL, 1, 0)) PERSISTENT;

CREATE INDEX IF NOT EXISTS idx_record_integrity_pending_interaction
    ON bkn_trace_interactions (record_integrity_pending, interaction_id);
