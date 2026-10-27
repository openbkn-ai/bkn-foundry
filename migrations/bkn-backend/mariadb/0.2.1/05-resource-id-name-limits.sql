-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Resource IDs and names are widened together with every copied/reference
-- column. Existing values are preserved; only the declared capacity changes.

ALTER TABLE t_object_metric_reference
  DROP FOREIGN KEY IF EXISTS fk_object_metric_reference_target;

ALTER TABLE t_knowledge_network
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络名称';

ALTER TABLE t_object_type
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id';

ALTER TABLE t_object_type_status
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类id',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id';

ALTER TABLE t_relation_type
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '关系类id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '关系类名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  MODIFY COLUMN f_source_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '起点对象类',
  MODIFY COLUMN f_target_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '终点对象类',
  MODIFY COLUMN f_fallback_for VARCHAR(128) DEFAULT NULL COMMENT '替代的主关系id，空为无';

ALTER TABLE t_action_type
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '行动类id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '行动类名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  MODIFY COLUMN f_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类';

ALTER TABLE t_concept_group
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id';

ALTER TABLE t_concept_group_relation
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  MODIFY COLUMN f_group_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组id',
  MODIFY COLUMN f_concept_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念id';

ALTER TABLE t_value_type
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '值类型id',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '值类型名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id';

ALTER TABLE t_action_schedule
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID',
  MODIFY COLUMN f_action_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Action type ID to execute';

ALTER TABLE t_risk_type
  MODIFY COLUMN f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '风险类ID',
  MODIFY COLUMN f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '风险类名称',
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID';

ALTER TABLE t_metric_definition
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  MODIFY COLUMN f_scope_ref VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类或子图ID';

ALTER TABLE t_kn_proxy_account
  MODIFY COLUMN f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL;

ALTER TABLE t_kn_proxy_published_grant_source
  MODIFY COLUMN f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL;

ALTER TABLE t_kn_proxy_planned_grant_source
  MODIFY COLUMN f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL;

ALTER TABLE t_kn_proxy_sync_outbox
  MODIFY COLUMN f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL;

ALTER TABLE t_kn_capability_binding
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id';

ALTER TABLE t_object_metric_definition
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  MODIFY COLUMN f_owner_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '主归属对象类ID';

ALTER TABLE t_object_metric_reference
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  MODIFY COLUMN f_source_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '来源指标或对象类ID',
  ADD CONSTRAINT fk_object_metric_reference_target
    FOREIGN KEY (f_kn_id, f_branch, f_target_metric_id, f_target_version)
    REFERENCES t_object_metric_definition (f_kn_id, f_branch, f_id, f_version)
    ON UPDATE RESTRICT ON DELETE RESTRICT;
