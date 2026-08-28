-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- ==========================================
-- VEGA Catalog / Resource 初始化表结构定义
-- ==========================================

-- ==========================================
-- Schema 定义说明（f_schema_definition JSON 数组）
-- ==========================================
-- 每个字段使用 Property 模型：name、display_name、type、description、
-- original_name、original_type、original_description、features 和 attributes。
-- features 中的元素使用 PropertyFeature 模型；attributes 由各 connector 定义。
-- 具体 JSON 契约以 interfaces/resource.go 和 VEGA OpenAPI 为准。
-- ==========================================
USE openbkn;
-- ==========================================
-- 1. t_catalog 主表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_catalog (
    -- 主键与基础信息
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'catalog唯一标识',
    f_name                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '目录名称，系统一级命名空间',
    f_tags                    VARCHAR(255) NOT NULL DEFAULT '[]' COMMENT '标签，JSON 数组格式，用于分类和检索',
    f_description             VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '目录描述',

    f_type                    VARCHAR(20) NOT NULL DEFAULT '' COMMENT '目录类型: physical, logical',
    f_enabled                 BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否启用',
    f_internal                BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否系统内部目录：内部目录在权限服务按 internal_catalog 类型注册，业务角色的 catalog:* 通配授权匹配不到，仅超级管理员可见',

    -- Physical Catalog 专属字段
    f_connector_type          VARCHAR(50) NOT NULL DEFAULT '' COMMENT '数据源类型: mysql, postgresql, s3, kafka, elasticsearch, api, prometheus, etc.',
    f_connector_config        MEDIUMTEXT NOT NULL COMMENT '加密存储的连接配置（JSON格式）',
    f_metadata                MEDIUMTEXT NOT NULL COMMENT '自动发现的元数据（JSON格式），如数据库版本、schema快照等',

    -- 状态管理
    f_health_check_status     VARCHAR(20) NOT NULL DEFAULT 'unchecked' COMMENT '连接状态: unchecked, healthy, degraded, unhealthy, offline',
    f_last_check_time         BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最后健康检查时间',
    f_health_check_result     TEXT NOT NULL COMMENT '健康检查结果',

    -- 审计字段
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
    f_updater                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
    f_updater_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
    f_update_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',

    -- 索引
    PRIMARY KEY (f_id),
    UNIQUE INDEX uk_name (f_name),
    INDEX idx_type (f_type),
    INDEX idx_connector_type (f_connector_type),
    INDEX idx_health_check_status (f_health_check_status)
)  ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='目录表，管理数据源连接和命名空间';


-- ==========================================
-- 2. t_resource 数据资源主表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_resource (
    -- 主键与基础信息
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'resource唯一标识',
    f_catalog_id              VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属catalog ID',
    f_name                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '数据资源名称，catalog下唯一',
    f_tags                    VARCHAR(255) NOT NULL DEFAULT '[]' COMMENT '标签，JSON数组格式',
    f_description             VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '数据资源描述',

    f_category                VARCHAR(20) NOT NULL DEFAULT '' COMMENT '数据资源类型: table, file, fileset, api, metric, topic, index, logicview, dataset',

    -- 状态管理
    f_enabled                 TINYINT(1) NOT NULL DEFAULT 1 COMMENT '资源是否启用',
    f_status                  VARCHAR(20) NOT NULL DEFAULT 'active' COMMENT '数据资源状态: active, deprecated, stale',
    f_status_message          VARCHAR(500) NOT NULL DEFAULT '' COMMENT '状态说明',
    f_last_discover_status    VARCHAR(32) NOT NULL DEFAULT '' COMMENT '最近一次扫描观察状态',

    -- 物理数据资源专属字段
    f_schema                  VARCHAR(128) NOT NULL DEFAULT '' COMMENT '所属 schema 名称，由发现流程写入',
    f_source_identifier       VARCHAR(500) NOT NULL DEFAULT '' COMMENT '源端标识(表名/文件路径/索引名等)',
    f_source_metadata         MEDIUMTEXT NOT NULL COMMENT '源端元数据（JSON格式）',

    -- Schema相关
    f_schema_definition       MEDIUMTEXT NOT NULL COMMENT 'Schema定义（JSON数组格式，包含所有字段信息）',
    f_index_config            MEDIUMTEXT NOT NULL COMMENT '本地索引配置（JSON格式）',

    -- LogicView 专属字段
    f_logic_type              VARCHAR(20) NOT NULL DEFAULT '' COMMENT '逻辑类型: derived(衍生), composite(复合), 仅LogicView使用',
    f_logic_definition        MEDIUMTEXT NOT NULL COMMENT '逻辑定义（JSON格式），仅LogicView使用',

    -- Local索引已提交状态
    f_local_status            VARCHAR(20) NOT NULL DEFAULT 'unavailable' COMMENT 'Local index status: unavailable, available, stale',
    f_local_index_name        VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Local OpenSearch index name',
    f_sync_mark               TEXT NOT NULL COMMENT 'Committed batch SyncCheckpoint owned by the Resource',

    -- 审计字段
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
    f_updater                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
    f_updater_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
    f_update_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_category (f_category),
    INDEX idx_status (f_status),
    INDEX idx_enabled (f_enabled),
    INDEX idx_catalog_schema (f_catalog_id, f_schema)
)  ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='数据资源主表，管理所有类型的数据资源';


-- ==========================================
-- 3. t_resource_schema_history Schema历史表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_resource_schema_history (
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT '历史记录唯一标识',
    f_resource_id             VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属resource ID',
    f_schema_version          VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Schema版本号',
    f_schema_definition       MEDIUMTEXT NOT NULL COMMENT 'Schema定义快照（JSON数组格式）',

    -- 变更信息
    f_change_type             VARCHAR(20) NOT NULL DEFAULT '' COMMENT '变更类型: created, field_added, field_removed, field_modified, type_changed',
    f_change_summary          VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '变更摘要',
    f_schema_inferred         BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Schema是否为自动推导',
    f_change_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '变更时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_resource_id (f_resource_id)
)  ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='数据资源Schema历史表，记录Schema变更历史';


-- ==========================================
-- 4. t_connector_type Connector 类型注册表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_connector_type (
    -- 主键与基础信息
    f_type                    VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'connector类型,唯一标识',
    f_name                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '类型名称: mysql, postgresql, kafka...',
    f_tags                    VARCHAR(255) NOT NULL DEFAULT '[]' COMMENT '标签，JSON数组格式',
    f_description             VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '类型描述',

    -- 类型分类
    f_mode                    VARCHAR(20) NOT NULL DEFAULT '' COMMENT '模式: local, remote',
    f_category                VARCHAR(32) NOT NULL DEFAULT '' COMMENT '分类: table, index, topic, file, api',

    -- Remote 模式专用字段
    f_endpoint                VARCHAR(512) NOT NULL DEFAULT '' COMMENT '远程服务地址 (仅remote模式)',

    -- 状态
    f_enabled                 BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否启用',

    -- 索引
    PRIMARY KEY (f_type),
    UNIQUE INDEX uk_name (f_name),
    INDEX idx_mode (f_mode),
    INDEX idx_category (f_category),
    INDEX idx_enabled (f_enabled)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='Connector类型注册表';


-- ==========================================
-- 5. 初始化内置 Local Connector
-- ==========================================
INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'mariadb', 'mariadb', 'MariaDB 关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'mariadb' );

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'mysql', 'mysql', 'MySQL 关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'mysql' );

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'opensearch', 'opensearch', 'OpenSearch 搜索引擎连接器', 'local', 'index', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'opensearch' );

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'postgresql', 'postgresql', 'PostgreSQL 关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'postgresql' );

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'sqlserver', 'sqlserver', 'Microsoft SQL Server 关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'sqlserver' );

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'anyshare', 'anyshare', 'AnyShare 连接器', 'local', 'fileset', TRUE
FROM DUAL WHERE NOT EXISTS ( SELECT f_type FROM t_connector_type WHERE f_type = 'anyshare' );

-- ==========================================
-- 6. t_discover_task 发现任务表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_discover_task (
    -- 主键与关联信息
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT '任务唯一标识',
    f_catalog_id              VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属catalog ID',
    f_resource_id             VARCHAR(40) NOT NULL DEFAULT '' COMMENT '单资源刷新目标；空表示 Catalog 扫描',
    f_schedule_id             VARCHAR(40) NOT NULL DEFAULT '' COMMENT '关联的 DiscoverSchedule ID',
    f_strategy                VARCHAR(32) NOT NULL DEFAULT 'full_sync' COMMENT '发现策略: full_sync, create_only, cleanup_only',
    f_strategies              VARCHAR(100) NOT NULL DEFAULT '' COMMENT '历史策略数组字段',
    f_trigger_type            VARCHAR(20) NOT NULL DEFAULT 'manual' COMMENT '触发类型: manual(立即执行), scheduled(定时驱动)',
    f_queue_priority          TINYINT NOT NULL DEFAULT 20 COMMENT '调度优先级，数值越大越优先',

    -- 任务状态
    f_status                  VARCHAR(20) NOT NULL DEFAULT 'pending' COMMENT '任务状态: pending, running, completed, failed, cancelled',
    f_progress                INT NOT NULL DEFAULT 0 COMMENT '任务进度: 0-100',
    f_message                 VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '任务消息/错误信息',

    -- 时间信息
    f_start_time              BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始执行时间',
    f_finish_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '完成时间',
    f_last_progress_time      BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次对外可观测进度更新时间',

    -- 执行结果
    f_result                  MEDIUMTEXT NOT NULL COMMENT '发现结果（JSON格式，包含发现的资源统计等）',

    -- 审计字段
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_catalog_id (f_catalog_id),
    INDEX idx_status (f_status),
    INDEX idx_pending_priority (f_status, f_queue_priority, f_create_time, f_id),
    INDEX idx_resource_active (f_resource_id, f_status),
    INDEX idx_schedule_id (f_schedule_id),
    INDEX idx_create_time (f_create_time),
    INDEX idx_start_time (f_start_time),
    INDEX idx_finish_time (f_finish_time),
    INDEX idx_last_progress_time (f_last_progress_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='发现任务表，记录异步资源发现任务的状态和结果';

-- ==========================================
-- 7. t_build_task 构建任务表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_build_task (
    -- 主键与关联信息
    f_id                      VARCHAR(40) NOT NULL COMMENT '任务ID',
    f_resource_id             VARCHAR(40) NOT NULL COMMENT '资源ID',
    f_catalog_id              VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属catalog ID',

    f_mode                    VARCHAR(20) NOT NULL COMMENT '任务模式: full, incremental, realtime',
    f_execute_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'batch 执行类型: full, incremental；空表示 streaming 任务',

    -- 任务索引配置
    f_index_config            MEDIUMTEXT NOT NULL COMMENT '索引配置快照(JSON)',

    -- 任务状态
    f_status                  VARCHAR(20) NOT NULL COMMENT '任务状态: pending, running, stopping, stopped, completed, failed, cancelled',
    f_total_count             BIGINT NOT NULL DEFAULT 0 COMMENT '总数',
    f_synced_count            BIGINT NOT NULL DEFAULT 0 COMMENT '已同步数',
    f_synced_mark             TEXT NOT NULL COMMENT 'Task execution checkpoint (batch SyncCheckpoint; streaming opaque)',
    f_error_msg               TEXT NOT NULL COMMENT '错误信息',
    f_failure_detail          TEXT NOT NULL COMMENT '构建完成但部分文档向量化失败的明细（区别于 f_error_msg 的整任务硬失败）',

    -- 审计字段
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
    f_start_time              BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始执行时间',
    f_finish_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '完成时间',
    f_last_progress_time      BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次对外可观测进度更新时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_resource_id (f_resource_id),
    INDEX idx_catalog_id (f_catalog_id),
    INDEX idx_status (f_status),
    INDEX idx_create_time (f_create_time),
    INDEX idx_start_time (f_start_time),
    INDEX idx_finish_time (f_finish_time),
    INDEX idx_last_progress_time (f_last_progress_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='构建任务表';

-- ==========================================
-- 8. t_semantic_understanding_task 语义理解任务表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_semantic_understanding_task (
    -- 主键与关联信息
    f_id                         VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Vega 语义理解任务唯一标识',
    f_scope                      VARCHAR(20) NOT NULL DEFAULT '' COMMENT '任务范围: resource, catalog',
    f_catalog_id                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属 catalog ID',
    f_resource_id                VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'resource 级任务关联 resource ID，catalog 级为空',
    f_agent_task_id              VARCHAR(80) NOT NULL DEFAULT '' COMMENT 'bkn-agent 任务 ID',
    f_agent_id                   VARCHAR(80) NOT NULL DEFAULT '' COMMENT '执行语义理解的 agent ID',

    -- 输入快照与状态
    f_input                      MEDIUMTEXT NOT NULL COMMENT '发送给 bkn-agent 的完整结构化输入(JSON)',
    f_input_hash                 VARCHAR(128) NOT NULL DEFAULT '' COMMENT '基于 agent 输入生成的哈希，用于去重和快照匹配',
    f_status                     VARCHAR(20) NOT NULL DEFAULT 'pending' COMMENT '任务状态: pending, running, completed, failed, cancelled',
    f_apply_mode                 VARCHAR(20) NOT NULL DEFAULT 'fill_empty' COMMENT '应用模式: dry_run, fill_empty, force',

    -- agent 结果与应用详情
    f_result_json                MEDIUMTEXT NOT NULL COMMENT 'agent 原始结构化输出(JSON)',
    f_confidence_threshold       DECIMAL(5,4) NOT NULL DEFAULT 0.7500 COMMENT '本次任务要求的最低置信分',
    f_confidence                 DECIMAL(5,4) NOT NULL DEFAULT 0.0000 COMMENT '任务级语义置信度',
    f_confidence_detail_json     MEDIUMTEXT NOT NULL COMMENT '字段、逻辑视图、stale 建议等细粒度置信分(JSON)',
    f_apply_detail_json          MEDIUMTEXT NOT NULL COMMENT '应用明细(JSON)',
    f_applied                    TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'agent 结果是否已应用: 0-否, 1-是',
    f_failure_detail             TEXT NOT NULL COMMENT '失败详情',

    -- 审计字段
    f_creator                    VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type               VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',

    f_start_time                 BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始执行时间',
    f_finish_time                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '完成时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_scope_input_hash_status (f_scope, f_input_hash, f_status),
    INDEX idx_catalog_id (f_catalog_id),
    INDEX idx_resource_id (f_resource_id),
    INDEX idx_agent_task_id (f_agent_task_id),
    INDEX idx_status (f_status),
    INDEX idx_create_time (f_create_time),
    INDEX idx_start_time (f_start_time),
    INDEX idx_finish_time (f_finish_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='语义理解任务表，记录 resource/catalog 语义理解异步任务、agent 输出和应用状态';


-- ==========================================
-- 9. t_discover_schedule 资源发现调度表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_discover_schedule (
    -- 主键与关联信息
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT '调度唯一标识',
    f_name                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '调度名称',
    f_catalog_id              VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属catalog ID',
    f_cron_expr               VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'Cron表达式',

    -- 时间配置
    f_start_time              BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始时间（Unix毫秒时间戳）',
    f_end_time                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '结束时间（Unix毫秒时间戳），0表示无结束时间',

    -- 调度状态
    f_enabled                 TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否启用: 0-禁用, 1-启用',
    f_strategy                VARCHAR(32) NOT NULL DEFAULT 'full_sync' COMMENT '发现策略: full_sync, create_only, cleanup_only',

    f_last_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最后执行时间（Unix毫秒时间戳）',
    f_next_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '下次执行时间（Unix毫秒时间戳）',

    -- 审计字段
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者id',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
    f_updater                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者id',
    f_updater_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
    f_update_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',

    -- 索引
    PRIMARY KEY (f_id),
    INDEX idx_catalog_id (f_catalog_id),
    INDEX idx_enabled (f_enabled),
    INDEX idx_next_run (f_next_run),
    INDEX idx_name (f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='资源发现调度表，记录定时资源发现的配置和执行状态';

-- ==========================================
-- 10. t_catalog_health_check_schedule Catalog 健康检查调度表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_catalog_health_check_schedule (
    f_catalog_id              VARCHAR(40) NOT NULL COMMENT '所属 Catalog ID，同时为主键',
    f_mode                    VARCHAR(16) NOT NULL DEFAULT 'inherit' COMMENT '调度模式: inherit, enabled, disabled',
    f_cron_expr               VARCHAR(100) NOT NULL DEFAULT '' COMMENT '自定义 Cron；enabled 时必填',
    f_last_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次计划执行时间（Unix 毫秒时间戳）',
    f_next_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '下一次计划执行时间（Unix 毫秒时间戳）',
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者 ID',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
    f_updater                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者 ID',
    f_updater_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
    f_update_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
    PRIMARY KEY (f_catalog_id),
    INDEX idx_mode_next_run (f_mode, f_next_run)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='Catalog 定时健康检查配置与执行元数据';

-- ==========================================
-- 11. t_vega_operation_audit 数据资源管理操作审计表
-- ==========================================
CREATE TABLE IF NOT EXISTS t_vega_operation_audit (
    event_id                 VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_time               DATETIME(6) NOT NULL,
    recorded_at              DATETIME(6) NOT NULL,
    tenant_id                VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    business_domain_id       VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_id                 VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    actor_name               VARCHAR(255) NOT NULL,
    actor_type               VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    auth_method              VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_id               VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_channel           VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    method                   VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    action                   VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_type              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_id                VARCHAR(1024) NOT NULL,
    target_name              VARCHAR(1024) NOT NULL,
    outcome                  VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    failure_code             VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    failure_message          VARCHAR(512) NOT NULL DEFAULT '',
    PRIMARY KEY (event_id),
    INDEX idx_vega_audit_tenant_time (tenant_id, event_time, event_id),
    INDEX idx_vega_audit_domain_time (business_domain_id, event_time, event_id),
    INDEX idx_vega_audit_actor_time (actor_id, event_time, event_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='Vega 数据资源管理操作审计事实';
