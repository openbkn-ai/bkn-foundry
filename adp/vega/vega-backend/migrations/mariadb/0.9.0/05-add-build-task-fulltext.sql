-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- 迁移脚本：t_build_task 增加全文索引字段配置
-- ==========================================

USE kweaver;

ALTER TABLE t_build_task ADD COLUMN IF NOT EXISTS f_fulltext_fields VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE t_build_task ADD COLUMN IF NOT EXISTS f_fulltext_analyzer VARCHAR(40) NOT NULL DEFAULT '';
