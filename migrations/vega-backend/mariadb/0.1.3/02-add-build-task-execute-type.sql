-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- 空值保留给 streaming 任务；历史 batch 任务在升级时显式补齐为 full，
-- 仅处理尚未设置执行类型的记录，绝不覆盖已持久化的 incremental 值。
ALTER TABLE t_build_task
    ADD COLUMN IF NOT EXISTS f_execute_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'batch 执行类型: full, incremental；空表示 streaming 或历史记录'
    AFTER f_mode;

UPDATE t_build_task
SET f_execute_type = 'full'
WHERE f_mode = 'batch'
  AND f_execute_type = '';
