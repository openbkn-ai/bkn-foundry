-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE t_semantic_understanding_task
    ADD COLUMN IF NOT EXISTS f_start_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始执行时间',
    ADD COLUMN IF NOT EXISTS f_finish_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '完成时间',
    DROP COLUMN IF EXISTS f_applied_time,
    DROP COLUMN IF EXISTS f_update_time,
    ADD INDEX IF NOT EXISTS idx_create_time (f_create_time),
    ADD INDEX IF NOT EXISTS idx_start_time (f_start_time),
    ADD INDEX IF NOT EXISTS idx_finish_time (f_finish_time);

ALTER TABLE t_discover_task
    ADD COLUMN f_last_progress_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次对外可观测进度更新时间' AFTER f_finish_time,
    ADD INDEX IF NOT EXISTS idx_create_time (f_create_time),
    ADD INDEX IF NOT EXISTS idx_start_time (f_start_time),
    ADD INDEX IF NOT EXISTS idx_finish_time (f_finish_time),
    ADD INDEX IF NOT EXISTS idx_last_progress_time (f_last_progress_time);
