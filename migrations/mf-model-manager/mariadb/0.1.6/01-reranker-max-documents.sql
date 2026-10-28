-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

USE openbkn;

ALTER TABLE t_small_model
    ADD COLUMN IF NOT EXISTS f_max_documents INT NULL
    COMMENT 'Maximum documents per rerank request; NULL means no local limit';
