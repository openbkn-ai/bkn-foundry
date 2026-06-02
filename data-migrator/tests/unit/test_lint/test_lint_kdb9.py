#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import pytest
from server.lint.rds.kdb9 import LintKDB9
from server.utils.table_define import Database, Column


# ── SQL fixtures ──────────────────────────────────────────────────────────────

SET_PATH = "SET SEARCH_PATH TO adp"

SIMPLE_TABLE = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL,
  f_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '名称',
  PRIMARY KEY (f_id)
)"""

TABLE_NO_IF_NOT_EXISTS = """\
CREATE TABLE t_user (
  f_id BIGINT NOT NULL,
  PRIMARY KEY (f_id)
)"""

TABLE_NO_PK = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL
)"""

TABLE_WITH_FOREIGN_KEY = """\
CREATE TABLE IF NOT EXISTS t_order (
  f_id BIGINT NOT NULL,
  f_user_id BIGINT NOT NULL,
  PRIMARY KEY (f_id),
  FOREIGN KEY (f_user_id) REFERENCES t_user (f_id)
)"""


@pytest.fixture
def lint(cfg, logger):
    return LintKDB9(cfg, logger)


@pytest.fixture
def lint_allow_no_pk(cfg_allow_no_pk, logger):
    return LintKDB9(cfg_allow_no_pk, logger)


@pytest.fixture
def lint_allow_fk(cfg_allow_fk, logger):
    return LintKDB9(cfg_allow_fk, logger)


@pytest.fixture
def db():
    return Database("adp")


# ── check_init ────────────────────────────────────────────────────────────────

class TestCheckInit:
    def test_empty_list(self, lint):
        lint.check_init([])

    def test_valid(self, lint):
        lint.check_init([SET_PATH, SIMPLE_TABLE])

    def test_valid_with_insert(self, lint):
        lint.check_init([SET_PATH, SIMPLE_TABLE, "INSERT INTO t_user VALUES (1, 'a')"])

    def test_valid_create_view(self, lint):
        lint.check_init([SET_PATH, "CREATE VIEW v_foo AS SELECT 1"])

    def test_first_stmt_not_set_raises(self, lint):
        with pytest.raises(Exception, match="SET SEARCH_PATH"):
            lint.check_init([SIMPLE_TABLE])

    def test_first_stmt_wrong_set_raises(self, lint):
        # SET SCHEMA xxx 不是合法的 KDB9 首句
        with pytest.raises(Exception, match="SET SEARCH_PATH"):
            lint.check_init(["SET SCHEMA adp", SIMPLE_TABLE])

    def test_illegal_stmt_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([SET_PATH, "DROP TABLE t_user"])

    def test_illegal_create_subtype_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([SET_PATH, "CREATE DATABASE foo"])


# ── check_update ──────────────────────────────────────────────────────────────

class TestCheckUpdate:
    def test_empty_list(self, lint):
        lint.check_update([])

    def test_valid_alter_table(self, lint):
        lint.check_update([SET_PATH, "ALTER TABLE t_user ADD COLUMN f_age INT"])

    def test_valid_drop_table(self, lint):
        lint.check_update([SET_PATH, "DROP TABLE t_old"])

    def test_valid_drop_index(self, lint):
        lint.check_update([SET_PATH, "DROP INDEX idx_foo"])

    def test_valid_insert_update(self, lint):
        lint.check_update([SET_PATH, "INSERT INTO t_user VALUES (1, 'a')",
                           "UPDATE t_user SET f_name = 'b'"])

    def test_valid_delete(self, lint):
        lint.check_update([SET_PATH, "DELETE FROM t_user WHERE f_id = 1"])

    def test_first_stmt_not_set_raises(self, lint):
        with pytest.raises(Exception, match="SET SEARCH_PATH"):
            lint.check_update(["ALTER TABLE t_user ADD COLUMN f_x INT"])

    def test_invalid_drop_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_update([SET_PATH, "DROP DATABASE foo"])

    def test_invalid_alter_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_update([SET_PATH, "ALTER INDEX idx_foo"])


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

    def test_foreign_key_allowed(self, lint_allow_fk, db):
        lint_allow_fk._parse_and_check_create_table(SIMPLE_TABLE, db)
        lint_allow_fk._parse_and_check_create_table(TABLE_WITH_FOREIGN_KEY, db)

    def test_table_options_not_allowed_raises(self, lint, db):
        sql = """\
CREATE TABLE IF NOT EXISTS t_bad (
  f_id BIGINT NOT NULL,
  PRIMARY KEY (f_id)
) ENGINE = InnoDB"""
        with pytest.raises(Exception, match="不合法的关键字"):
            lint._parse_and_check_create_table(sql, db)

    def test_duplicate_table_raises(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        with pytest.raises(Exception, match="已存在"):
            lint._parse_and_check_create_table(SIMPLE_TABLE, db)


# ── check_column ──────────────────────────────────────────────────────────────

class TestCheckColumn:
    def test_text_null_default_ok(self, lint):
        col = Column("f_content", "TEXT")
        col.ColumnDefault = "NULL"
        lint.check_column("t_x", col)

    def test_text_with_default_raises(self, lint):
        col = Column("f_content", "TEXT")
        col.ColumnDefault = "''"
        with pytest.raises(Exception, match="文本类型"):
            lint.check_column("t_x", col)

    def test_bigint_ok(self, lint):
        col = Column("f_id", "BIGINT")
        lint.check_column("t_x", col)
