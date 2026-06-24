-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 0.6.0 -> 0.7.0 增量：t_small_model 增加 f_default 列
-- 用于标记某 model_type 下的系统默认小模型(1=默认)，供 get_default/set-default 接口使用
-- ==========================================
USE kweaver;

ALTER TABLE t_small_model
    ADD COLUMN IF NOT EXISTS f_default int default 0 null comment '该 model_type 下的系统默认小模型标记(1=默认)';
