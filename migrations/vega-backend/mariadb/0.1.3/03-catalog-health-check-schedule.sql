-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

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

INSERT IGNORE INTO t_catalog_health_check_schedule (
    f_catalog_id, f_mode, f_cron_expr, f_last_run, f_next_run,
    f_creator, f_creator_type, f_create_time,
    f_updater, f_updater_type, f_update_time
)
SELECT
    f_id, 'inherit', '', 0, 0,
    f_creator, f_creator_type, f_create_time,
    f_updater, f_updater_type, f_update_time
FROM t_catalog
WHERE f_type = 'physical';
