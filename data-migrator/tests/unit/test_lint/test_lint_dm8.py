#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import pytest
from tests.unit.conftest import make_check_config
from server.lint.rds.dm8 import LintDM8
from server.utils.table_define import Database, Column


# ── SQL fixtures ──────────────────────────────────────────────────────────────

SET_SCHEMA = "SET SCHEMA adp"

# DM8 建表：必须有 IF NOT EXISTS，VARCHAR 必须用 n CHAR，整数类型不能有长度
SIMPLE_TABLE = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL IDENTITY(1, 1),
  f_name VARCHAR(64 CHAR) NOT NULL DEFAULT '' COMMENT '名称',
  CLUSTER PRIMARY KEY (f_id)
)"""

TABLE_NO_IF_NOT_EXISTS = """\
CREATE TABLE t_user (
  f_id BIGINT NOT NULL,
  CLUSTER PRIMARY KEY (f_id)
)"""

TABLE_NO_PK = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL
)"""

TABLE_WITH_FOREIGN_KEY = """\
CREATE TABLE IF NOT EXISTS t_order (
  f_id BIGINT NOT NULL,
  f_user_id BIGINT NOT NULL,
  FOREIGN KEY (f_user_id) REFERENCES t_user (f_id),
  CLUSTER PRIMARY KEY (f_id)
)"""


@pytest.fixture
def lint(cfg, logger):
    return LintDM8(cfg, logger)


@pytest.fixture
def lint_allow_no_pk(cfg_allow_no_pk, logger):
    return LintDM8(cfg_allow_no_pk, logger)


@pytest.fixture
def lint_allow_fk(cfg_allow_fk, logger):
    return LintDM8(cfg_allow_fk, logger)


@pytest.fixture
def db():
    return Database("adp")


# ── check_init ────────────────────────────────────────────────────────────────

class TestCheckInit:
    def test_empty_list(self, lint):
        lint.check_init([])

    def test_valid(self, lint):
        lint.check_init([SET_SCHEMA, SIMPLE_TABLE])

    def test_valid_set_identity_insert(self, lint):
        lint.check_init([SET_SCHEMA, SIMPLE_TABLE,
                         "SET IDENTITY_INSERT t_user ON",
                         "INSERT INTO t_user VALUES (1, 'a')",
                         "SET IDENTITY_INSERT t_user OFF"])

    def test_valid_create_view(self, lint):
        lint.check_init([SET_SCHEMA, "CREATE VIEW v_foo AS SELECT 1"])

    def test_first_stmt_not_set_raises(self, lint):
        with pytest.raises(Exception, match="SET SCHEMA"):
            lint.check_init([SIMPLE_TABLE])

    def test_illegal_stmt_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([SET_SCHEMA, "UPDATE t_user SET f_name = 'x'"])

    def test_set_invalid_subcommand_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([SET_SCHEMA, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED"])


# ── check_update ──────────────────────────────────────────────────────────────

class TestCheckUpdate:
    def test_empty_list(self, lint):
        lint.check_update([])

    def test_valid_alter_table(self, lint):
        lint.check_update([SET_SCHEMA, "ALTER TABLE t_user ADD f_age INT"])

    def test_valid_drop_index(self, lint):
        lint.check_update([SET_SCHEMA, "DROP INDEX idx_foo"])

    def test_valid_dml(self, lint):
        lint.check_update([SET_SCHEMA, "INSERT INTO t_user VALUES (1, 'a')",
                           "UPDATE t_user SET f_name = 'b'",
                           "DELETE FROM t_user WHERE f_id = 1"])

    def test_first_stmt_not_set_raises(self, lint):
        with pytest.raises(Exception, match="SET SCHEMA"):
            lint.check_update(["ALTER TABLE t_user ADD f_x INT"])

    def test_invalid_drop_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_update([SET_SCHEMA, "DROP DATABASE foo"])


# ── _parse_and_check_create_table ─────────────────────────────────────────────

class TestParseCreateTable:
    def test_valid(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        assert "t_user" in db.Tables

    def test_without_if_not_exists_ok(self, lint, db):
        lint._parse_and_check_create_table(TABLE_NO_IF_NOT_EXISTS, db)
        assert "t_user" in db.Tables

    def test_no_primary_key_raises(self, lint, db):
        with pytest.raises(Exception, match="主键"):
            lint._parse_and_check_create_table(TABLE_NO_PK, db)

    def test_no_primary_key_allowed(self, lint_allow_no_pk, db):
        lint_allow_no_pk._parse_and_check_create_table(TABLE_NO_PK, db)

    def test_foreign_key_not_allowed_raises(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        with pytest.raises(Exception, match="外键"):
            lint._parse_and_check_create_table(TABLE_WITH_FOREIGN_KEY, db)

    def test_table_options_not_allowed_raises(self, lint, db):
        sql = """\
CREATE TABLE IF NOT EXISTS t_bad (
  f_id BIGINT NOT NULL,
  CLUSTER PRIMARY KEY (f_id)
) ENGINE = InnoDB"""
        with pytest.raises(Exception, match="不合法的关键字"):
            lint._parse_and_check_create_table(sql, db)


# ── check_column (DM8 特有规则) ───────────────────────────────────────────────

class TestCheckColumn:
    def test_char_type_raises(self, lint):
        col = Column("f_code", "CHAR")
        with pytest.raises(Exception, match="CHAR"):
            lint.check_column("t_x", col)

    def test_varchar_without_char_suffix_raises(self, lint):
        col = Column("f_name", "VARCHAR")
        col.ColumnLen = "64"
        with pytest.raises(Exception, match="VARCHAR"):
            lint.check_column("t_x", col)

    def test_varchar_with_char_suffix_ok(self, lint):
        col = Column("f_name", "VARCHAR")
        col.ColumnLen = "64 CHAR"
        lint.check_column("t_x", col)

    def test_int_with_length_raises(self, lint):
        col = Column("f_count", "INT")
        col.ColumnLen = "11"
        with pytest.raises(Exception, match="数值类型长度"):
            lint.check_column("t_x", col)

    def test_bigint_without_length_ok(self, lint):
        col = Column("f_id", "BIGINT")
        lint.check_column("t_x", col)

    def test_text_with_non_null_default_raises(self, lint):
        col = Column("f_content", "TEXT")
        col.ColumnDefault = "''"
        with pytest.raises(Exception, match="文本类型"):
            lint.check_column("t_x", col)

    def test_text_index_column_raises(self, lint):
        # TEXT 类型字段不能建索引 — 直接构造对象验证规则，绕开多行 SQL 的 rfind 边界问题
        from server.utils.table_define import Table, Index, PrimaryIndex
        table = Table("t_doc", lint.logger)
        col_id = Column("f_id", "BIGINT")
        col_body = Column("f_body", "TEXT")
        table.add_column(col_id)
        table.add_column(col_body)
        pk = PrimaryIndex("t_doc")
        pk.add_column("f_id")
        table.set_primary_index(pk)
        idx = Index("t_doc", "idx_body", lint.logger)
        idx.add_column("f_body")
        table.add_index(idx)
        with pytest.raises(Exception, match="文本类型"):
            lint._check_table(table)
