#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""lint 子命令入口 — 纯静态目录结构与 SQL 语法校验，无 DB 依赖"""
from __future__ import annotations

import json
import os
from logging import Logger
from typing import TYPE_CHECKING

import sqlparse

from server.config.models import AppConfig, CheckRulesConfig
from server.utils.version import VersionUtil

if TYPE_CHECKING:
    from server.lint.rds.base import LintRDS


class LintExecutor:
    def __init__(self, app_config: AppConfig, logger: Logger):
        self.app_config = app_config
        self.logger = logger

    def run(self):
        self.logger.info("开始检查代码库目录结构")

        has_error = False
        for service_name, service_cfg in self.app_config.services.items():
            self.logger.info(f"检查服务目录: {service_name}")
            repo_path = os.path.join(self.app_config.repo_path, service_name)
            check_from = service_cfg.check_from
            if not self._check_repo(repo_path, check_from):
                has_error = True

        if has_error:
            raise Exception("代码库目录检查失败")

        self.logger.info("代码库目录检查成功")

    def _create_lint_rds(self, db_type: str) -> LintRDS:
        from server.lint.rds.mariadb import LintMariaDB
        from server.lint.rds.dm8 import LintDM8
        from server.lint.rds.kdb9 import LintKDB9

        if db_type == "mariadb":
            return LintMariaDB(self.app_config.check_rules, self.logger)
        elif db_type == "dm8":
            return LintDM8(self.app_config.check_rules, self.logger)
        elif db_type == "kdb9":
            return LintKDB9(self.app_config.check_rules, self.logger)
        else:
            raise Exception(f"不支持的数据库类型: {db_type}")

    def _check_repo(self, repo_path: str, check_from: str) -> bool:
        self.logger.info(f"repo目录: {repo_path}")
        for db_type in self.app_config.db_types:
            lint_rds = self._create_lint_rds(db_type)
            repo_db_path = os.path.join(repo_path, db_type)
            try:
                self._check_db_type(repo_db_path, lint_rds, check_from)
            except Exception as e:
                self.logger.error(f"check_db_type 失败: {repo_db_path}, 错误: {e}")
                return False
        return True

    def _check_db_type(self, repo_db_path: str, lint_rds: LintRDS, check_from: str):
        self.logger.info(f"db目录: {repo_db_path}")
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

        for version in versions:
            version_dir = os.path.join(repo_db_path, version.VersionStr)
            self._check_version(version_dir, lint_rds)

    def _check_version(self, version_dir: str, lint_rds: LintRDS):
        self.logger.info(f"version目录: {version_dir}")
        filenames = os.listdir(version_dir)
        if not filenames:
            raise Exception(f"空目录: {version_dir}")

        update_files = {}
        init_file = None

        for filename in filenames:
            filepath = os.path.join(version_dir, filename)
            if os.path.isdir(filepath):
                raise Exception(f"版本目录下不应有子目录: {filepath}")

            if filename == "init.sql":
                init_file = filepath
            elif filename.endswith((".sql", ".py", ".json")):
                if not (len(filename) >= 3 and filename[:2].isdigit() and filename[2] == "-"):
                    raise Exception(f"升级文件命名格式错误 (应为 NN-xxx.sql/py/json): {filename}")
                seq = int(filename[:2])
                if seq in update_files:
                    raise Exception(f"重复的文件序号: {update_files[seq]}, {filepath}")
                update_files[seq] = filepath
            else:
                raise Exception(f"无效的文件: {filepath}")

        if init_file is None:
            raise Exception(f"缺少 init.sql: {version_dir}")
        self._check_init_file(init_file, lint_rds)

        for seq in sorted(update_files):
            self._check_update_file(update_files[seq], lint_rds)

    def _check_init_file(self, init_file: str, lint_rds: LintRDS):
        self.logger.info(f"检查init文件: {init_file}")
        with open(init_file, "r", encoding="utf-8") as f:
            sqls_str = f.read()
        formatted = sqlparse.format(sqls_str, strip_comments=True, keyword_case="upper")
        sql_list = sqlparse.split(formatted)
        sql_list = [sql for sql in sql_list if sql.strip() and sql.strip() != ";"]
        if not sql_list:
            self.logger.warning(f"init.sql 为空: {init_file}")
            return
        lint_rds.check_init(sql_list)

    def _check_update_file(self, filepath: str, lint_rds: LintRDS):
        self.logger.info(f"检查升级文件: {filepath}")
        if filepath.endswith(".json"):
            self._check_update_file_json(filepath, lint_rds)
        elif filepath.endswith(".sql"):
            self._check_update_file_sql(filepath, lint_rds)
        elif filepath.endswith(".py"):
            pass  # .py 文件只检查命名，不执行
        else:
            raise Exception(f"不支持的升级文件类型: {filepath}")
        self.logger.info(f"检查通过: {filepath}")

    def _check_update_file_json(self, filepath: str, lint_rds: LintRDS):
        with open(filepath, "r", encoding="utf-8") as f:
            try:
                update_items = json.load(f)
            except json.JSONDecodeError as e:
                raise Exception(f"无效的JSON文件: {filepath}, {e}")
            if not isinstance(update_items, list):
                raise Exception(f"JSON根类型必须为对象(list): {filepath}")

        required_fields = ["db_name", "table_name", "object_type", "operation_type",
                           "object_name", "object_property", "object_comment"]
        allowed_object_types = {"COLUMN", "INDEX", "UNIQUE INDEX", "CONSTRAINT", "TABLE", "DB"}
        allowed_operation_types = {"ADD", "DROP", "MODIFY", "RENAME"}

        if not update_items:
            raise Exception(f"JSON列表不能为空: {filepath}")

        for item in update_items:
            if not isinstance(item, dict):
                raise Exception(f"格式错误: {item}")
            for field in required_fields:
                if field not in item:
                    raise Exception(f"缺少必填字段 '{field}': {item}")
                if not isinstance(item[field], str):
                    raise Exception(f"字段 '{field}' 必须为字符串: {item}")

            object_type = item["object_type"]
            operation_type = item["operation_type"]

            if object_type not in allowed_object_types:
                raise Exception(f"不支持的 object_type '{object_type}': {item}")
            if operation_type not in allowed_operation_types:
                raise Exception(f"不支持的 operation_type '{operation_type}': {item}")

            if object_type == "COLUMN" and operation_type == "ADD":
                column = lint_rds.parse_sql_column_define(item["object_name"], item["object_property"])
                if column is None:
                    raise Exception(f"无效的列定义: {item['object_name']}, {item['object_property']}")
                lint_rds.check_column(item["table_name"], column)

    def _check_update_file_sql(self, filepath: str, lint_rds: LintRDS):
        self.logger.info(f"检查update sql文件: {filepath}")
        with open(filepath, "r", encoding="utf-8") as f:
            sqls_str = f.read()
        formatted = sqlparse.format(sqls_str, strip_comments=True, keyword_case="upper")
        sql_list = sqlparse.split(formatted)
        if not sql_list:
            self.logger.warning(f"空 SQL 文件: {filepath}")
            return
        lint_rds.check_update(sql_list)
