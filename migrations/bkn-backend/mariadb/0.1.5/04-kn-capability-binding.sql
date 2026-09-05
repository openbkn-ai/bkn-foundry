-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Knowledge-network capability binding. The same DDL also lives in 0.1.5/init.sql:
-- data-migrator runs init.sql on a fresh install only and skips it on upgrades, so a
-- table added to just one of the two files is missing on half of the deployments.
USE openbkn;

-- 知识网络能力绑定：登记 Skill / Function 对知识网络的归属，主数据留在执行工厂。
CREATE TABLE IF NOT EXISTS t_kn_capability_binding (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '绑定id',
  f_kn_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_capability_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '能力类型：skill / function',
  f_owner_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '所属容器id：function 时为 box_id，skill 时为空',
  f_capability_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '执行工厂侧标识：skill_id 或 tool_id',
  f_bound_as_box TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否由整箱挂载展开而来',
  f_comment VARCHAR(255) NOT NULL DEFAULT '' COMMENT '备注',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_kn_capability (f_kn_id, f_branch, f_capability_type, f_owner_id, f_capability_id),
  KEY idx_capability (f_capability_type, f_owner_id, f_capability_id),
  KEY idx_kn_branch (f_kn_id, f_branch)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络能力绑定';
