#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""run_sql logs the affected row count of INSERT/UPDATE/DELETE.

A conditional statement that matches no row raises nothing and the script is recorded as
successful, so the log is the only place a no-op upgrade shows (bkn-foundry#3295).
"""
from contextlib import contextmanager
from unittest.mock import MagicMock

from server.db.dialect.mariadb import MariaDBDialect


class FakeCursor:
    def __init__(self, rowcounts):
        self.rowcounts = list(rowcounts)
        self.rowcount = -1

    def execute(self, sql):
        self.rowcount = self.rowcounts.pop(0)

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def make_dialect(cursor):
    dialect = MariaDBDialect({"DB_TYPE": "MariaDB"}, MagicMock())
    conn = MagicMock()
    conn.cursor.return_value = cursor

    @contextmanager
    def connect():
        yield conn

    dialect._connect = connect
    return dialect


def test_logs_affected_rows_of_dml_only():
    # USE, INSERT matching nothing, UPDATE matching 3 rows, SELECT, DELETE matching 1 row
    cursor = FakeCursor([0, 0, 3, 1, 1])
    dialect = make_dialect(cursor)

    dialect.run_sql([
        "USE openbkn",
        "insert into t (a) select 1 from dual where 1 = 0",
        "update t set a = 2 where a = 1",
        "select 1",
        "delete from t where a = 2",
    ])

    messages = [call.args[0] for call in dialect.logger.info.call_args_list]
    assert "[SQL] INSERT affected rows: 0" in messages
    assert "[SQL] UPDATE affected rows: 3" in messages
    assert "[SQL] DELETE affected rows: 1" in messages
    assert not any(m.startswith("[SQL] SELECT affected") or m.startswith("[SQL] USE affected") for m in messages)
