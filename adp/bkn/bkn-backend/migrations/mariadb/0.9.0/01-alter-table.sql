-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 0.8.0 -> 0.9.0 增量：t_knowledge_network 增加建KN时锁定的 embedding 模型与维度
-- 写入与 KNN 查询全程读回该模型，消除"改全局默认模型后老 KN 概念向量/查询向量模型不一致"
-- ==========================================
USE kweaver;

ALTER TABLE t_knowledge_network
  ADD COLUMN IF NOT EXISTS f_embedding_model_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '建KN时锁定的embedding模型id';
ALTER TABLE t_knowledge_network
  ADD COLUMN IF NOT EXISTS f_embedding_dim INT NOT NULL DEFAULT 0 COMMENT '建KN时锁定的向量维度';
