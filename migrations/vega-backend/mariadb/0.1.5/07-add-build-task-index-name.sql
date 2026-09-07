-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE t_build_task
    ADD COLUMN f_index_name VARCHAR(100) NOT NULL DEFAULT '' COMMENT '任务目标索引名'
    AFTER f_execute_type;

UPDATE t_build_task
SET f_status = 'failed',
    f_error_msg = 'upgrade requires creating a new build task'
WHERE f_index_name = ''
  AND f_status IN ('pending', 'running', 'stopping', 'stopped');
