-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- #2775: 三类 Schedule 最近触发结果、构建计划及 BuildTask 来源。
-- 仅由获批准的升级执行；历史版本脚本保持不变。
USE openbkn;

ALTER TABLE t_discover_schedule
    ADD COLUMN IF NOT EXISTS f_last_status VARCHAR(20) NOT NULL DEFAULT 'never' COMMENT '最近触发状态: never, unknown, triggering, success, skipped, failed',
    ADD COLUMN IF NOT EXISTS f_last_message TEXT NOT NULL DEFAULT '' COMMENT '最近触发的简短脱敏说明',
    ADD COLUMN IF NOT EXISTS f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '最近触发的内部 UUIDv7';

-- 仅回填没有新触发结果的历史记录；重跑时不覆盖 unknown 或新代码已写入的结果。
UPDATE t_discover_schedule
SET f_last_status = 'unknown', f_last_message = '历史触发结果未记录'
WHERE f_last_run > 0 AND f_last_status = 'never' AND f_run_id = '';

ALTER TABLE t_catalog_health_check_schedule
    ADD COLUMN IF NOT EXISTS f_last_status VARCHAR(20) NOT NULL DEFAULT 'never' COMMENT '最近触发状态: never, unknown, triggering, success, skipped, failed',
    ADD COLUMN IF NOT EXISTS f_last_message TEXT NOT NULL DEFAULT '' COMMENT '最近触发的简短脱敏说明',
    ADD COLUMN IF NOT EXISTS f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '最近触发的内部 UUIDv7';

-- 仅回填没有新触发结果的历史记录；重跑时不覆盖 unknown 或新代码已写入的结果。
UPDATE t_catalog_health_check_schedule
SET f_last_status = 'unknown', f_last_message = '历史触发结果未记录'
WHERE f_last_run > 0 AND f_last_status = 'never' AND f_run_id = '';

ALTER TABLE t_build_task
    ADD COLUMN IF NOT EXISTS f_schedule_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '触发本任务的 BuildSchedule ID；手动任务为空',
    ADD COLUMN IF NOT EXISTS f_trigger_type VARCHAR(20) NOT NULL DEFAULT 'manual' COMMENT '触发类型: manual, scheduled',
    ADD INDEX IF NOT EXISTS idx_schedule_id (f_schedule_id);

CREATE TABLE IF NOT EXISTS t_build_schedule (
    f_id                      VARCHAR(40) NOT NULL DEFAULT '' COMMENT '构建调度唯一标识',
    f_name                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '调度名称',
    f_resource_id             VARCHAR(40) NOT NULL DEFAULT '' COMMENT '绑定的 Resource ID，创建后不可修改',
    f_catalog_id              VARCHAR(40) NOT NULL DEFAULT '' COMMENT '所属 Catalog ID，用于权限过滤',
    f_cron_expr               VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'Cron 表达式，支持表达式内时区',
    f_execute_type            VARCHAR(20) NOT NULL COMMENT '批处理构建类型: full, incremental',
    f_start_time              BIGINT(20) NOT NULL DEFAULT 0 COMMENT '开始时间，Unix 毫秒；0 表示无起始限制',
    f_end_time                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '结束时间，Unix 毫秒；0 表示无结束限制',
    f_enabled                 BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否启用',
    f_last_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近触发尝试时间，Unix 毫秒，不表示业务成功',
    f_next_run                BIGINT(20) NOT NULL DEFAULT 0 COMMENT '下次触发时间，Unix 毫秒',
    f_last_status             VARCHAR(20) NOT NULL DEFAULT 'never' COMMENT '最近触发状态: never, unknown, triggering, success, skipped, failed',
    f_last_message            TEXT NOT NULL DEFAULT '' COMMENT '最近触发的简短脱敏说明',
    f_run_id                  VARCHAR(40) NOT NULL DEFAULT '' COMMENT '最近触发的内部 UUIDv7，用于防止旧结果覆盖新结果',
    f_creator                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者 ID及执行身份',
    f_creator_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间，Unix 毫秒',
    f_updater                 VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者 ID，不接管执行身份',
    f_updater_type            VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
    f_update_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '配置版本，Unix 毫秒',
    PRIMARY KEY (f_id),
    INDEX idx_resource_id (f_resource_id),
    INDEX idx_catalog_id (f_catalog_id),
    INDEX idx_enabled_next_run (f_enabled, f_next_run),
    INDEX idx_name (f_name)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='资源本地索引定时构建配置及最近触发结果';
