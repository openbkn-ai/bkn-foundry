-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 0.6.0 -> 0.7.0 增量：t_small_model 增加 f_default 列
-- 用于标记某 model_type 下的系统默认小模型(1=默认)，供 get_default/set-default 接口使用
-- 注：DM8 不支持 ADD COLUMN IF NOT EXISTS；本脚本仅在原地升级路径执行(新装只跑 init.sql)，不会重复。
-- ==========================================

SET SCHEMA kweaver;

ALTER TABLE t_small_model ADD "f_default" int DEFAULT 0;
