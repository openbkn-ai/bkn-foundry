-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- MariaDB / MySQL / TiDB
-- 用于 source_type=external 模式，由 DBA 手动执行初始化 deploy 管控库。
-- 请将 `deploy` 替换为实际库名（与 config.yaml 中 deploy_db 一致）。

CREATE DATABASE IF NOT EXISTS `deploy` DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_general_ci;

-- 迁移任务表：每个服务唯一一条记录，仅记录成功态，兼做断点续跑锚点
CREATE TABLE IF NOT EXISTS `deploy`.`t_schema_migration_task` (
  `f_id`                BIGINT      NOT NULL AUTO_INCREMENT COMMENT '主键',
  `f_service_name`      VARCHAR(255) NOT NULL COMMENT '微服务名',
  `f_installed_version` VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '已完成的最新版本',
  `f_target_version`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '本次迁移的目标版本',
  `f_script_file_name`  VARCHAR(512) NOT NULL DEFAULT '' COMMENT '最后成功执行的脚本（version/filename）',
  `f_create_time`       DATETIME     NOT NULL COMMENT '创建时间',
  `f_update_time`       DATETIME     NOT NULL COMMENT '最后更新时间',
  PRIMARY KEY (`f_id`),
  UNIQUE KEY `uk_service_name` (`f_service_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移任务主表';

-- 迁移历史表：每次脚本执行追加一条，success 和 failed 均记录
CREATE TABLE IF NOT EXISTS `deploy`.`t_schema_migration_history` (
  `f_id`               BIGINT       NOT NULL AUTO_INCREMENT COMMENT '主键',
  `f_service_name`     VARCHAR(255) NOT NULL COMMENT '微服务名',
  `f_version`          VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '脚本所属版本',
  `f_script_file_name` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '脚本文件名（version/filename）',
  `f_status`           VARCHAR(32)  NOT NULL DEFAULT 'success' COMMENT 'success / failed',
  `f_message`          TEXT         COMMENT '失败时的错误信息',
  `f_create_time`      DATETIME     NOT NULL COMMENT '执行时间',
  PRIMARY KEY (`f_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移历史流水表';
