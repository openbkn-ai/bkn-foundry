-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- BKN spec 3.0 value types and aliases (openbkn-ai/bkn-foundry#2281).
-- Existing rows keep NULL aliases, which means no aliases; nothing is backfilled.
-- The new columns are appended last so MariaDB can add them in place.

USE openbkn;

ALTER TABLE t_object_type
  ADD COLUMN IF NOT EXISTS f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组';

ALTER TABLE t_action_type
  ADD COLUMN IF NOT EXISTS f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组';

CREATE TABLE IF NOT EXISTS t_value_type (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '值类型id',
  f_name VARCHAR(40) NOT NULL DEFAULT '' COMMENT '值类型名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_base_type VARCHAR(40) NOT NULL DEFAULT '' COMMENT '基础类型',
  f_definition MEDIUMTEXT DEFAULT NULL COMMENT '取值定义，JSON：enum、range、length、pattern、unit',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id,f_branch,f_id),
  UNIQUE KEY uk_value_type_name (f_kn_id,f_branch,f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '值类型';
