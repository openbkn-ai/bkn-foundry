#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""HistoryManager 单元测试

mock self.db（OperateDB），验证 record 行为。
"""
from unittest.mock import MagicMock

from server.migrate.history_manager import HistoryManager
from server.migrate.task_manager import TaskStatus


def make_manager():
    mgr = HistoryManager.__new__(HistoryManager)
    mgr.db = MagicMock()
    mgr.deploy_db = "deploy"
    mgr.logger = MagicMock()
    return mgr


class TestGetCreateTableSql:
    def test_uses_table_class_attribute(self):
        sql = HistoryManager.get_create_table_sql("mydb")
        assert HistoryManager.TABLE in sql

    def test_uses_deploy_db_prefix(self):
        sql = HistoryManager.get_create_table_sql("mydb")
        assert "mydb." in sql

    def test_contains_required_columns(self):
        sql = HistoryManager.get_create_table_sql("deploy")
        for col in ["f_id", "f_service_name", "f_version",
                    "f_script_file_name", "f_status", "f_create_time"]:
            assert col in sql, f"缺少列: {col}"

    def test_does_not_contain_checksum_column(self):
        sql = HistoryManager.get_create_table_sql("deploy")
        assert "f_checksum" not in sql

    def test_has_create_if_not_exists(self):
        sql = HistoryManager.get_create_table_sql("deploy")
        assert "CREATE TABLE IF NOT EXISTS" in sql


class TestRecord:
    def test_inserts_with_all_required_fields(self):
        mgr = make_manager()
        mgr.record("svc", "1.1.0", "1.1.0/01-a.sql", TaskStatus.SUCCESS)

        _, data = mgr.db.insert.call_args[0]
        assert data["f_service_name"] == "svc"
        assert data["f_version"] == "1.1.0"
        assert data["f_script_file_name"] == "1.1.0/01-a.sql"
        assert "f_create_time" in data

    def test_no_checksum_field(self):
        mgr = make_manager()
        mgr.record("svc", "1.0.0", "1.0.0/init.sql", TaskStatus.SUCCESS)

        _, data = mgr.db.insert.call_args[0]
        assert "f_checksum" not in data

    def test_status_success_stored(self):
        mgr = make_manager()
        mgr.record("svc", "1.0.0", "1.0.0/init.sql", TaskStatus.SUCCESS)

        _, data = mgr.db.insert.call_args[0]
        assert data["f_status"] == TaskStatus.SUCCESS

    def test_status_failed_stored(self):
        mgr = make_manager()
        mgr.record("svc", "1.0.0", "1.0.0/01-a.sql", TaskStatus.FAILED)

        _, data = mgr.db.insert.call_args[0]
        assert data["f_status"] == TaskStatus.FAILED

    def test_targets_correct_table(self):
        mgr = make_manager()
        mgr.record("svc", "1.0.0", "1.0.0/init.sql", TaskStatus.SUCCESS)

        table_arg = mgr.db.insert.call_args[0][0]
        assert "deploy" in table_arg
        assert HistoryManager.TABLE in table_arg
