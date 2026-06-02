#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""迁移主循环

流程：
  1. 自建 deploy 表 (CREATE TABLE IF NOT EXISTS)
  2. 处理服务改名
  3. 遍历服务:
     - 注册任务 (INSERT/UPDATE task)
     - 选择脚本 (script_selector)
     - 逐个执行 (幂等预检 + SQL/PY 执行)
     - 记录历史 (history)
     - 更新状态
"""
import os
import subprocess
import sys
from logging import Logger

from server.config.models import AppConfig
from server.db.operate import OperateDB
from server.db.dialect.factory import create_dialect
from server.migrate.task_manager import TaskManager, TaskStatus
from server.migrate.history_manager import HistoryManager
from server.migrate.script_selector import ScriptSelector
from server.migrate.json_executor import JsonExecutor
from server.utils.sql import parse_sql_file
from server.utils.version import extract_number



class MigrationExecutor:
    def __init__(self, app_config: AppConfig, logger: Logger):
        self.app_config = app_config
        self.logger = logger
        self.operate_db = OperateDB(app_config.rds, logger)
        self.dialect = create_dialect(app_config.rds, logger)
        self.task_mgr = TaskManager(app_config.rds, logger)
        self.history_mgr = HistoryManager(app_config.rds, logger)
        self.script_selector = ScriptSelector(app_config, logger)
        self.json_executor = JsonExecutor(self.dialect, logger)
        self.deploy_db = app_config.rds.get_deploy_db_name()

    def run(self):
        """迁移主入口"""
        self.logger.info("========== 开始数据迁移 ==========")

        # 1. 自建 deploy 表
        self._ensure_deploy_tables()

        # 2. 服务改名
        self._handle_renamed_services()

        # 3. 确保数据库存在
        self._ensure_databases_exist()

        # 4. 遍历服务
        services = self._list_services()
        for service_name in services:
            try:
                self._migrate_service(service_name)
            except Exception as ex:
                self.logger.error(f"[{service_name}] 迁移失败: {ex}")
                sys.exit(1)

        self.logger.info("========== 数据迁移完成 ==========")

    def _ensure_deploy_tables(self):
        """确保 deploy 库和管控表存在（internal: 自动创建；external: 仅校验）"""
        if self.app_config.rds.source_type == "external":
            self._verify_deploy_tables()
        else:
            self._create_deploy_tables()

    def _create_deploy_tables(self):
        """internal 模式：自动创建 deploy 库和管控表"""
        self.logger.info(f"确保 deploy 库存在: {self.deploy_db}")
        create_db_sql = self.dialect.CREATE_DATABASE_SQL.format(db_name=self.deploy_db)
        try:
            self.operate_db.run_ddl([create_db_sql])
        except Exception:
            self.logger.info(f"deploy 库可能已存在: {self.deploy_db}")

        task_ddl = self.task_mgr.get_create_table_sql(self.deploy_db, self.app_config.rds.type)
        history_ddl = HistoryManager.get_create_table_sql(self.deploy_db, self.app_config.rds.type)
        self.operate_db.run_ddl([task_ddl])
        self.operate_db.run_ddl([history_ddl])
        self.logger.info("deploy 管控表就绪")

    def _verify_deploy_tables(self):
        """external 模式：校验 deploy 库和管控表已存在，否则报错退出"""
        self.logger.info(f"external 模式: 校验 deploy 库存在: {self.deploy_db}")
        if not self.dialect.db_exists(self.deploy_db):
            raise Exception(f"external 模式: deploy 库 '{self.deploy_db}' 不存在，请手动创建")

        for table_name in [TaskManager.TABLE, HistoryManager.TABLE]:
            if not self.dialect.table_exists(self.deploy_db, table_name):
                raise Exception( f"external 模式: 管控表 '{self.deploy_db}.{table_name}' 不存在，请手动创建")
        self.logger.info("deploy 管控表校验通过")

    def _handle_renamed_services(self):
        """处理服务改名"""
        for item in self.app_config.renamed_services:
            old_name = item.get("old_name", "")
            new_name = item.get("new_name", "")
            if old_name and new_name:
                self.logger.info(f"服务改名: {old_name} -> {new_name}")
                self.task_mgr.update_service_name(old_name, new_name)

    def _ensure_databases_exist(self):
        """确保配置中的数据库存在

        internal 模式: 数据库不存在时自动创建
        external 模式: 数据库不存在时报错，不创建
        """
        if self.app_config.rds.source_type == "external":
            for db_name in self.app_config.databases:
                if not self.dialect.db_exists(db_name):
                    raise Exception(f"external 模式: 数据库 '{db_name}' 不存在，请手动创建")
                self.logger.debug(f"数据库已存在: {db_name}")
        else:
            for db_name in self.app_config.databases:
                if not self.dialect.db_exists(db_name):
                    self.logger.info(f"创建数据库: {db_name}")
                    try:
                        self.dialect.create_db(db_name)
                    except Exception as e:
                        self.logger.error(f"创建数据库可能失败: {db_name}, 错误: {e}")
                        raise Exception(f"internal 模式: 数据库 '{db_name}' 创建失败，请检查数据库配置")
                else:
                    self.logger.debug(f"数据库已存在，跳过: {db_name}")

    def _list_services(self):
        """获取要迁移的服务列表"""
        script_dir = self.app_config.repo_path
        if not os.path.isdir(script_dir):
            self.logger.warning(f"脚本目录不存在: {script_dir}")
            return []

        result = []
        for name in self.app_config.services:
            svc_path = os.path.join(script_dir, name)
            if not os.path.isdir(svc_path):
                self.logger.warning(f"服务目录不存在，跳过: {svc_path}")
                continue
            result.append(name)
        return result

    def _migrate_service(self, service_name: str):
        """迁移单个服务"""
        self.logger.info(f"======= 开始迁移服务: {service_name} =======")

        task_record = self.task_mgr.select_task(service_name)

        if task_record:
            # 升级路径
            self._upgrade_service(service_name, task_record)
        else:
            # 首次安装路径
            self._install_service(service_name)

    def _install_service(self, service_name: str):
        """首次安装：执行最大版本的 init.sql，成功后写 task"""
        self.logger.info(f"[{service_name}] 首次安装")

        init_path, init_version = self.script_selector.find_init_sql(service_name)
        if not init_path:
            self.logger.warning(f"[{service_name}] 未找到 init.sql，跳过")
            return

        relative_name = f"{init_version}/init.sql"

        try:
            sql_list = parse_sql_file(init_path, self.logger)
            self._execute_sql_list_with_idempotency(sql_list)
        except Exception as ex:
            self.history_mgr.record(
                service_name=service_name,
                version=init_version,
                script_file_name=relative_name,
                status=TaskStatus.FAILED,
                message=str(ex),
            )
            raise Exception(f"[{service_name}] init.sql 执行失败: {ex}")

        self.task_mgr.insert_task(
            service_name=service_name,
            installed_version=init_version,
            target_version=init_version,
            script_file_name=relative_name,
        )
        self.history_mgr.record(
            service_name=service_name,
            version=init_version,
            script_file_name=relative_name,
            status=TaskStatus.SUCCESS,
        )
        self.logger.info(f"[{service_name}] 安装完成, version={init_version}")

    def _upgrade_service(self, service_name: str, task_record: dict):
        """升级路径"""
        installed_version = task_record["f_installed_version"]
        last_script = task_record["f_script_file_name"]
        self.logger.info(f"[{service_name}] 升级, installed_version={installed_version}")

        upgrade_files, max_version, has_scripts = self.script_selector.select_upgrade_scripts(
            service_name, installed_version
        )

        if not has_scripts:
            self.logger.info(f"[{service_name}] 无需升级，已是最新版本")
            return

        self._execute_upgrade_files(service_name, upgrade_files, max_version, last_script)
        self.logger.info(f"[{service_name}] 升级完成, version={max_version}")

    def _execute_upgrade_files(self, service_name: str, upgrade_files_list: list,
                               target_version: str, last_script: str):
        """执行升级文件列表，支持断点续跑。

        last_script: task 表中上次成功执行的脚本（格式 "version/filename"），
                     用于跳过同版本内已完成的脚本。
        """
        last_script_version = last_script.split("/")[0] if last_script else None
        last_script_filename = last_script.split("/")[1] if last_script else ""
        last_script_seq = extract_number(last_script_filename) if last_script_filename and last_script_filename != "init.sql" else -1

        for version, version_scripts in upgrade_files_list:
            for script_path in version_scripts:
                script_name = os.path.basename(script_path)
                relative_name = f"{version}/{script_name}"

                # 断点续跑：跳过同版本内已完成的脚本
                if version == last_script_version:
                    if extract_number(script_name) <= last_script_seq:
                        self.logger.info(f"[{service_name}] 跳过（已完成）: {relative_name}")
                        continue

                self.logger.info(f"[{service_name}] 执行: {relative_name}")

                try:
                    self._run_script(script_path)
                except Exception as ex:
                    self.history_mgr.record(
                        service_name=service_name,
                        version=version,
                        script_file_name=relative_name,
                        status=TaskStatus.FAILED,
                        message=str(ex),
                    )
                    raise Exception(f"执行脚本失败: {relative_name}, error: {ex}")

                # 成功：更新 task 的最后成功脚本，记录历史
                self.task_mgr.record_script_done(service_name, relative_name)
                self.history_mgr.record(
                    service_name=service_name,
                    version=version,
                    script_file_name=relative_name,
                    status=TaskStatus.SUCCESS,
                )
                self.logger.info(f"[{service_name}] 成功: {relative_name}")

            # 版本内所有脚本完成
            self.task_mgr.record_version_done(service_name, version, target_version)
            last_script_version = None
            last_script_seq = -1

    def _run_script(self, script_path: str):
        """执行单个脚本文件（.sql 或 .py）"""
        _, ext = os.path.splitext(script_path)

        if ext == ".sql":
            sql_list = parse_sql_file(script_path, self.logger)
            self._execute_sql_list_with_idempotency(sql_list)

        elif ext == ".json":
            self.json_executor.execute(script_path)

        elif ext == ".py":
            custom_env = os.environ.copy()
            custom_env["PYTHONUNBUFFERED"] = "1"
            try:
                result = subprocess.run(
                    [sys.executable, script_path],
                    env=custom_env,
                    capture_output=True,
                    text=True,
                    check=True,
                    encoding="utf-8",
                )
            except subprocess.CalledProcessError as e:
                stderr_output = e.stderr.strip() if e.stderr else ""
                raise Exception(f"[{script_path}] 退出码 {e.returncode}\n{stderr_output}")
            for line in result.stdout.splitlines():
                self.logger.info(line.strip())
            for line in result.stderr.splitlines():
                self.logger.info(line.strip())

        else:
            self.logger.warning(f"不支持的脚本类型: {script_path}，跳过")

    def _execute_sql_list_with_idempotency(self, sql_list: list):
        """带幂等执行 SQL 列表（委托给 dialect.run_sql）"""
        self.dialect.run_sql(sql_list)
