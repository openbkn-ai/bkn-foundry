-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

SET SCHEMA kweaver;

ALTER TABLE t_catalog ADD COLUMN IF NOT EXISTS f_internal TINYINT NOT NULL DEFAULT 0;

-- 存量系统内部目录补标记（BKN 概念索引、执行工厂 skill 索引）
UPDATE t_catalog SET f_internal = 1 WHERE f_id IN ('adp_bkn_catalog', 'kweaver_execution_factory_catalog');
