#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""executor._ensure_deploy_tables 的 internal/external 分支测试"""
import logging
from unittest.mock import MagicMock, patch, call

import pytest

from server.config.models import AppConfig, RDSConfig, CheckRulesConfig
from server.migrate.executor import MigrationExecutor
from server.migrate.task_manager import TaskManager
from server.migrate.history_manager import HistoryManager


def make_executor(source_type: str) -> MigrationExecutor:
    rds = RDSConfig(
        host="localhost", port=3306, user="u", password="p",
        type="mariadb", source_type=source_type,
    )
    app_config = AppConfig(rds=rds, check_rules=CheckRulesConfig())
    logger = logging.getLogger("test")

    with patch("server.migrate.executor.OperateDB"), \
         patch("server.migrate.executor.create_dialect"), \
         patch("server.migrate.executor.TaskManager"), \
         patch("server.migrate.executor.HistoryManager"), \
         patch("server.migrate.executor.ScriptSelector"), \
         patch("server.migrate.executor.JsonExecutor"):
        executor = MigrationExecutor(app_config, logger)

    # 替换为可控的 mock
    executor.dialect = MagicMock()
    executor.operate_db = MagicMock()
    return executor


class TestEnsureDeployTablesInternal:
    def test_creates_db_and_tables(self):
        executor = make_executor("internal")
        executor.dialect.CREATE_DATABASE_SQL = "CREATE DATABASE IF NOT EXISTS {db_name}"

        executor._ensure_deploy_tables()

        executor.operate_db.run_ddl.assert_called()
        assert executor.operate_db.run_ddl.call_count >= 3  # db + task表 + history表

    def test_does_not_call_db_exists(self):
        executor = make_executor("internal")
        executor.dialect.CREATE_DATABASE_SQL = "CREATE DATABASE IF NOT EXISTS {db_name}"

        executor._ensure_deploy_tables()

        executor.dialect.db_exists.assert_not_called()
        executor.dialect.table_exists.assert_not_called()


class TestEnsureDeployTablesExternal:
    def test_passes_when_db_and_tables_exist(self):
        executor = make_executor("external")
        executor.dialect.db_exists.return_value = True
        executor.dialect.table_exists.return_value = True

        executor._ensure_deploy_tables()  # 不抛异常

        executor.dialect.db_exists.assert_called_once_with("deploy")
        assert executor.dialect.table_exists.call_count == 2

    def test_raises_when_deploy_db_missing(self):
        executor = make_executor("external")
        executor.dialect.db_exists.return_value = False

        with pytest.raises(Exception, match="deploy.*不存在"):
            executor._ensure_deploy_tables()

    def test_raises_with_task_table_name_when_missing(self):
        executor = make_executor("external")
        executor.dialect.db_exists.return_value = True
        executor.dialect.table_exists.side_effect = lambda db, tbl: tbl != TaskManager.TABLE

        with pytest.raises(Exception, match=TaskManager.TABLE):
            executor._ensure_deploy_tables()

    def test_raises_with_history_table_name_when_missing(self):
        executor = make_executor("external")
        executor.dialect.db_exists.return_value = True
        executor.dialect.table_exists.side_effect = lambda db, tbl: tbl != HistoryManager.TABLE

        with pytest.raises(Exception, match=HistoryManager.TABLE):
            executor._ensure_deploy_tables()

    def test_does_not_call_run_ddl(self):
        executor = make_executor("external")
        executor.dialect.db_exists.return_value = True
        executor.dialect.table_exists.return_value = True

        executor._ensure_deploy_tables()

        executor.operate_db.run_ddl.assert_not_called()
