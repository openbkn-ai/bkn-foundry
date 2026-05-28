#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""t_schema_migration_task 管理 - INSERT/UPDATE/SELECT"""
import datetime
from enum import Enum
from logging import Logger

from server.db.operate import OperateDB
from server.config.models import RDSConfig


class TaskStatus(str, Enum):
    PENDING = "pending"
    RUNNING = "running"
    SUCCESS = "success"
    FAILED  = "failed"


class TaskManager:
    TABLE = "t_schema_migration_task"

    def __init__(self, rds_config: RDSConfig, logger: Logger):
        self.db = OperateDB(rds_config, logger)
        self.rds_config = rds_config
        self.logger = logger
        self.deploy_db = rds_config.get_deploy_db_name()

    @staticmethod
    def get_create_table_sql(deploy_db: str, db_type: str = "mariadb") -> str:
        if db_type == "dm8":
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{TaskManager.TABLE} (
                    f_id                BIGINT       IDENTITY(1,1),
                    f_service_name      VARCHAR(255) NOT NULL,
                    f_installed_version VARCHAR(64)  NOT NULL DEFAULT '',
                    f_target_version    VARCHAR(64)  NOT NULL DEFAULT '',
                    f_script_file_name  VARCHAR(512) NOT NULL DEFAULT '',
                    f_create_time       DATETIME     NOT NULL,
                    f_update_time       DATETIME     NOT NULL,
                    CLUSTER PRIMARY KEY ("f_id"),
                    CONSTRAINT uk_service_name UNIQUE (f_service_name)
                );
            """
        elif db_type == "kdb9":
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{TaskManager.TABLE} (
                    f_id                BIGSERIAL,
                    f_service_name      VARCHAR(255) NOT NULL,
                    f_installed_version VARCHAR(64)  NOT NULL DEFAULT '',
                    f_target_version    VARCHAR(64)  NOT NULL DEFAULT '',
                    f_script_file_name  VARCHAR(512) NOT NULL DEFAULT '',
                    f_create_time       TIMESTAMP    NOT NULL,
                    f_update_time       TIMESTAMP    NOT NULL,
                    PRIMARY KEY (f_id),
                    CONSTRAINT uk_service_name UNIQUE (f_service_name)
                );
            """
        else:
            return f"""
                CREATE TABLE IF NOT EXISTS {deploy_db}.{TaskManager.TABLE} (
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
            """

    def select_task(self, service_name: str) -> dict:
        """查询服务的迁移任务记录"""
        sql = (
            f"SELECT * FROM {self.deploy_db}.{self.TABLE} "
            f"WHERE f_service_name = %s"
        )
        return self.db.fetch_one(sql, service_name)

    def insert_task(self, service_name: str, installed_version: str,
                    target_version: str, script_file_name: str):
        """插入新任务记录"""
        now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        self.db.insert(f"{self.deploy_db}.{self.TABLE}", {
            "f_service_name": service_name,
            "f_installed_version": installed_version,
            "f_target_version": target_version,
            "f_script_file_name": script_file_name,
            "f_create_time": now,
            "f_update_time": now,
        })

    def record_script_done(self, service_name: str, script_file_name: str):
        """记录单个脚本执行成功（断点续跑锚点）"""
        now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        sql = (
            f"UPDATE {self.deploy_db}.{self.TABLE} "
            f"SET f_script_file_name = %s, f_update_time = %s "
            f"WHERE f_service_name = %s"
        )
        self.db.execute(sql, script_file_name, now, service_name)

    def record_version_done(self, service_name: str, installed_version: str, target_version: str):
        """记录整个版本执行完成"""
        now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        sql = (
            f"UPDATE {self.deploy_db}.{self.TABLE} "
            f"SET f_installed_version = %s, f_target_version = %s, f_update_time = %s "
            f"WHERE f_service_name = %s"
        )
        self.db.execute(sql, installed_version, target_version, now, service_name)

    def update_service_name(self, old_name: str, new_name: str):
        """服务改名"""
        sql = (
            f"UPDATE {self.deploy_db}.{self.TABLE} "
            f"SET f_service_name = %s WHERE f_service_name = %s"
        )
        self.db.execute(sql, new_name, old_name)
