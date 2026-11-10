-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Knowledge network versioning (#1229). Every statement can run again safely.

-- 知识网络版本状态：每个开启版本管理的网络一行，没有这一行表示未开启
CREATE TABLE IF NOT EXISTS t_kn_version_state (
  f_kn_id VARCHAR(128) NOT NULL COMMENT '业务知识网络id',
  f_mode VARCHAR(20) NOT NULL DEFAULT 'enabled' COMMENT 'enabled / disabling',
  f_running_version INT NOT NULL DEFAULT 0 COMMENT '运行版本号，0 表示未发布',
  f_previous_version INT NOT NULL DEFAULT 0 COMMENT '上一版，0 表示没有',
  f_next_version INT NOT NULL DEFAULT 1 COMMENT '下一个版本号',
  f_draft_base_version INT NOT NULL DEFAULT 0 COMMENT '草稿的上游（来源版本号）',
  f_draft_based_on_running INT NOT NULL DEFAULT 0 COMMENT '草稿确定上游时的运行版本号',
  f_draft_modified TINYINT(1) NOT NULL DEFAULT 0 COMMENT '草稿自来源之后是否被修改',
  f_draft_revision BIGINT NOT NULL DEFAULT 0 COMMENT '草稿修订号，任何草稿写入加一',
  f_draft_index_build VARCHAR(40) NOT NULL DEFAULT '' COMMENT '草稿引用的概念索引构建，空表示未构建',
  f_draft_index_revision BIGINT NOT NULL DEFAULT -1 COMMENT '草稿引用的构建对应的草稿修订号',
  f_enabled_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '开启者id',
  f_enabled_by_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '开启者类型',
  f_enable_time BIGINT NOT NULL DEFAULT 0 COMMENT '开启时间',
  f_update_time BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络版本状态';

-- 知识网络版本：内容列写入后不变，状态列与 f_index_build 可变
CREATE TABLE IF NOT EXISTS t_kn_version (
  f_kn_id VARCHAR(128) NOT NULL COMMENT '业务知识网络id',
  f_version INT NOT NULL COMMENT '版本号',
  f_label VARCHAR(64) NOT NULL DEFAULT '' COMMENT '版本标签，如 v1.2.0',
  f_status VARCHAR(20) NOT NULL COMMENT 'running / history / deprecated / purged',
  f_origin VARCHAR(20) NOT NULL COMMENT '产生方式：enable / publish',
  f_base_version INT NOT NULL DEFAULT 0 COMMENT '发布时草稿的来源版本号',
  f_draft_revision BIGINT NOT NULL DEFAULT 0 COMMENT '发布时的草稿修订号',
  f_index_build VARCHAR(40) NOT NULL DEFAULT '' COMMENT '引用的概念索引构建',
  f_notes TEXT NOT NULL COMMENT '发布说明',
  f_source TEXT NOT NULL COMMENT '来源，JSON：{"type","project_id","ref"}',
  f_publisher VARCHAR(40) NOT NULL DEFAULT '' COMMENT '发布人id',
  f_publisher_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '发布人类型',
  f_publish_time BIGINT NOT NULL DEFAULT 0 COMMENT '发布时间',
  f_status_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '最近一次状态变更人id',
  f_status_by_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '最近一次状态变更人类型',
  f_status_time BIGINT NOT NULL DEFAULT 0 COMMENT '最近一次状态变更时间',
  PRIMARY KEY (f_kn_id, f_version),
  KEY idx_kn_version_status (f_kn_id, f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络版本';

-- 知识网络版本事件：只追加，记录谁在何时对版本做了什么
CREATE TABLE IF NOT EXISTS t_kn_version_event (
  f_id VARCHAR(40) NOT NULL COMMENT '事件id',
  f_kn_id VARCHAR(128) NOT NULL COMMENT '业务知识网络id',
  f_type VARCHAR(20) NOT NULL COMMENT 'enable / publish / switch / copy / discard / deprecate / restore / purge / disable',
  f_from_version INT NOT NULL DEFAULT 0 COMMENT '操作前运行版本或复制来源',
  f_to_version INT NOT NULL DEFAULT 0 COMMENT '操作后运行版本或目标版本',
  f_reason VARCHAR(20) NOT NULL DEFAULT '' COMMENT '触发原因，社区版发布时的自动清理为 retention',
  f_notes TEXT NOT NULL COMMENT '说明',
  f_operator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '操作人id',
  f_operator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '操作人类型',
  f_create_time BIGINT NOT NULL DEFAULT 0 COMMENT '时间',
  f_idempotency_key VARCHAR(64) DEFAULT NULL COMMENT '幂等键，取自请求头 Idempotency-Key，未带时为 NULL',
  PRIMARY KEY (f_id),
  KEY idx_kn_version_event (f_kn_id, f_create_time),
  UNIQUE KEY uk_kn_version_event_idem (f_kn_id, f_idempotency_key)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络版本事件';

-- 概念索引构建：就绪后不再改变，版本与草稿引用构建
CREATE TABLE IF NOT EXISTS t_kn_index_build (
  f_build_id VARCHAR(40) NOT NULL COMMENT '构建id',
  f_kn_id VARCHAR(128) NOT NULL COMMENT '业务知识网络id',
  f_index_key VARCHAR(64) NOT NULL COMMENT '索引键，写入概念索引记录的 branch 字段',
  f_def_branch VARCHAR(40) NOT NULL COMMENT '构建所依据的定义分支',
  f_draft_revision BIGINT NOT NULL DEFAULT -1 COMMENT '依据草稿构建时的草稿修订号，其他为 -1',
  f_source_build VARCHAR(40) NOT NULL DEFAULT '' COMMENT '复用向量的来源构建',
  f_status VARCHAR(20) NOT NULL COMMENT 'building / ready / failed / deleting',
  f_lease_owner VARCHAR(64) NOT NULL DEFAULT '' COMMENT '构建者实例',
  f_lease_until BIGINT NOT NULL DEFAULT 0 COMMENT '租约到期时间',
  f_unreferenced_at BIGINT NOT NULL DEFAULT 0 COMMENT '清理任务首次发现无引用的时间，0 表示有引用',
  f_create_time BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_update_time BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_build_id),
  KEY idx_kn_index_build (f_kn_id, f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '概念索引构建';

-- 对象指标版本号分配：按网络分配，复制或切换版本不会复用版本号
CREATE TABLE IF NOT EXISTS t_object_metric_version_seq (
  f_kn_id VARCHAR(128) NOT NULL COMMENT '业务知识网络id',
  f_metric_id VARCHAR(40) NOT NULL COMMENT '对象指标id',
  f_last_version INT NOT NULL DEFAULT 0 COMMENT '已分配的最大版本号',
  PRIMARY KEY (f_kn_id, f_metric_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象指标版本号分配';

INSERT INTO t_object_metric_version_seq (f_kn_id, f_metric_id, f_last_version)
SELECT f_kn_id, f_id, MAX(f_version) FROM t_object_metric_definition GROUP BY f_kn_id, f_id
ON DUPLICATE KEY UPDATE f_last_version = GREATEST(f_last_version, VALUES(f_last_version));

-- 定义修订标识，作为 If-Match 比较的值。存量行与旧代码插入的空值在此补值。
ALTER TABLE t_object_type
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_object_type SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_relation_type
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_relation_type SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_action_type
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_action_type SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_concept_group
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_concept_group SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_value_type
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_value_type SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_risk_type
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_risk_type SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_object_metric_definition
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_object_metric_definition SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

ALTER TABLE t_kn_capability_binding
  ADD COLUMN IF NOT EXISTS f_revision VARCHAR(40) NOT NULL DEFAULT '' COMMENT '定义修订标识';
UPDATE t_kn_capability_binding SET f_revision = REPLACE(UUID(), '-', '') WHERE f_revision = '';

-- 能力绑定按 (网络, 分支, id) 唯一，复制到其他分支时保留原 id。存量 f_id 全局唯一，改主键不会冲突；
-- 再次执行只是重建同一个主键。
ALTER TABLE t_kn_capability_binding DROP PRIMARY KEY, ADD PRIMARY KEY (f_kn_id, f_branch, f_id);

-- 审计记录修改作用于哪个版本：draft、main 或版本号。列名沿用该表不带 f_ 前缀的命名。
ALTER TABLE t_operation_audit
  ADD COLUMN IF NOT EXISTS kn_version VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '';
