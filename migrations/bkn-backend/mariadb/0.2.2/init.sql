-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;


-- 业务知识网络
CREATE TABLE IF NOT EXISTS t_knowledge_network (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_skill_content MEDIUMTEXT NOT NULL COMMENT 'SkillContent',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_id,f_branch),
  UNIQUE KEY uk_kn_name (f_name,f_branch)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '业务知识网络';


-- 对象类
CREATE TABLE IF NOT EXISTS t_object_type (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_data_source VARCHAR(255) NOT NULL COMMENT '数据来源，当前只有视图',
  f_data_properties LONGTEXT DEFAULT NULL COMMENT '数据属性',
  f_logic_properties MEDIUMTEXT DEFAULT NULL COMMENT '逻辑属性',
  f_primary_keys VARCHAR(8192) DEFAULT NULL COMMENT '对象类主键',
  f_display_key VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象实例的显示属性',
  f_incremental_key VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象类增量键',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组',
  f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by',
  PRIMARY KEY (f_kn_id,f_branch,f_id),
  UNIQUE KEY uk_object_type_name (f_kn_id,f_branch,f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象类';


-- 对象类状态
CREATE TABLE IF NOT EXISTS t_object_type_status (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类id',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_incremental_key VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象类增量键',
  f_incremental_value VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象类当前增量值',
  f_index VARCHAR(255) NOT NULL DEFAULT '' COMMENT '索引名称',
  f_index_available BOOLEAN NOT NULL DEFAULT 0 COMMENT '索引是否可用',
  f_doc_count BIGINT(20) NOT NULL DEFAULT 0 COMMENT '文档数量',
  f_storage_size BIGINT(20) NOT NULL DEFAULT 0 COMMENT '存储大小',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id,f_branch,f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象类状态';


-- 关系类
CREATE TABLE IF NOT EXISTS t_relation_type (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '关系类id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '关系类名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_source_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '起点对象类',
  f_target_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '终点对象类',
  f_type VARCHAR(40) NOT NULL DEFAULT '' COMMENT '关联类型',
  f_mapping_rules TEXT DEFAULT NULL COMMENT '关联规则',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  f_reverse_name VARCHAR(40) DEFAULT NULL COMMENT '反向名称',
  f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组',
  f_cardinality VARCHAR(20) DEFAULT NULL COMMENT '基数，空为未声明',
  f_constraints TEXT DEFAULT NULL COMMENT '关系约束，JSON：required、characteristics',
  f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by',
  f_importance VARCHAR(20) DEFAULT NULL COMMENT '主次，空即 primary',
  f_fallback_for VARCHAR(128) DEFAULT NULL COMMENT '替代的主关系id，空为无',
  f_verification VARCHAR(20) DEFAULT NULL COMMENT '可信度，空为未声明',
  PRIMARY KEY (f_kn_id,f_branch,f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '关系类';


-- 行动类
CREATE TABLE IF NOT EXISTS t_action_type (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '行动类id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '行动类名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_action_type VARCHAR(40) NOT NULL DEFAULT '' COMMENT '行动类型',
  f_action_intent VARCHAR(40) NOT NULL DEFAULT '' COMMENT '行动意图',
  f_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类',
  f_condition TEXT DEFAULT NULL COMMENT '行动条件',
  f_affect TEXT DEFAULT NULL COMMENT '行动影响',
  f_impact_contracts TEXT DEFAULT NULL COMMENT '行动影响契约',
  f_action_source VARCHAR(255) NOT NULL COMMENT '行动资源',
  f_parameters TEXT DEFAULT NULL COMMENT '行动参数',
  f_schedule VARCHAR(255) DEFAULT NULL COMMENT '行动监听',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  f_aliases TEXT DEFAULT NULL COMMENT '别名，JSON 字符串数组',
  f_lifecycle_status VARCHAR(20) DEFAULT NULL COMMENT '生命周期状态，空即 active',
  f_deprecation TEXT DEFAULT NULL COMMENT '弃用说明，JSON：reason、replaced_by',
  PRIMARY KEY (f_kn_id,f_branch,f_id),
  UNIQUE KEY uk_action_type_name (f_kn_id,f_branch,f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '行动类';


-- 概念分组
CREATE TABLE IF NOT EXISTS t_concept_group (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id,f_branch,f_id),
  UNIQUE KEY uk_concept_group_name (f_kn_id,f_branch,f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '概念分组';


-- 分组与概念对应表
CREATE TABLE IF NOT EXISTS t_concept_group_relation (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '主键id',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_group_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念分组id',
  f_concept_type VARCHAR(40) NOT NULL DEFAULT '' COMMENT '概念类型',
  f_concept_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '概念id',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_concept_group_relation (f_kn_id,f_branch,f_group_id,f_concept_type,f_concept_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '分组与概念对应表';


-- 值类型
CREATE TABLE IF NOT EXISTS t_value_type (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '值类型id',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '值类型名称',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_comment TEXT NOT NULL COMMENT '备注',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
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


-- Action Schedule Management
-- Supports cron-based scheduled action execution with distributed locking
CREATE TABLE IF NOT EXISTS t_action_schedule (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Schedule ID',
  f_name VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'Schedule name',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Branch',
  f_action_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Action type ID to execute',
  f_cron_expression VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'Standard 5-field cron expression (min hour dom mon dow)',
  f_instance_identities MEDIUMTEXT DEFAULT NULL COMMENT 'JSON array of target object instance identities',
  f_dynamic_params MEDIUMTEXT DEFAULT NULL COMMENT 'JSON object of dynamic parameters',
  f_status VARCHAR(20) NOT NULL DEFAULT 'inactive' COMMENT 'Schedule status: active or inactive',
  f_last_run_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Last execution timestamp (ms)',
  f_next_run_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Next scheduled run timestamp (ms)',
  f_lock_holder VARCHAR(64) DEFAULT NULL COMMENT 'Pod ID holding execution lock (NULL = unlocked)',
  f_lock_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Lock acquisition timestamp (ms) for timeout detection',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Creator ID',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Creator type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create timestamp (ms)',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Updater ID',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Updater type',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update timestamp (ms)',
  f_execution_subject VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Current execution subject ID',
  f_execution_subject_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Current execution subject type',
  PRIMARY KEY (f_id),
  KEY idx_kn_branch (f_kn_id, f_branch),
  KEY idx_status_next_run (f_status, f_next_run_time),
  KEY idx_action_type (f_action_type_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'Action schedule for cron-based execution';


-- Risk Type
CREATE TABLE IF NOT EXISTS t_risk_type (
  f_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '风险类ID',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '风险类名称',
  f_comment TEXT NOT NULL COMMENT '描述',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_branch, f_id),
  UNIQUE KEY uk_risk_type_name (f_kn_id, f_branch, f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '风险类';

CREATE TABLE IF NOT EXISTS t_metric_definition (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '指标ID',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '指标技术名',
  f_comment TEXT NOT NULL COMMENT '描述',
  f_tags VARCHAR(255) DEFAULT NULL COMMENT '标签',
  f_icon VARCHAR(255) NOT NULL DEFAULT '' COMMENT '图标',
  f_color VARCHAR(40) NOT NULL DEFAULT '' COMMENT '颜色',
  f_bkn_raw_content MEDIUMTEXT NOT NULL COMMENT 'BKNRawContent',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_unit_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '单位类型',
  f_unit VARCHAR(64) NOT NULL DEFAULT '' COMMENT '单位',
  f_metric_type VARCHAR(32) NOT NULL DEFAULT 'atomic' COMMENT '指标类型 atomic|derived|composite',
  f_scope_type VARCHAR(32) NOT NULL DEFAULT 'object_type' COMMENT '统计主体类型',
  f_scope_ref VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类或子图ID',
  f_time_dimension LONGTEXT DEFAULT NULL COMMENT '时间维度 JSON',
  f_calculation_formula LONGTEXT NOT NULL COMMENT '计算公式 JSON',
  f_analysis_dimensions LONGTEXT DEFAULT NULL COMMENT '分析维度 JSON',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_branch, f_id),
  UNIQUE KEY uk_metric_name (f_kn_id, f_branch, f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN 指标定义';

-- BKN Backend source-owned management audit facts.
-- This table stores bounded facts only; request/response bodies and credentials are excluded.
CREATE TABLE IF NOT EXISTS t_operation_audit (
  event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  event_time DATETIME(6) NOT NULL,
  recorded_at DATETIME(6) NOT NULL,
  knowledge_network_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_name VARCHAR(255) NOT NULL,
  actor_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  auth_method VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  credential_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_channel VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  method VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  action VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_id VARCHAR(1024) NOT NULL,
  target_name VARCHAR(1024) NOT NULL,
  outcome VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  failure_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  failure_message VARCHAR(512) NOT NULL DEFAULT '',
  change_summary TEXT NOT NULL,
  schema_version VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  PRIMARY KEY (event_id),
  INDEX idx_bkn_audit_network_time (knowledge_network_id, event_time, event_id),
  INDEX idx_bkn_audit_actor_time (actor_id, event_time, event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN Backend management operation audit facts';

-- Environment-local one-to-one mapping and publication synchronization state.
CREATE TABLE IF NOT EXISTS t_kn_proxy_account (
  f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'app',
  f_lifecycle_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  f_version BIGINT NOT NULL DEFAULT 1,
  f_sync_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  f_published_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_synced_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_pending_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_sync_generation BIGINT NOT NULL DEFAULT 0,
  f_published_generation BIGINT NOT NULL DEFAULT 0,
  f_last_sync_error VARCHAR(1024) NOT NULL DEFAULT '',
  f_last_grantor_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lock_owner VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lock_until BIGINT NOT NULL DEFAULT 0,
  f_last_sync_started_at BIGINT NOT NULL DEFAULT 0,
  f_last_sync_succeeded_at BIGINT NOT NULL DEFAULT 0,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_kn_id),
  UNIQUE KEY uk_kn_proxy_account_proxy (f_proxy_account_id),
  INDEX idx_kn_proxy_sync (f_sync_status, f_updated_at),
  INDEX idx_kn_proxy_lifecycle (f_lifecycle_status, f_updated_at),
  INDEX idx_kn_proxy_lock (f_lock_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Knowledge network managed proxy mapping';

CREATE TABLE IF NOT EXISTS t_kn_proxy_published_grant_source (
  f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_kn_id, f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation),
  INDEX idx_kn_proxy_published_grant_source_kn (f_kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Published managed proxy grant snapshot';

CREATE TABLE IF NOT EXISTS t_kn_proxy_planned_grant_source (
  f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_kn_id, f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation),
  INDEX idx_kn_proxy_planned_grant_source_kn (f_kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Planned managed proxy grant snapshot';

CREATE TABLE IF NOT EXISTS t_kn_proxy_sync_outbox (
  f_id VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_kn_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_generation BIGINT NOT NULL,
  f_base_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_target_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  f_attempt_count INT NOT NULL DEFAULT 0,
  f_next_retry_at BIGINT NOT NULL DEFAULT 0,
  f_lease_owner VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lease_until BIGINT NOT NULL DEFAULT 0,
  f_last_error VARCHAR(1024) NOT NULL DEFAULT '',
  f_payload LONGTEXT NOT NULL,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  f_completed_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_kn_proxy_outbox_generation (f_kn_id, f_generation),
  INDEX idx_kn_proxy_outbox_ready (f_status, f_next_retry_at, f_created_at),
  INDEX idx_kn_proxy_outbox_cleanup (f_status, f_updated_at, f_id),
  INDEX idx_kn_proxy_outbox_proxy_status (f_proxy_account_id, f_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Durable managed proxy grant publication queue';

-- 知识网络能力绑定：登记 Skill / Function 对知识网络的归属，主数据留在执行工厂。
CREATE TABLE IF NOT EXISTS t_kn_capability_binding (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '绑定id',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络id',
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


CREATE TABLE IF NOT EXISTS t_object_metric_definition (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象指标ID',
  f_version INT NOT NULL DEFAULT 1 COMMENT '不可变版本号',
  f_code VARCHAR(128) NOT NULL DEFAULT '' COMMENT '稳定指标编码',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '指标名称',
  f_description TEXT NOT NULL COMMENT '业务定义',
  f_owner_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '主归属对象类ID',
  f_metric_type VARCHAR(32) NOT NULL COMMENT 'atomic|derived|composite',
  f_status VARCHAR(32) NOT NULL DEFAULT 'draft' COMMENT 'draft|validated|published|deprecated',
  f_definition LONGTEXT NOT NULL COMMENT '对象指标V1定义JSON',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者ID',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者ID',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_branch, f_id, f_version),
  UNIQUE KEY uk_object_metric_code_version (f_kn_id, f_branch, f_code, f_version),
  INDEX idx_object_metric_owner (f_kn_id, f_branch, f_owner_object_type_id, f_update_time),
  INDEX idx_object_metric_status (f_kn_id, f_branch, f_status, f_update_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象指标版本定义';


CREATE TABLE IF NOT EXISTS t_object_metric_reference (
  f_id BIGINT NOT NULL AUTO_INCREMENT COMMENT '引用记录ID',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_source_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '来源指标ID',
  f_source_version INT NOT NULL DEFAULT 1 COMMENT '来源指标版本',
  f_source_field VARCHAR(128) NOT NULL DEFAULT '' COMMENT '引用字段路径',
  f_target_metric_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '目标指标ID',
  f_target_version INT NOT NULL DEFAULT 1 COMMENT '目标指标版本',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_object_metric_reference (
    f_kn_id, f_branch, f_source_id, f_source_version,
    f_source_field, f_target_metric_id, f_target_version
  ),
  INDEX idx_object_metric_reference_target (
    f_kn_id, f_branch, f_target_metric_id, f_target_version
  ),
  INDEX idx_object_metric_reference_source (
    f_kn_id, f_branch, f_source_id, f_source_version
  ),
  CONSTRAINT fk_object_metric_reference_target
    FOREIGN KEY (f_kn_id, f_branch, f_target_metric_id, f_target_version)
    REFERENCES t_object_metric_definition (f_kn_id, f_branch, f_id, f_version)
    ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象指标版本引用索引';


-- 知识网络事件类型（识别配置）
CREATE TABLE IF NOT EXISTS t_kn_event_type (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_code VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件类型名称',
  f_source VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'rule|report',
  f_subject VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件涉及的对象类ID',
  f_severity VARCHAR(16) NOT NULL DEFAULT '' COMMENT '默认严重程度',
  f_merge_hours DOUBLE NOT NULL DEFAULT 0 COMMENT '登记事件的合并窗口（小时）',
  f_rule LONGTEXT NOT NULL COMMENT '规则定义JSON',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者ID',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者ID',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_code)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件类型';


-- 知识网络事件账本
CREATE TABLE IF NOT EXISTS t_kn_event (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '事件ID',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_title VARCHAR(256) NOT NULL DEFAULT '' COMMENT '标题',
  f_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'unverified|verified|rejected|duplicate|closed',
  f_severity VARCHAR(16) NOT NULL DEFAULT '' COMMENT '严重程度',
  f_source VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'rule|report',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_object_type VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类ID',
  f_object_key VARCHAR(250) NOT NULL DEFAULT '' COMMENT '对象键',
  f_object_name VARCHAR(256) NOT NULL DEFAULT '' COMMENT '对象显示名',
  f_object_attributes LONGTEXT NOT NULL COMMENT '写入方提供的对象属性值JSON',
  f_occurred_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '发生时间',
  f_recorded_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '记录时间',
  f_ended_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '结束时间',
  f_first_detected_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '首次发现时间',
  f_last_checked_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近检测时间',
  f_check_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'ok|unknown',
  f_detail LONGTEXT NOT NULL COMMENT '详情JSON',
  f_backlog TINYINT(1) NOT NULL DEFAULT 0 COMMENT '规则首次检查发现的存量',
  f_merged_into VARCHAR(40) NOT NULL DEFAULT '' COMMENT '合并到的事件ID',
  f_reported_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '登记人ID',
  f_reported_by_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '登记人显示名',
  f_verified_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '核实人ID',
  f_verified_by_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '核实人类型',
  f_verified_by_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '核实人显示名',
  f_verified_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '核实时间',
  f_revision INT NOT NULL DEFAULT 1 COMMENT '修订号',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_id),
  INDEX idx_kn_event_recorded (f_kn_id, f_recorded_at, f_id),
  INDEX idx_kn_event_type_object (f_kn_id, f_event_type, f_object_key)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件';


-- 知识网络事件变更记录，按网络连续编号，兼作变更流
CREATE TABLE IF NOT EXISTS t_kn_event_change (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_seq BIGINT(20) NOT NULL COMMENT '网络内变更序号',
  f_event_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '事件ID',
  f_action VARCHAR(32) NOT NULL DEFAULT '' COMMENT '变更动作',
  f_from_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT '变更前状态',
  f_to_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT '变更后状态',
  f_actor VARCHAR(40) NOT NULL DEFAULT '' COMMENT '操作者ID',
  f_actor_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '操作者类型',
  f_actor_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '操作者显示名',
  f_note VARCHAR(512) NOT NULL DEFAULT '' COMMENT '原因或说明',
  f_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '变更时间',
  PRIMARY KEY (f_kn_id, f_seq),
  INDEX idx_kn_event_change_event (f_event_id, f_seq),
  INDEX idx_kn_event_change_action_time (f_action, f_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件变更记录';


-- 知识网络事件规则检查（各事件类型最近一次）
CREATE TABLE IF NOT EXISTS t_kn_event_rule_check (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'ok|unknown',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_hits INT NOT NULL DEFAULT 0 COMMENT '命中对象数',
  f_message VARCHAR(1024) NOT NULL DEFAULT '' COMMENT '无法判断的原因',
  f_last_ok BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次成功判断的时间',
  f_checked_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '检查人ID',
  f_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '检查时间',
  PRIMARY KEY (f_kn_id, f_event_type)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件规则检查';


-- 每个知识网络一行，记最新变更序号；写入事件时锁住它，保证变更按提交顺序编号
CREATE TABLE IF NOT EXISTS t_kn_event_head (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_last_seq BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最新变更序号',
  PRIMARY KEY (f_kn_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件序号';


-- Copyright openbkn.ai
-- Eval schema: licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
-- Initialized by the community data-migrator; Eval execution remains an EE capability.

CREATE TABLE IF NOT EXISTS t_bkn_eval_suite (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_suite_kn (kn_id),
    description VARCHAR(1000) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_suite_version (
    id VARCHAR(128) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    members_json JSON NOT NULL,
    members_sha256 CHAR(64) NOT NULL,
    published_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_suite_version (kn_id, suite_id, version),
    CONSTRAINT fk_eval_suite_version_suite FOREIGN KEY (suite_id) REFERENCES t_bkn_eval_suite (id),
    tags_json JSON NOT NULL,
    name_snapshot VARCHAR(255) NOT NULL,
    description_snapshot VARCHAR(1000) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    title VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_case_kn (kn_id),
    archived_at DATETIME(6) NULL,
    archived_by VARCHAR(128) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_version (
    id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    review_status VARCHAR(32) NOT NULL,
    source_json JSON NOT NULL,
    input_spec_json JSON NOT NULL,
    execution_spec_json JSON NOT NULL,
    assertions_json JSON NOT NULL,
    expected_ciphertext LONGBLOB NOT NULL,
    expected_nonce VARBINARY(32) NOT NULL,
    expected_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_case_version (case_id, version),
    KEY idx_eval_case_version_kn (kn_id, review_status),
    CONSTRAINT fk_eval_case_version_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id),
    title_snapshot VARCHAR(255) NOT NULL,
    expected_source_json JSON NOT NULL,
    category VARCHAR(32) NOT NULL,
    risk_level VARCHAR(32) NOT NULL,
    diagnostic_spec_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_sha256 CHAR(64) NOT NULL,
    scope_kind VARCHAR(32) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    suite_version INT NOT NULL,
    target_branch VARCHAR(128) NOT NULL,
    data_baseline_id VARCHAR(128) NOT NULL,
    runner_type VARCHAR(64) NOT NULL,
    selection_snapshot_json JSON NOT NULL,
    selection_sha256 CHAR(64) NOT NULL,
    execution_status VARCHAR(32) NOT NULL,
    phase VARCHAR(32) NULL,
    quality_outcome VARCHAR(32) NULL,
    target_snapshot_json JSON NULL,
    target_snapshot_sha256 CHAR(64) NULL,
    planned_cases INT NOT NULL DEFAULT 0,
    completed_cases INT NOT NULL DEFAULT 0,
    planned_attempts INT NOT NULL DEFAULT 0,
    completed_attempts INT NOT NULL DEFAULT 0,
    result_revision INT NULL,
    result_checksum CHAR(64) NULL,
    started_at DATETIME(6) NULL,
    finished_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_run_idempotency (kn_id, idempotency_key),
    KEY idx_eval_run_kn_created (kn_id, created_at, id),
    trigger_type VARCHAR(16) NOT NULL DEFAULT 'manual',
    run_plan_id VARCHAR(36) NULL,
    scheduled_at DATETIME(6) NULL,
    KEY idx_eval_run_plan_id (kn_id, run_plan_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_credential (
    run_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    ciphertext LONGBLOB NOT NULL,
    nonce VARBINARY(32) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (run_id),
    KEY idx_eval_run_credential_expiry (expires_at),
    CONSTRAINT fk_eval_run_credential_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_run (
    id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    case_version_id VARCHAR(128) NOT NULL,
    attempt_no INT NOT NULL,
    execution_status VARCHAR(32) NOT NULL,
    phase VARCHAR(32) NULL,
    quality_outcome VARCHAR(32) NULL,
    interaction_id VARCHAR(128) NULL,
    interaction_intent_key VARCHAR(128) NULL,
    client_invocation_id CHAR(64) NULL,
    observation_json JSON NULL,
    observation_sha256 CHAR(64) NULL,
    evidence_refs_json JSON NULL,
    evidence_refs_sha256 CHAR(64) NULL,
    duration_ms BIGINT NULL,
    created_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_case_run_attempt (run_id, case_version_id, attempt_no),
    KEY idx_eval_case_run_page (run_id, created_at, id),
    CONSTRAINT fk_eval_case_run_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_assertion_result (
    id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    assertion_id VARCHAR(128) NOT NULL,
    evaluator_version VARCHAR(64) NOT NULL,
    assertion_type VARCHAR(64) NOT NULL,
    outcome VARCHAR(32) NOT NULL,
    message TEXT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_assertion_once (case_run_id, assertion_id, evaluator_version),
    CONSTRAINT fk_eval_assertion_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_finding (
    id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    category VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    detail_json JSON NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_finding_case_run (case_run_id),
    CONSTRAINT fk_eval_finding_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id),
    first_result_revision INT NOT NULL DEFAULT 1,
    last_result_revision INT NULL,
    assertion_id VARCHAR(128) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_job (
    id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NULL,
    job_type VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_retry_at DATETIME(6) NOT NULL,
    lease_owner VARCHAR(128) NULL,
    lease_until DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_job_once (job_type, idempotency_key),
    KEY idx_eval_job_claim (status, next_retry_at, lease_until, created_at, id),
    CONSTRAINT fk_eval_job_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_draft (
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    title VARCHAR(255) NOT NULL,
    category VARCHAR(32) NOT NULL,
    risk_level VARCHAR(32) NOT NULL,
    source_json JSON NOT NULL,
    input_spec_json JSON NOT NULL,
    execution_spec_json JSON NOT NULL,
    assertions_json JSON NOT NULL,
    expected_source_json JSON NOT NULL,
    expected_ciphertext LONGBLOB NOT NULL,
    expected_nonce VARBINARY(32) NOT NULL,
    expected_sha256 CHAR(64) NOT NULL,
    diagnostic_spec_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (case_id),
    KEY idx_eval_case_draft_kn_status (kn_id, status, updated_at, case_id),
    CONSTRAINT fk_eval_case_draft_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_gold_access_audit (
    id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    draft_revision INT NOT NULL,
    principal_id VARCHAR(128) NOT NULL,
    accessed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_gold_audit_case (kn_id, case_id, accessed_at),
    KEY idx_eval_gold_audit_principal (kn_id, principal_id, accessed_at),
    CONSTRAINT fk_eval_gold_audit_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_config (
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    rules_json JSON NOT NULL,
    rules_sha256 CHAR(64) NOT NULL,
    published_version INT NOT NULL DEFAULT 0,
    published_revision INT NOT NULL DEFAULT 0,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_config_version (
    kn_id VARCHAR(128) NOT NULL,
    version INT NOT NULL,
    rules_json JSON NOT NULL,
    rules_sha256 CHAR(64) NOT NULL,
    published_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_result_revision (
    run_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    result_checksum CHAR(64) NOT NULL,
    quality_outcome VARCHAR(32) NOT NULL,
    target_snapshot_sha256 CHAR(64) NOT NULL,
    members_json JSON NOT NULL,
    members_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (run_id, revision),
    CONSTRAINT fk_eval_result_revision_run FOREIGN KEY (run_id) REFERENCES t_bkn_eval_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_target_scan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    resources_json JSON NOT NULL,
    resources_sha256 CHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_scan_kn (kn_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_generation_plan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    scan_id VARCHAR(36) NOT NULL,
    template_version INT NOT NULL,
    template_sha256 CHAR(64) NOT NULL,
    revision INT NOT NULL,
    selection_json JSON NOT NULL,
    selection_sha256 CHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_eval_plan_kn (kn_id, created_at),
    CONSTRAINT fk_eval_plan_scan FOREIGN KEY (scan_id) REFERENCES t_bkn_eval_target_scan (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_generation_job (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    plan_id VARCHAR(36) NOT NULL,
    status VARCHAR(20) NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_generation_plan_once (plan_id),
    KEY idx_eval_generation_kn (kn_id, created_at),
    CONSTRAINT fk_eval_generation_plan FOREIGN KEY (plan_id) REFERENCES t_bkn_eval_generation_plan (id),
    credential_ciphertext LONGBLOB NULL,
    credential_nonce VARBINARY(32) NULL,
    lease_owner VARCHAR(36) NULL,
    lease_until DATETIME(6) NULL,
    error_code VARCHAR(80) NULL,
    updated_at DATETIME(6) NULL,
    KEY idx_eval_generation_claim (status, lease_until, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_candidate (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    job_id VARCHAR(36) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    rule_revision INT NOT NULL,
    definition_json JSON NOT NULL,
    definition_sha256 CHAR(64) NOT NULL,
    expected_ciphertext LONGBLOB NULL,
    expected_nonce VARBINARY(32) NULL,
    expected_sha256 CHAR(64) NULL,
    status VARCHAR(20) NOT NULL,
    case_id VARCHAR(128) NULL,
    reason VARCHAR(255) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_candidate_target_rule (job_id, target_id, rule_id),
    KEY idx_eval_candidate_kn (kn_id, job_id, status),
    CONSTRAINT fk_eval_candidate_job FOREIGN KEY (job_id) REFERENCES t_bkn_eval_generation_job (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_grant (
    kn_id VARCHAR(128) NOT NULL,
    account_id VARCHAR(128) NOT NULL,
    operation VARCHAR(32) NOT NULL,
    granted_by VARCHAR(128) NOT NULL,
    granted_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id, account_id, operation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_template_origin (
    kn_id VARCHAR(128) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    rule_revision INT NOT NULL,
    case_id VARCHAR(128) NOT NULL,
    KEY idx_eval_template_origin_case (case_id),
    CONSTRAINT fk_eval_template_origin_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id),
    PRIMARY KEY (kn_id, target_id, rule_id, case_id),
    UNIQUE KEY uk_eval_template_origin_case (case_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_case_state (
    case_id VARCHAR(128) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    enabled TINYINT(1) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (case_id),
    KEY idx_eval_case_state_kn (kn_id, enabled),
    CONSTRAINT fk_eval_case_state_case FOREIGN KEY (case_id) REFERENCES t_bkn_eval_case (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_plan (
    id VARCHAR(36) NOT NULL,
    kn_id VARCHAR(128) NOT NULL,
    name VARCHAR(128) NOT NULL,
    suite_id VARCHAR(128) NOT NULL,
    version_policy VARCHAR(16) NOT NULL,
    suite_version INT NULL,
    schedule_kind VARCHAR(16) NOT NULL,
    time_of_day CHAR(5) NOT NULL,
    weekday TINYINT NULL,
    day_of_month TINYINT NULL,
    timezone VARCHAR(64) NOT NULL,
    overlap_policy VARCHAR(16) NOT NULL,
    enabled TINYINT(1) NOT NULL,
    next_run_at DATETIME(6) NULL,
    last_triggered_at DATETIME(6) NULL,
    last_run_id VARCHAR(36) NULL,
    last_status VARCHAR(16) NULL,
    lease_owner VARCHAR(64) NULL,
    lease_expires_at DATETIME(6) NULL,
    revision INT NOT NULL,
    created_by VARCHAR(128) NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_eval_run_plan_name (kn_id, name),
    KEY idx_eval_run_plan_due (enabled, next_run_at, lease_expires_at),
    KEY idx_eval_run_plan_kn (kn_id, updated_at),
    CONSTRAINT fk_eval_run_plan_suite FOREIGN KEY (suite_id) REFERENCES t_bkn_eval_suite (id),
    last_error_code VARCHAR(64) NULL,
    last_error_message VARCHAR(512) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_run_plan_authorization (
    plan_id VARCHAR(36) NOT NULL PRIMARY KEY,
    kn_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    refresh_ciphertext BLOB NULL,
    refresh_nonce BINARY(12) NULL,
    authorized_at DATETIME(6) NULL,
    pending_ciphertext BLOB NULL,
    pending_nonce BINARY(12) NULL,
    state_sha256 CHAR(64) NULL,
    pending_until DATETIME(6) NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT fk_eval_plan_authorization FOREIGN KEY (plan_id) REFERENCES t_bkn_eval_run_plan(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_overview_scope (
    kn_id VARCHAR(128) NOT NULL,
    revision INT NOT NULL,
    excluded_kinds_json JSON NOT NULL,
    excluded_resources_json JSON NOT NULL,
    updated_by VARCHAR(128) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS t_bkn_eval_root_cause_analysis (
    id CHAR(64) NOT NULL PRIMARY KEY,
    kn_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    case_run_id VARCHAR(128) NOT NULL,
    requested_by VARCHAR(128) NOT NULL,
    result_revision INT NOT NULL,
    context_sha256 CHAR(64) NOT NULL,
    context_json JSON NOT NULL,
    contains_gold BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL,
    result_json JSON NULL,
    error_code VARCHAR(64) NULL,
    agent_run_id VARCHAR(128) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    INDEX idx_eval_analysis_history (kn_id, requested_by, run_id, case_run_id, result_revision, created_at, id),
    CONSTRAINT fk_eval_analysis_case_run FOREIGN KEY (case_run_id) REFERENCES t_bkn_eval_case_run (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
