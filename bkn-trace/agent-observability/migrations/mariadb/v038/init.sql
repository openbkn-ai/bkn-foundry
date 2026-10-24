-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

ALTER TABLE bkn_trace_archive_jobs
    ADD COLUMN IF NOT EXISTS batch_progress LONGTEXT NULL;
