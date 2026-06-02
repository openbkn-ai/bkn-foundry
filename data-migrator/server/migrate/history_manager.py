#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""t_schema_migration_history 管理 - 历史记录写入"""
import datetime
from logging import Logger

from server.db.operate import OperateDB
from server.config.models import RDSConfig
from server.migrate.task_manager import TaskStatus

class HistoryManager:
    TABLE = "t_schema_migration_history"

    def __init__(self, rds_config: RDSConfig, logger: Logger):
        self.db = OperateDB(rds_config, logger)
        self.rds_config = rds_config
        self.logger = logger
        self.deploy_db = rds_config.get_deploy_db_name()

    @staticmethod
    def get_create_table_sql(deploy_db: str, db_type: str = "mariadb") -> str:
        if db_type == "dm8":
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{HistoryManager.TABLE} (
                    f_id               BIGINT       IDENTITY(1,1),
                    f_service_name     VARCHAR(255) NOT NULL,
                    f_version          VARCHAR(64)  NOT NULL DEFAULT '',
                    f_script_file_name VARCHAR(512) NOT NULL DEFAULT '',
                    f_status           VARCHAR(32)  NOT NULL DEFAULT 'success',
                    f_message          TEXT,
                    f_create_time      DATETIME     NOT NULL,
                    CLUSTER PRIMARY KEY ("f_id")
                );
            """
        elif db_type == "kdb9":
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{HistoryManager.TABLE} (
                    f_id               BIGSERIAL,
                    f_service_name     VARCHAR(255) NOT NULL,
                    f_version          VARCHAR(64)  NOT NULL DEFAULT '',
                    f_script_file_name VARCHAR(512) NOT NULL DEFAULT '',
                    f_status           VARCHAR(32)  NOT NULL DEFAULT 'success',
                    f_message          TEXT,
                    f_create_time      TIMESTAMP    NOT NULL,
                    PRIMARY KEY (f_id)
                );
            """
        else:
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{HistoryManager.TABLE} (
                    `f_id`               BIGINT       NOT NULL AUTO_INCREMENT COMMENT '主键',
                    `f_service_name`     VARCHAR(255) NOT NULL COMMENT '微服务名',
                    `f_version`          VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '脚本所属版本',
                    `f_script_file_name` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '脚本文件名（version/filename）',
                    `f_status`           VARCHAR(32)  NOT NULL DEFAULT 'success' COMMENT 'success / failed',
                    `f_message`          TEXT         COMMENT '失败时的错误信息',
                    `f_create_time`      DATETIME     NOT NULL COMMENT '执行时间',
                    PRIMARY KEY (`f_id`)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移历史流水表';
            """

    def record(self, service_name: str, version: str, script_file_name: str,
               status: TaskStatus, message: str = ""):
        """记录一条迁移历史

        Args:
            service_name: 服务名称
            version: 版本号
            script_file_name: 脚本文件名
            status: 执行状态
            message: 错误信息（失败时填写）
        """
        now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        self.db.insert(f"{self.deploy_db}.{self.TABLE}", {
            "f_service_name": service_name,
            "f_version": version,
            "f_script_file_name": script_file_name,
            "f_status": status,
            "f_message": message,
            "f_create_time": now,
        })
