-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 迁移脚本：t_build_task 增加部分向量化失败明细字段
-- 与 f_error_msg 区分：f_error_msg 记录整任务硬失败，
-- f_failure_detail 记录 completed 但部分文档向量化失败的明细。
-- ==========================================

USE kweaver;

ALTER TABLE t_build_task ADD COLUMN IF NOT EXISTS f_failure_detail TEXT NOT NULL DEFAULT '' COMMENT '构建完成但部分文档向量化失败的明细';
