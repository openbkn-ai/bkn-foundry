-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 0.8.0 -> 0.9.0 增量：t_knowledge_network 增加建KN时锁定的 embedding 模型与维度
-- 注：DM8 不支持 ADD COLUMN IF NOT EXISTS；本脚本仅在原地升级路径执行(新装只跑 init.sql)，不会重复。
-- ==========================================

SET SCHEMA kweaver;

ALTER TABLE t_knowledge_network ADD f_embedding_model_id VARCHAR(40 CHAR) NOT NULL DEFAULT '';
ALTER TABLE t_knowledge_network ADD f_embedding_dim INT NOT NULL DEFAULT 0;
