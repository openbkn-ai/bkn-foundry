-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- BKN spec 3.0 relation declarations and lifecycle (openbkn-ai/bkn-foundry#2284).
-- Existing rows keep NULL in every new column, which means nothing is declared:
-- lifecycle active, importance primary, no fallback, cardinality and
-- verification undeclared. Nothing is backfilled.
-- The new columns are appended last so MariaDB can add them in place.
-- Relation properties live in f_mapping_rules (indirect mapping JSON) and need
-- no column.

USE openbkn;

ALTER TABLE t_object_type
  ADD COLUMN IF NOT EXISTS f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  ADD COLUMN IF NOT EXISTS f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by';

ALTER TABLE t_action_type
  ADD COLUMN IF NOT EXISTS f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  ADD COLUMN IF NOT EXISTS f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by';

ALTER TABLE t_relation_type
  ADD COLUMN IF NOT EXISTS f_reverse_name VARCHAR(40) DEFAULT NULL COMMENT '反向名称',
  ADD COLUMN IF NOT EXISTS f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组',
  ADD COLUMN IF NOT EXISTS f_cardinality VARCHAR(20) DEFAULT NULL COMMENT '基数，空为未声明',
  ADD COLUMN IF NOT EXISTS f_constraints TEXT DEFAULT NULL COMMENT '关系约束，JSON：required、characteristics',
  ADD COLUMN IF NOT EXISTS f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  ADD COLUMN IF NOT EXISTS f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by',
  ADD COLUMN IF NOT EXISTS f_importance VARCHAR(20) DEFAULT NULL COMMENT '主次，空即 primary',
  ADD COLUMN IF NOT EXISTS f_fallback_for VARCHAR(40) DEFAULT NULL COMMENT '替代的主关系id，空为无',
  ADD COLUMN IF NOT EXISTS f_verification VARCHAR(20) DEFAULT NULL COMMENT '可信度，空为未声明';
