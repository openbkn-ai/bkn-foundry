#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MigrationExecutor._execute_upgrade_files 单元测试

mock _run_script / task_mgr / history_mgr，专注断点续跑逻辑。
"""
import pytest
from unittest.mock import MagicMock, call

from server.migrate.executor import MigrationExecutor
from server.migrate.task_manager import TaskStatus


def make_executor():
    executor = MigrationExecutor.__new__(MigrationExecutor)
    executor.logger = MagicMock()
    executor.task_mgr = MagicMock()
    executor.history_mgr = MagicMock()
    executor._run_script = MagicMock()
    return executor


def mk_scripts(tmp_path, version, filenames):
    """创建伪脚本文件，返回绝对路径列表（已按 filename 排序）"""
    vdir = tmp_path / version
    vdir.mkdir(parents=True, exist_ok=True)
    paths = []
    for name in sorted(filenames):
        p = vdir / name
        p.write_text("")
        paths.append(str(p))
    return paths


class TestBasicExecution:
    def test_executes_all_scripts_in_order(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        assert executor._run_script.call_count == 2
        executor._run_script.assert_any_call(s[0])
        executor._run_script.assert_any_call(s[1])

    def test_record_script_done_called_after_each_script(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        executor.task_mgr.record_script_done.assert_any_call("svc", "1.1.0/01-a.sql")
        executor.task_mgr.record_script_done.assert_any_call("svc", "1.1.0/02-b.sql")

    def test_record_version_done_called_after_each_version(self, tmp_path):
        s1 = mk_scripts(tmp_path, "1.1.0", ["01-a.sql"])
        s2 = mk_scripts(tmp_path, "1.2.0", ["01-b.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s1), ("1.2.0", s2)], "1.2.0", "")

        executor.task_mgr.record_version_done.assert_any_call("svc", "1.1.0", "1.2.0")
        executor.task_mgr.record_version_done.assert_any_call("svc", "1.2.0", "1.2.0")

    def test_history_success_recorded_per_script(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        call_kwargs = executor.history_mgr.record.call_args.kwargs
        assert call_kwargs["status"] == TaskStatus.SUCCESS
        assert call_kwargs["script_file_name"] == "1.1.0/01-a.sql"


class TestResumeLogic:
    def test_skips_completed_scripts_in_same_version(self, tmp_path):
        """last_script=01，rerun 时跳过 01，从 02 开始"""
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql", "03-c.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "1.1.0/01-a.sql")

        assert executor._run_script.call_count == 2
        executor._run_script.assert_any_call(s[1])
        executor._run_script.assert_any_call(s[2])

    def test_skips_all_scripts_when_last_script_is_final(self, tmp_path):
        """断点在最后一个脚本，rerun 时全部跳过"""
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "1.1.0/02-b.sql")

        executor._run_script.assert_not_called()

    def test_init_sql_as_last_script_runs_all_upgrade_scripts(self, tmp_path):
        """last_script=init.sql（首次安装后的初始值），应执行所有增量脚本"""
        s = mk_scripts(tmp_path, "1.0.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.0.0", s)], "1.0.0", "1.0.0/init.sql")

        assert executor._run_script.call_count == 2

    def test_different_version_anchor_does_not_affect_new_version(self, tmp_path):
        """last_script 属于旧版本，新版本脚本全部执行"""
        s2 = mk_scripts(tmp_path, "1.2.0", ["01-new.sql", "02-new.sql"])
        executor = make_executor()

        executor._execute_upgrade_files("svc", [("1.2.0", s2)], "1.2.0", "1.1.0/02-done.sql")

        assert executor._run_script.call_count == 2

    def test_anchor_version_resets_after_version_completes(self, tmp_path):
        """第一个版本断点续跑后，第二个版本从头执行"""
        s1 = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        s2 = mk_scripts(tmp_path, "1.2.0", ["01-c.sql"])
        executor = make_executor()

        executor._execute_upgrade_files(
            "svc", [("1.1.0", s1), ("1.2.0", s2)], "1.2.0", "1.1.0/01-a.sql"
        )

        # 1.1.0 跳过 01，执行 02；1.2.0 全量执行
        assert executor._run_script.call_count == 2
        executor._run_script.assert_any_call(s1[1])
        executor._run_script.assert_any_call(s2[0])


class TestFailureHandling:
    def test_script_failure_writes_history_failed(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql"])
        executor = make_executor()
        executor._run_script.side_effect = Exception("DB error")

        with pytest.raises(Exception):
            executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        call_kwargs = executor.history_mgr.record.call_args.kwargs
        assert call_kwargs["status"] == TaskStatus.FAILED
        assert call_kwargs["script_file_name"] == "1.1.0/01-a.sql"

    def test_script_failure_reraises_with_script_name(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql"])
        executor = make_executor()
        executor._run_script.side_effect = Exception("timeout")

        with pytest.raises(Exception, match="01-a.sql"):
            executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

    def test_version_done_not_called_on_failure(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()
        executor._run_script.side_effect = Exception("fail")

        with pytest.raises(Exception):
            executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        executor.task_mgr.record_version_done.assert_not_called()

    def test_second_script_failure_does_not_call_version_done(self, tmp_path):
        s = mk_scripts(tmp_path, "1.1.0", ["01-a.sql", "02-b.sql"])
        executor = make_executor()
        executor._run_script.side_effect = [None, Exception("fail on 02")]

        with pytest.raises(Exception):
            executor._execute_upgrade_files("svc", [("1.1.0", s)], "1.1.0", "")

        executor.task_mgr.record_version_done.assert_not_called()
        # 第一个脚本成功，record_script_done 应被调用一次
        executor.task_mgr.record_script_done.assert_called_once_with("svc", "1.1.0/01-a.sql")
