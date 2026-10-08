-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

ALTER TABLE t_resource ADD COLUMN IF NOT EXISTS f_last_discover_time BIGINT NOT NULL DEFAULT 0 COMMENT 'Last successful discovery time, Unix milliseconds';
-- Legacy discovery time is approximate. Exact count time remains unknown until collected.
UPDATE t_resource SET f_last_discover_time = f_update_time WHERE f_last_discover_time = 0;
