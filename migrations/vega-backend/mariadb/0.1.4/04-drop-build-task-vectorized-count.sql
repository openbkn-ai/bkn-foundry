-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;


ALTER TABLE t_build_task
    DROP COLUMN IF EXISTS f_vectorized_count;

ALTER TABLE t_build_task
    MODIFY COLUMN f_synced_mark TEXT NOT NULL COMMENT '同步游标(JSON key/value array)';
