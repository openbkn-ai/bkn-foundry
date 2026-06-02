#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MigrationExecutor 高层流程单元测试

覆盖：_migrate_service 路由、_install_service、_upgrade_service、_list_services
"""
import os
import pytest
from unittest.mock import MagicMock, patch, call

from server.migrate.executor import MigrationExecutor
from server.migrate.task_manager import TaskStatus


def make_executor():
    executor = MigrationExecutor.__new__(MigrationExecutor)
    executor.logger = MagicMock()
    executor.task_mgr = MagicMock()
    executor.history_mgr = MagicMock()
    executor.script_selector = MagicMock()
    executor._execute_sql_list_with_idempotency = MagicMock()
    executor._execute_upgrade_files = MagicMock()
    return executor


# ──────────────────────────────────────────────
# _migrate_service 路由
# ──────────────────────────────────────────────

class TestMigrateServiceRouting:
    def test_no_task_record_calls_install(self):
        executor = make_executor()
        executor.task_mgr.select_task.return_value = None
        executor._install_service = MagicMock()
        executor._upgrade_service = MagicMock()

        executor._migrate_service("svc")

        executor._install_service.assert_called_once_with("svc")
        executor._upgrade_service.assert_not_called()

    def test_existing_task_record_calls_upgrade(self):
        executor = make_executor()
        executor.task_mgr.select_task.return_value = {
            "f_installed_version": "1.0.0",
            "f_script_file_name": "1.0.0/init.sql",
        }
        executor._install_service = MagicMock()
        executor._upgrade_service = MagicMock()

        executor._migrate_service("svc")

        executor._upgrade_service.assert_called_once()
        executor._install_service.assert_not_called()

    def test_upgrade_receives_task_record(self):
        task_record = {"f_installed_version": "1.1.0", "f_script_file_name": "1.1.0/02-x.sql"}
        executor = make_executor()
        executor.task_mgr.select_task.return_value = task_record
        executor._install_service = MagicMock()
        executor._upgrade_service = MagicMock()

        executor._migrate_service("svc")

        executor._upgrade_service.assert_called_once_with("svc", task_record)


# ──────────────────────────────────────────────
# _install_service
# ──────────────────────────────────────────────

class TestInstallService:
    def test_no_init_sql_skips_silently(self):
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (None, None)

        executor._install_service("svc")

        executor.task_mgr.insert_task.assert_not_called()
        executor.history_mgr.record.assert_not_called()

    def test_success_inserts_task_with_correct_args(self, tmp_path):
        init_path = str(tmp_path / "init.sql")
        open(init_path, "w").close()
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (init_path, "1.0.0")

        with patch("server.migrate.executor.parse_sql_file", return_value=["CREATE TABLE..."]):
            executor._install_service("svc")

        executor.task_mgr.insert_task.assert_called_once_with(
            service_name="svc",
            installed_version="1.0.0",
            target_version="1.0.0",
            script_file_name="1.0.0/init.sql",
        )

    def test_success_records_history_success(self, tmp_path):
        init_path = str(tmp_path / "init.sql")
        open(init_path, "w").close()
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (init_path, "1.0.0")

        with patch("server.migrate.executor.parse_sql_file", return_value=[]):
            executor._install_service("svc")

        success_calls = [
            c for c in executor.history_mgr.record.call_args_list
            if c.kwargs.get("status") == TaskStatus.SUCCESS
        ]
        assert len(success_calls) == 1
        assert success_calls[0].kwargs["script_file_name"] == "1.0.0/init.sql"

    def test_failure_does_not_insert_task(self, tmp_path):
        init_path = str(tmp_path / "init.sql")
        open(init_path, "w").close()
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (init_path, "1.0.0")
        executor._execute_sql_list_with_idempotency.side_effect = Exception("syntax error")

        with patch("server.migrate.executor.parse_sql_file", return_value=["BAD SQL"]):
            with pytest.raises(Exception):
                executor._install_service("svc")

        executor.task_mgr.insert_task.assert_not_called()

    def test_failure_records_history_failed(self, tmp_path):
        init_path = str(tmp_path / "init.sql")
        open(init_path, "w").close()
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (init_path, "1.0.0")
        executor._execute_sql_list_with_idempotency.side_effect = Exception("fail")

        with patch("server.migrate.executor.parse_sql_file", return_value=[]):
            with pytest.raises(Exception):
                executor._install_service("svc")

        call_kwargs = executor.history_mgr.record.call_args.kwargs
        assert call_kwargs["status"] == TaskStatus.FAILED
        assert call_kwargs["script_file_name"] == "1.0.0/init.sql"

    def test_failure_reraises_exception(self, tmp_path):
        init_path = str(tmp_path / "init.sql")
        open(init_path, "w").close()
        executor = make_executor()
        executor.script_selector.find_init_sql.return_value = (init_path, "1.0.0")
        executor._execute_sql_list_with_idempotency.side_effect = Exception("conn lost")

        with patch("server.migrate.executor.parse_sql_file", return_value=[]):
            with pytest.raises(Exception, match="init.sql"):
                executor._install_service("svc")


# ──────────────────────────────────────────────
# _upgrade_service
# ──────────────────────────────────────────────

class TestUpgradeService:
    def _task_record(self, installed="1.0.0", last_script="1.0.0/init.sql"):
        return {"f_installed_version": installed, "f_script_file_name": last_script}

    def test_no_scripts_skips_execute(self):
        executor = make_executor()
        executor.script_selector.select_upgrade_scripts.return_value = ([], "1.0.0", False)

        executor._upgrade_service("svc", self._task_record())

        executor._execute_upgrade_files.assert_not_called()

    def test_has_scripts_calls_execute_upgrade_files(self):
        upgrade_files = [("1.1.0", ["/path/01-a.sql"])]
        executor = make_executor()
        executor.script_selector.select_upgrade_scripts.return_value = (upgrade_files, "1.1.0", True)

        executor._upgrade_service("svc", self._task_record("1.0.0", "1.0.0/init.sql"))

        executor._execute_upgrade_files.assert_called_once_with(
            "svc", upgrade_files, "1.1.0", "1.0.0/init.sql"
        )

    def test_passes_installed_version_to_selector(self):
        executor = make_executor()
        executor.script_selector.select_upgrade_scripts.return_value = ([], "1.1.0", False)

        executor._upgrade_service("svc", self._task_record(installed="1.1.0"))

        executor.script_selector.select_upgrade_scripts.assert_called_once_with("svc", "1.1.0")


# ──────────────────────────────────────────────
# _list_services
# ──────────────────────────────────────────────

class TestListServices:
    def _make_executor_with_services(self, repo_path, services):
        executor = MigrationExecutor.__new__(MigrationExecutor)
        executor.logger = MagicMock()
        executor.app_config = MagicMock()
        executor.app_config.repo_path = repo_path
        executor.app_config.services = services
        return executor

    def test_returns_services_with_existing_dirs(self, tmp_path):
        (tmp_path / "svc-a").mkdir()
        (tmp_path / "svc-b").mkdir()
        executor = self._make_executor_with_services(str(tmp_path), ["svc-a", "svc-b"])

        result = executor._list_services()

        assert result == ["svc-a", "svc-b"]

    def test_skips_services_without_dirs(self, tmp_path):
        (tmp_path / "svc-a").mkdir()
        executor = self._make_executor_with_services(str(tmp_path), ["svc-a", "svc-missing"])

        result = executor._list_services()

        assert result == ["svc-a"]
        assert "svc-missing" not in result

    def test_missing_service_logs_warning(self, tmp_path):
        (tmp_path / "svc-a").mkdir()
        executor = self._make_executor_with_services(str(tmp_path), ["svc-a", "svc-missing"])

        executor._list_services()

        executor.logger.warning.assert_called()
        warning_msg = executor.logger.warning.call_args[0][0]
        assert "svc-missing" in warning_msg

    def test_missing_repo_path_returns_empty(self, tmp_path):
        executor = self._make_executor_with_services(str(tmp_path / "nonexistent"), ["svc-a"])

        result = executor._list_services()

        assert result == []

    def test_respects_config_order(self, tmp_path):
        for name in ["svc-c", "svc-a", "svc-b"]:
            (tmp_path / name).mkdir()
        executor = self._make_executor_with_services(str(tmp_path), ["svc-c", "svc-a", "svc-b"])

        result = executor._list_services()

        assert result == ["svc-c", "svc-a", "svc-b"]
