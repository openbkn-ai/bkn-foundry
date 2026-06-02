-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- KDB9（人大金仓）
-- 用于 source_type=external 模式，由 DBA 手动执行初始化 deploy 管控 Schema。
-- 请将 deploy 替换为实际 Schema 名（与 config.yaml 中 deploy_db 一致）。

CREATE SCHEMA deploy;

-- 迁移任务表：每个服务唯一一条记录，仅记录成功态，兼做断点续跑锚点
CREATE TABLE IF NOT EXISTS deploy.t_schema_migration_task (
  f_id                BIGSERIAL,
  f_service_name      VARCHAR(255) NOT NULL,
  f_installed_version VARCHAR(64)  NOT NULL DEFAULT '',
  f_target_version    VARCHAR(64)  NOT NULL DEFAULT '',
  f_script_file_name  VARCHAR(512) NOT NULL DEFAULT '',
  f_create_time       TIMESTAMP    NOT NULL, -- 注意：KDB9 的 TIMESTAMP 含时区语义，与 MariaDB/DM8 的 DATETIME 不同；应用层写入时以本地时间字符串传入，无需显式处理时区
  f_update_time       TIMESTAMP    NOT NULL,
  PRIMARY KEY (`f_id`),
  CONSTRAINT uk_service_name UNIQUE (f_service_name)
);

-- 迁移历史表：每次脚本执行追加一条，success 和 failed 均记录
CREATE TABLE IF NOT EXISTS deploy.t_schema_migration_history (
  f_id               BIGSERIAL,
  f_service_name     VARCHAR(255) NOT NULL,
  f_version          VARCHAR(64)  NOT NULL DEFAULT '',
  f_script_file_name VARCHAR(512) NOT NULL DEFAULT '',
  f_status           VARCHAR(32)  NOT NULL DEFAULT 'success',
  f_message          TEXT,
  f_create_time      TIMESTAMP    NOT NULL, -- 同上，含时区语义
  PRIMARY KEY (`f_id`)
);
