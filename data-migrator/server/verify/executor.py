#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""verify 子命令入口 — 连接测试 DB，执行 SQL 校验 + schema 对比"""
import json
import os
import subprocess
import sys
from logging import Logger
from typing import Optional

import yaml
import sqlparse

from server.config.models import AppConfig, CheckRulesConfig, DEFAULT_DB_TYPE_FALLBACK
from server.verify.rds.mariadb import VerifyMariaDB
from server.verify.rds.mysql import VerifyMySQL
from server.verify.rds.dm8 import VerifyDM8
from server.verify.rds.kdb9 import VerifyKDB9
from server.db.dialect.base import RDSDialect
from server.utils.version import VersionUtil

_DEFAULT_RDS_CONFIG_PATH = os.path.join(os.path.dirname(__file__), "rds", "verify_rds_config.yaml")

def _load_verify_rds_config(config_path: Optional[str]) -> dict:
    path = config_path or _DEFAULT_RDS_CONFIG_PATH
    with open(path, "r", encoding="utf-8") as f:
        return yaml.safe_load(f)


def _validate_rds_config(rds_cfg: dict, required_db_types: list, config_path: str):
    missing = [t for t in required_db_types if t not in rds_cfg]
    if missing:
        raise Exception(
            f"verify_rds_config.yaml 缺少以下数据库类型的连接配置: {missing}，"
            f"配置文件路径: {config_path}"
        )


class VerifyExecutor:
    def __init__(self, app_config: AppConfig, logger: Logger, verify_rds_config_path: Optional[str] = None):
        self.app_config = app_config
        self.logger = logger
        path = verify_rds_config_path or _DEFAULT_RDS_CONFIG_PATH
        self.rds_cfg = _load_verify_rds_config(path)
        _validate_rds_config(self.rds_cfg, self.app_config.db_types, path)

    def run(self):
        self.logger.info("开始验证数据模型脚本")

        self._reset_schema()

        for service_name, service_cfg in self.app_config.services.items():
            self.logger.info(f"验证服务: {service_name}")
            repo_path = os.path.join(self.app_config.repo_path, service_name)
            check_from = service_cfg.check_from
            if not self._verify_repo(repo_path, check_from):
                raise Exception(f"数据模型 {service_name} 验证失败")

        self.logger.info("数据模型验证成功")

    def _create_verify_rds(self, db_type: str, is_primary: bool = True) -> RDSDialect:
        section = self.rds_cfg[db_type]["primary" if is_primary else "secondary"]
        if db_type == "mariadb":
            return VerifyMariaDB({**section, "DB_TYPE": "MARIADB"}, self.app_config.check_rules, self.logger)
        elif db_type == "mysql":
            return VerifyMySQL({**section, "DB_TYPE": "MYSQL"}, self.app_config.check_rules, self.logger)
        elif db_type == "dm8":
            return VerifyDM8({**section, "DB_TYPE": "DM8"}, self.app_config.check_rules, self.logger)
        elif db_type == "kdb9":
            return VerifyKDB9({**section, "DB_TYPE": "KDB9"}, self.app_config.check_rules, self.logger)
        else:
            raise Exception(f"不支持的数据库类型: {db_type}")

    def _reset_schema(self):
        self.logger.info("重置数据模式")
        for db_type in self.app_config.db_types:
            primary = self._create_verify_rds(db_type, is_primary=True)
            secondary = self._create_verify_rds(db_type, is_primary=False)
            try:
                primary.reset_schema(self.app_config.databases)
                secondary.reset_schema(self.app_config.databases)
            except Exception as e:
                self.logger.error(f"reset_schema 失败: {db_type}, 错误: {e}")
                raise

    def _verify_repo(self, repo_path: str, check_from: str) -> bool:
        # mariadb 永远作为 base，先单独初始化
        mariadb_primary = self._create_verify_rds(DEFAULT_DB_TYPE_FALLBACK, is_primary=True)
        mariadb_secondary = self._create_verify_rds(DEFAULT_DB_TYPE_FALLBACK, is_primary=False)
        mariadb_path = os.path.join(repo_path, DEFAULT_DB_TYPE_FALLBACK)
        try:
            self._verify_db_type(mariadb_path, mariadb_primary, mariadb_secondary, check_from)
        except Exception as e:
            self.logger.error(f"verify_db_type 失败: {mariadb_path}, 错误: {e}")
            return False

        for db_type in self.app_config.db_types:
            if db_type == DEFAULT_DB_TYPE_FALLBACK:
                continue

            primary = self._create_verify_rds(db_type, is_primary=True)
            secondary = self._create_verify_rds(db_type, is_primary=False)

            repo_db_path = os.path.join(repo_path, db_type)
            if not os.path.isdir(repo_db_path):
                self.logger.warning(
                    f"目录 {repo_db_path} 不存在，fallback 到 mariadb 目录: {mariadb_path}"
                )
                repo_db_path = mariadb_path
            try:
                self._verify_db_type(repo_db_path, primary, secondary, check_from)
            except Exception as e:
                self.logger.error(f"verify_db_type 失败: {repo_db_path}, 错误: {e}")
                return False

            self._compare_schema(mariadb_primary, primary)

        return True

    def _verify_db_type(self, repo_db_path: str, primary: RDSDialect,
                        secondary: RDSDialect, check_from: str):
        versions = []
        for v in os.listdir(repo_db_path):
            try:
                versions.append(VersionUtil(v))
            except (ValueError, AttributeError):
                continue
        versions.sort()

        if check_from:
            from_version = VersionUtil(check_from)
            versions = [v for v in versions if v >= from_version]

        if self.app_config.check_rules.check_type == CheckRulesConfig.CheckLatest:
            if len(versions) >= 1:
                versions = versions[-1:]
        elif self.app_config.check_rules.check_type == CheckRulesConfig.CheckRecently:
            if len(versions) >= 2:
                versions = versions[-2:]

        # primary 模拟升级路径：
        #   - 第一个版本：执行 init.sql 建立初始 schema
        #   - 后续版本：依次执行 upgrades，逐步演进到最新 schema
        for i, version in enumerate(versions):
            version_dir = os.path.join(repo_db_path, version.VersionStr)
            if i == 0:
                self._verify_version_init(version_dir, primary)
            else:
                self._verify_version_upgrades(version_dir, primary)

        # secondary 模拟全新安装：直接执行最新版本的 init.sql
        # 最终由 _compare_schema 对比两库，验证"升级路径 ≡ 全新安装"
        if len(versions) >= 1:
            last_dir = os.path.join(repo_db_path, versions[-1].VersionStr)
            self._verify_version_init(last_dir, secondary)

    def _verify_version_init(self, version_dir: str, check_rds: RDSDialect):
        init_file = os.path.join(version_dir, "init.sql")
        if not os.path.isfile(init_file):
            return

        self.logger.info(f"执行 init.sql: {init_file}")
        with open(init_file, "r", encoding="utf-8") as f:
            sqls_str = f.read()
        formatted = sqlparse.format(sqls_str, strip_comments=True, keyword_case="upper")
        sql_list = sqlparse.split(formatted)
        sql_list = [sql for sql in sql_list if sql.strip() and sql.strip() != ";"]
        if sql_list:
            check_rds.run_sql(sql_list)

    def _verify_version_upgrades(self, version_dir: str, check_rds: RDSDialect):
        filenames = sorted(os.listdir(version_dir))
        for filename in filenames:
            if filename == "init.sql":
                continue
            filepath = os.path.join(version_dir, filename)
            if not os.path.isfile(filepath):
                continue

            if filename.endswith(".json"):
                self._verify_json_file(filepath, check_rds)
            elif filename.endswith(".sql"):
                self._verify_sql_file(filepath, check_rds)
            elif filename.endswith(".py"):
                self._verify_py_file(filepath, check_rds)

    def _verify_sql_file(self, filepath: str, check_rds: RDSDialect):
        self.logger.info(f"执行 SQL 文件: {filepath}")
        with open(filepath, "r", encoding="utf-8") as f:
            sqls_str = f.read()
        formatted = sqlparse.format(sqls_str, strip_comments=True, keyword_case="upper")
        sql_list = sqlparse.split(formatted)
        sql_list = [sql for sql in sql_list if sql.strip() and sql.strip() != ";"]
        if sql_list:
            check_rds.run_sql(sql_list)

    def _verify_json_file(self, filepath: str, check_rds: RDSDialect):
        self.logger.info(f"执行 JSON 文件: {filepath}")
        with open(filepath, "r", encoding="utf-8") as f:
            try:
                update_items = json.load(f)
            except json.JSONDecodeError as e:
                raise Exception(f"无效的JSON文件: {filepath}, {e}")
            if not isinstance(update_items, list):
                raise Exception(f"JSON根类型必须为对象(list): {filepath}")

        if not update_items:
            raise Exception(f"JSON列表不能为空: {filepath}")

        required_fields = ["db_name", "table_name", "object_type", "operation_type",
                           "object_name", "object_property", "object_comment"]
        allowed_object_types = {"COLUMN", "INDEX", "UNIQUE INDEX", "CONSTRAINT", "TABLE", "DB"}
        allowed_operation_types = {"ADD", "DROP", "MODIFY", "RENAME"}

        for item in update_items:
            if not isinstance(item, dict):
                raise Exception(f"格式错误: {item}")

            for field in required_fields:
                if field not in item:
                    raise Exception(f"缺少必填字段 '{field}': {item}")
                if not isinstance(item[field], str):
                    raise Exception(f"字段 '{field}' 必须为字符串: {item}")

            db_name = item["db_name"]
            table_name = item["table_name"]
            object_type = item["object_type"]
            operation_type = item["operation_type"]
            object_name = item.get("object_name", "")
            new_name = item.get("new_name", "")
            object_property = item.get("object_property", "")
            object_comment = item.get("object_comment", "")

            if object_type not in allowed_object_types:
                raise Exception(f"不支持的 object_type '{object_type}': {item}")
            if operation_type not in allowed_operation_types:
                raise Exception(f"不支持的 operation_type '{operation_type}': {item}")

            if object_type == "COLUMN":
                if operation_type == "ADD":
                    check_rds.add_column(db_name, table_name, object_name, object_property, object_comment)
                elif operation_type == "MODIFY":
                    check_rds.modify_column(db_name, table_name, object_name, object_property, object_comment)
                elif operation_type == "RENAME":
                    check_rds.rename_column(db_name, table_name, object_name, new_name, object_property, object_comment)
                elif operation_type == "DROP":
                    check_rds.drop_column(db_name, table_name, object_name)
            elif object_type in ("INDEX", "UNIQUE INDEX"):
                if operation_type == "ADD":
                    check_rds.add_index(db_name, table_name, object_type, object_name, object_property, object_comment)
                elif operation_type == "RENAME":
                    check_rds.rename_index(db_name, table_name, object_name, new_name)
                elif operation_type == "DROP":
                    check_rds.drop_index(db_name, table_name, object_name)
            elif object_type == "CONSTRAINT":
                if operation_type == "ADD":
                    check_rds.add_constraint(db_name, table_name, object_name, object_property)
                elif operation_type == "RENAME":
                    check_rds.rename_constraint(db_name, table_name, object_name, new_name)
                elif operation_type == "DROP":
                    check_rds.drop_constraint(db_name, table_name, object_name)
            elif object_type == "TABLE":
                if operation_type == "RENAME":
                    check_rds.rename_table(db_name, table_name, new_name)
                elif operation_type == "DROP":
                    check_rds.drop_table(db_name, table_name)
            elif object_type == "DB":
                if operation_type == "DROP":
                    check_rds.drop_db(db_name)

    def _verify_py_file(self, filepath: str, check_rds: RDSDialect):
        self.logger.info(f"执行 Python 文件: {filepath}")
        try:
            custom_env = os.environ.copy()
            custom_env["CI_MODE"] = "true"
            custom_env["PYTHONUNBUFFERED"] = "1"
            custom_env["DB_TYPE"] = check_rds.DB_TYPE
            custom_env["DB_HOST"] = check_rds.conn_config["host"]
            custom_env["DB_PORT"] = str(check_rds.conn_config["port"])
            custom_env["DB_USER"] = check_rds.conn_config["user"]
            custom_env["DB_PASSWD"] = check_rds.conn_config["password"]

            result = subprocess.run(
                [sys.executable, filepath],
                env=custom_env,
                capture_output=True,
                text=True,
                check=True,
                encoding="utf-8",
            )
            self.logger.info(f"运行 {filepath} 成功, result: {result}")
        except subprocess.CalledProcessError as e:
            self.logger.error(f"Python 文件执行失败: {filepath}, 错误: {e.stderr}")
            if not self.app_config.check_rules.allow_python_exception:
                raise Exception(f"运行 Python 文件失败: {filepath}")

    def _compare_schema(self, base_rds: RDSDialect, check_rds: RDSDialect):
        self.logger.info(f"对比数据库 schema 差异: {base_rds.DB_TYPE} -> {check_rds.DB_TYPE}")

        for db_name in self.app_config.databases:
            base_tables = base_rds.list_tables_by_db(db_name)
            check_tables = check_rds.list_tables_by_db(db_name)

            extra = set(check_tables) - set(base_tables)
            if extra:
                self.logger.warning(f"对比库多出的表(允许): {extra}")

            missing = set(base_tables) - set(check_tables)
            if missing:
                self.logger.error(f"对比库缺少的表: {missing}, 基准库: {base_tables}, 对比库: {check_tables}")
                raise Exception(f"数据库 {db_name} 表数量不一致, 对比库缺少: {missing}")

            for table in base_tables:
                if table not in check_tables:
                    self.logger.warning(f"表 {table} 在对比库中不存在")
                    continue

                base_cols = base_rds.get_table_columns(db_name, table)
                check_cols = check_rds.get_table_columns(db_name, table)
                only_in_base = set(base_cols) - set(check_cols)
                only_in_check = set(check_cols) - set(base_cols)
                if only_in_base:
                    self.logger.error(f"表 {db_name}.{table} 仅在基准({base_rds.DB_TYPE})中存在的列: {sorted(only_in_base)}")
                    raise Exception(f"表 {db_name}.{table} 列不一致: 仅基准有 {sorted(only_in_base)}")
                if only_in_check:
                    self.logger.error(f"表 {db_name}.{table} 仅在对比({check_rds.DB_TYPE})中存在的列: {sorted(only_in_check)}")
                    raise Exception(f"表 {db_name}.{table} 列不一致: 仅对比有 {sorted(only_in_check)}")

                for col_name, base_col in base_cols.items():
                    if col_name not in check_cols:
                        continue

                    base_type, base_category = base_rds.get_column_type(base_col)
                    check_type, check_category = check_rds.get_column_type(check_col := check_cols[col_name])
                    if base_category != check_category:
                        self.logger.warning(
                            f"表 {db_name}.{table} 列 {col_name} 数据类型不一致, "
                            f"{base_rds.DB_TYPE}: {base_type} -> {check_rds.DB_TYPE}: {check_type}"
                        )

        self.logger.info("schema 差异对比完成")
