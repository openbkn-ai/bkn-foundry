-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE t_resource
    ADD COLUMN IF NOT EXISTS f_local_status VARCHAR(20) NOT NULL DEFAULT 'unavailable'
        COMMENT 'Local index status: unavailable, available, stale'
        AFTER f_logic_definition,
    ADD COLUMN IF NOT EXISTS f_sync_mark TEXT NULL
        COMMENT 'Committed batch SyncCheckpoint V1 owned by the Resource'
        AFTER f_local_index_name;
