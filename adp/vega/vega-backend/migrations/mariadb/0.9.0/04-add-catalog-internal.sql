-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE kweaver;

ALTER TABLE t_catalog ADD COLUMN IF NOT EXISTS f_internal BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否系统内部目录：内部目录在权限服务按 internal_catalog 类型注册，业务角色的 catalog:* 通配授权匹配不到，仅超级管理员可见';

-- 存量系统内部目录补标记（BKN 概念索引、执行工厂 skill 索引）
UPDATE t_catalog SET f_internal = TRUE WHERE f_id IN ('adp_bkn_catalog', 'kweaver_execution_factory_catalog');

-- 注：标记后所有鉴权改按 internal_catalog/internal_resource 类型，bkn-safe 中按旧类型注册的
-- 创建者实例策略成为无害残留（不授予任何业务角色权限）。如需清理，在 bkn-safe 库执行：
--   DELETE FROM casbin_rule WHERE ptype='p' AND v1 IN (
--     'catalog:adp_bkn_catalog', 'catalog:kweaver_execution_factory_catalog',
--     'resource:adp_bkn_concept_dataset', 'resource:kweaver_execution_factory_skill_dataset');
