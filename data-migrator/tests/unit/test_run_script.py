#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MigrationExecutor._run_script 单元测试

使用真实子进程验证 .py 脚本的执行、日志输出和异常处理。
"""
import pytest
from unittest.mock import MagicMock, patch

from server.migrate.executor import MigrationExecutor


def make_executor():
    executor = MigrationExecutor.__new__(MigrationExecutor)
    executor.logger = MagicMock()
    executor.dialect = MagicMock()
    executor.json_executor = MagicMock()
    return executor


class TestRunScriptPython:
    def test_success_logs_stdout(self, tmp_path):
        script = tmp_path / "01-ok.py"
        script.write_text("print('hello from script')")
        executor = make_executor()

        executor._run_script(str(script))

        executor.logger.info.assert_any_call("hello from script")

    def test_success_logs_stderr(self, tmp_path):
        script = tmp_path / "01-ok.py"
        script.write_text("import sys; print('warn msg', file=sys.stderr)")
        executor = make_executor()

        executor._run_script(str(script))

        executor.logger.info.assert_any_call("warn msg")

    def test_failure_raises_with_stderr(self, tmp_path):
        script = tmp_path / "01-fail.py"
        script.write_text("import sys; print('err msg', file=sys.stderr); sys.exit(1)")
        executor = make_executor()

        with pytest.raises(Exception, match="err msg"):
            executor._run_script(str(script))

    def test_failure_includes_exit_code(self, tmp_path):
        script = tmp_path / "01-fail.py"
        script.write_text("import sys; sys.exit(2)")
        executor = make_executor()

        with pytest.raises(Exception, match="2"):
            executor._run_script(str(script))

    def test_failure_does_not_log_stdout_on_error(self, tmp_path):
        script = tmp_path / "01-fail.py"
        script.write_text("import sys; print('output'); sys.exit(1)")
        executor = make_executor()

        with pytest.raises(Exception):
            executor._run_script(str(script))

        executor.logger.info.assert_not_called()


class TestRunScriptSql:
    def test_sql_delegates_to_execute(self, tmp_path):
        script = tmp_path / "01-add.sql"
        script.write_text("ALTER TABLE t ADD COLUMN c INT;")
        executor = make_executor()

        with patch("server.migrate.executor.parse_sql_file", return_value=["ALTER TABLE..."]):
            executor._run_script(str(script))

        executor.dialect.run_sql.assert_called_once()

    def test_sql_parse_failure_propagates(self, tmp_path):
        script = tmp_path / "01-add.sql"
        script.write_text("INVALID SQL")
        executor = make_executor()

        with patch("server.migrate.executor.parse_sql_file", side_effect=Exception("parse error")):
            with pytest.raises(Exception, match="parse error"):
                executor._run_script(str(script))


class TestRunScriptUnsupported:
    def test_unsupported_extension_logs_warning(self, tmp_path):
        script = tmp_path / "01-data.txt"
        script.write_text("some data")
        executor = make_executor()

        executor._run_script(str(script))

        executor.logger.warning.assert_called_once()
        assert "不支持" in executor.logger.warning.call_args[0][0]
