#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""TaskManager 单元测试

mock self.db（OperateDB），验证 SQL 内容和参数顺序正确性。
"""
from unittest.mock import MagicMock, call

from server.migrate.task_manager import TaskManager


def make_manager():
    mgr = TaskManager.__new__(TaskManager)
    mgr.db = MagicMock()
    mgr.deploy_db = "deploy"
    mgr.logger = MagicMock()
    return mgr


class TestGetCreateTableSql:
    def test_uses_table_class_attribute(self):
        sql = TaskManager.get_create_table_sql("mydb")
        assert TaskManager.TABLE in sql

    def test_uses_deploy_db_prefix(self):
        sql = TaskManager.get_create_table_sql("mydb")
        assert "mydb." in sql

    def test_contains_required_columns(self):
        sql = TaskManager.get_create_table_sql("deploy")
        for col in ["f_id", "f_service_name", "f_installed_version",
                    "f_target_version", "f_script_file_name",
                    "f_create_time", "f_update_time"]:
            assert col in sql, f"缺少列: {col}"

    def test_has_unique_key_on_service_name(self):
        sql = TaskManager.get_create_table_sql("deploy")
        assert "UNIQUE" in sql
        assert "f_service_name" in sql

    def test_has_create_if_not_exists(self):
        sql = TaskManager.get_create_table_sql("deploy")
        assert "CREATE TABLE IF NOT EXISTS" in sql


class TestInsertTask:
    def test_calls_db_insert(self):
        mgr = make_manager()
        mgr.insert_task("svc", "1.0.0", "1.0.0", "1.0.0/init.sql")
        mgr.db.insert.assert_called_once()

    def test_insert_contains_all_required_fields(self):
        mgr = make_manager()
        mgr.insert_task("svc", "1.0.0", "1.1.0", "1.0.0/init.sql")

        _, data = mgr.db.insert.call_args[0]
        assert data["f_service_name"] == "svc"
        assert data["f_installed_version"] == "1.0.0"
        assert data["f_target_version"] == "1.1.0"
        assert data["f_script_file_name"] == "1.0.0/init.sql"
        assert "f_create_time" in data
        assert "f_update_time" in data

    def test_targets_correct_table(self):
        mgr = make_manager()
        mgr.insert_task("svc", "1.0.0", "1.0.0", "1.0.0/init.sql")

        table_arg = mgr.db.insert.call_args[0][0]
        assert "deploy" in table_arg
        assert TaskManager.TABLE in table_arg


class TestRecordScriptDone:
    def test_updates_script_file_name(self):
        mgr = make_manager()
        mgr.record_script_done("svc", "1.1.0/02-add.sql")

        sql = mgr.db.execute.call_args[0][0]
        assert "f_script_file_name" in sql
        assert "UPDATE" in sql

    def test_does_not_update_installed_version(self):
        mgr = make_manager()
        mgr.record_script_done("svc", "1.1.0/02-add.sql")

        sql = mgr.db.execute.call_args[0][0]
        assert "f_installed_version" not in sql

    def test_where_clause_uses_service_name(self):
        mgr = make_manager()
        mgr.record_script_done("mysvc", "1.1.0/01-a.sql")

        args = mgr.db.execute.call_args[0]
        # args: (sql, script_file_name, now, service_name)
        assert args[-1] == "mysvc"

    def test_script_file_name_passed_as_param(self):
        mgr = make_manager()
        mgr.record_script_done("svc", "1.1.0/02-b.sql")

        args = mgr.db.execute.call_args[0]
        assert "1.1.0/02-b.sql" in args


class TestRecordVersionDone:
    def test_updates_installed_and_target_version(self):
        mgr = make_manager()
        mgr.record_version_done("svc", "1.1.0", "1.2.0")

        sql = mgr.db.execute.call_args[0][0]
        assert "f_installed_version" in sql
        assert "f_target_version" in sql

    def test_parameter_order_installed_before_target(self):
        """installed_version 必须先于 target_version 传入，顺序与 SQL SET 子句一致"""
        mgr = make_manager()
        mgr.record_version_done("svc", "1.1.0", "1.2.0")

        args = mgr.db.execute.call_args[0]
        # (sql, installed_version, target_version, now, service_name)
        sql, installed, target, now, service = args
        assert installed == "1.1.0"
        assert target == "1.2.0"
        assert service == "svc"

    def test_service_name_in_where_clause(self):
        mgr = make_manager()
        mgr.record_version_done("my-svc", "1.0.0", "1.1.0")

        sql = mgr.db.execute.call_args[0][0]
        assert "WHERE" in sql
        args = mgr.db.execute.call_args[0]
        assert args[-1] == "my-svc"


class TestSelectTask:
    def test_queries_by_service_name(self):
        mgr = make_manager()
        mgr.db.fetch_one.return_value = None

        mgr.select_task("svc")

        sql, param = mgr.db.fetch_one.call_args[0]
        assert "f_service_name" in sql
        assert param == "svc"

    def test_returns_db_result(self):
        mgr = make_manager()
        expected = {"f_service_name": "svc", "f_installed_version": "1.0.0"}
        mgr.db.fetch_one.return_value = expected

        result = mgr.select_task("svc")

        assert result == expected

    def test_queries_correct_table(self):
        mgr = make_manager()
        mgr.db.fetch_one.return_value = None

        mgr.select_task("svc")

        sql = mgr.db.fetch_one.call_args[0][0]
        assert TaskManager.TABLE in sql
        assert "deploy" in sql
