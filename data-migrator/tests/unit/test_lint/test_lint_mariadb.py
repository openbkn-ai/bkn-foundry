#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import pytest
from tests.unit.conftest import make_check_config
from server.lint.rds.mariadb import LintMariaDB
from server.utils.table_define import Database


# ── SQL fixtures ──────────────────────────────────────────────────────────────

USE_DB = "USE adp"

SIMPLE_TABLE = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL AUTO_INCREMENT,
  f_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '名称',
  PRIMARY KEY (f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '用户表'"""

TABLE_NO_IF_NOT_EXISTS = """\
CREATE TABLE t_user (
  f_id BIGINT NOT NULL,
  PRIMARY KEY (f_id)
) ENGINE = InnoDB"""

TABLE_BACKTICK_NAME = """\
CREATE TABLE IF NOT EXISTS `t_user` (
  f_id BIGINT NOT NULL,
  PRIMARY KEY (f_id)
) ENGINE = InnoDB"""

TABLE_NO_PK = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL
) ENGINE = InnoDB"""

TABLE_WITH_FOREIGN_KEY = """\
CREATE TABLE IF NOT EXISTS t_order (
  f_id BIGINT NOT NULL,
  f_user_id BIGINT NOT NULL,
  PRIMARY KEY (f_id),
  FOREIGN KEY (f_user_id) REFERENCES t_user (f_id)
) ENGINE = InnoDB"""

TABLE_TEXT_WITH_DEFAULT = """\
CREATE TABLE IF NOT EXISTS t_doc (
  f_id BIGINT NOT NULL,
  f_content TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (f_id)
) ENGINE = InnoDB"""


@pytest.fixture
def lint(cfg, logger):
    return LintMariaDB(cfg, logger)


@pytest.fixture
def lint_allow_no_pk(cfg_allow_no_pk, logger):
    return LintMariaDB(cfg_allow_no_pk, logger)


@pytest.fixture
def lint_allow_fk(cfg_allow_fk, logger):
    return LintMariaDB(cfg_allow_fk, logger)


@pytest.fixture
def db():
    return Database("adp")


# ── check_init ────────────────────────────────────────────────────────────────

class TestCheckInit:
    def test_empty_list(self, lint):
        lint.check_init([])  # should not raise

    def test_valid_use_and_create(self, lint):
        lint.check_init([USE_DB, SIMPLE_TABLE])

    def test_valid_with_insert(self, lint):
        lint.check_init([USE_DB, SIMPLE_TABLE, "INSERT INTO t_user VALUES (1, 'a')"])

    def test_valid_create_view(self, lint):
        lint.check_init([USE_DB, "CREATE VIEW v_foo AS SELECT 1"])

    def test_valid_create_or_replace_view(self, lint):
        lint.check_init([USE_DB, "CREATE OR REPLACE VIEW v_foo AS SELECT 1"])

    def test_first_stmt_not_use_raises(self, lint):
        with pytest.raises(Exception, match="USE"):
            lint.check_init([SIMPLE_TABLE])

    def test_illegal_stmt_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([USE_DB, "DROP TABLE t_user"])

    def test_illegal_create_subtype_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([USE_DB, "CREATE DATABASE foo"])

    def test_invalid_create_or_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_init([USE_DB, "CREATE OR DROP VIEW v_foo AS SELECT 1"])

    def test_multiple_use_switches_db(self, lint):
        # 切换 schema 后在新 schema 建表，不应报错
        lint.check_init([USE_DB, SIMPLE_TABLE, "USE other_db",
                         TABLE_NO_IF_NOT_EXISTS.replace("t_user", "t_product")])


# ── check_update ──────────────────────────────────────────────────────────────

class TestCheckUpdate:
    def test_empty_list(self, lint):
        lint.check_update([])

    def test_valid_alter_table(self, lint):
        lint.check_update([USE_DB, "ALTER TABLE t_user ADD COLUMN f_age INT"])

    def test_valid_drop_index(self, lint):
        lint.check_update([USE_DB, "DROP INDEX idx_name ON t_user"])

    def test_valid_drop_table(self, lint):
        lint.check_update([USE_DB, "DROP TABLE t_old"])

    def test_valid_drop_view(self, lint):
        lint.check_update([USE_DB, "DROP VIEW v_old"])

    def test_valid_rename(self, lint):
        lint.check_update([USE_DB, "RENAME TABLE t_old TO t_new"])

    def test_valid_dml(self, lint):
        lint.check_update([USE_DB, "UPDATE t_user SET f_name = 'x'",
                           "DELETE FROM t_user WHERE f_id = 1"])

    def test_first_stmt_not_use_raises(self, lint):
        with pytest.raises(Exception, match="USE"):
            lint.check_update(["ALTER TABLE t_user ADD COLUMN f_x INT"])

    def test_invalid_drop_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_update([USE_DB, "DROP DATABASE foo"])

    def test_invalid_alter_type_raises(self, lint):
        with pytest.raises(Exception):
            lint.check_update([USE_DB, "ALTER INDEX idx_foo"])


# ── _parse_and_check_create_table ─────────────────────────────────────────────

class TestParseCreateTable:
    def test_with_if_not_exists(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        assert "t_user" in db.Tables

    def test_without_if_not_exists(self, lint, db):
        lint._parse_and_check_create_table(TABLE_NO_IF_NOT_EXISTS, db)
        assert "t_user" in db.Tables

    def test_backtick_table_name(self, lint, db):
        lint._parse_and_check_create_table(TABLE_BACKTICK_NAME, db)
        assert "t_user" in db.Tables

    def test_duplicate_table_raises(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        with pytest.raises(Exception, match="已存在"):
            lint._parse_and_check_create_table(SIMPLE_TABLE, db)

    def test_no_primary_key_raises(self, lint, db):
        with pytest.raises(Exception, match="主键"):
            lint._parse_and_check_create_table(TABLE_NO_PK, db)

    def test_no_primary_key_allowed(self, lint_allow_no_pk, db):
        lint_allow_no_pk._parse_and_check_create_table(TABLE_NO_PK, db)
        assert "t_user" in db.Tables

    def test_foreign_key_not_allowed_raises(self, lint, db):
        lint._parse_and_check_create_table(SIMPLE_TABLE, db)
        db2 = Database("adp")
        db2.Tables["t_user"] = db.Tables["t_user"]
        with pytest.raises(Exception, match="外键"):
            lint._parse_and_check_create_table(TABLE_WITH_FOREIGN_KEY, db2)

    def test_foreign_key_allowed(self, lint_allow_fk, db):
        lint_allow_fk._parse_and_check_create_table(SIMPLE_TABLE, db)
        db.Tables["t_user"] = db.Tables["t_user"]
        lint_allow_fk._parse_and_check_create_table(TABLE_WITH_FOREIGN_KEY, db)

    def test_text_column_with_default_raises(self, lint, db):
        with pytest.raises(Exception, match="文本类型"):
            lint._parse_and_check_create_table(TABLE_TEXT_WITH_DEFAULT, db)


# ── _parse_table_options ──────────────────────────────────────────────────────

class TestParseTableOptions:
    def _mk(self, logger):
        from server.utils.table_define import Table
        return LintMariaDB(make_check_config(), logger), Table("t_x", logger)

    def test_empty_options(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options("", table)  # should not raise

    def test_engine_only(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options("ENGINE = InnoDB", table)

    def test_default_charset(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options("DEFAULT CHARSET = utf8mb4", table)

    def test_default_character_set(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options("DEFAULT CHARACTER SET = utf8mb4", table)

    def test_default_collate(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options("DEFAULT COLLATE = utf8mb4_bin", table)

    def test_full_option_string(self, logger):
        lint, table = self._mk(logger)
        lint._parse_table_options(
            "ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '用户表'",
            table,
        )

    def test_illegal_keyword_raises(self, logger):
        lint, table = self._mk(logger)
        with pytest.raises(Exception, match="不合法的关键字"):
            lint._parse_table_options("UNKNOWN_OPTION = foo", table)

    def test_comment_with_paren_in_value(self, lint, db):
        # 回归：COMMENT 含 ) 时不应误判为建表语句结尾
        sql = """\
CREATE TABLE IF NOT EXISTS t_user (
  f_id BIGINT NOT NULL AUTO_INCREMENT,
  f_name VARCHAR(64) NOT NULL DEFAULT '',
  PRIMARY KEY (f_id)
) ENGINE = InnoDB COMMENT = '用户表 (v2)'"""
        lint._parse_and_check_create_table(sql, db)
        assert "t_user" in db.Tables


# ── check_column ──────────────────────────────────────────────────────────────

class TestCheckColumn:
    def test_text_null_default_ok(self, lint):
        from server.utils.table_define import Column
        col = Column("f_content", "TEXT")
        col.ColumnDefault = "NULL"
        lint.check_column("t_x", col)  # should not raise

    def test_text_with_default_raises(self, lint):
        from server.utils.table_define import Column
        col = Column("f_content", "TEXT")
        col.ColumnDefault = "''"
        with pytest.raises(Exception, match="文本类型"):
            lint.check_column("t_x", col)

    def test_json_with_default_raises(self, lint):
        from server.utils.table_define import Column
        col = Column("f_data", "JSON")
        col.ColumnDefault = "{}"
        with pytest.raises(Exception, match="文本类型"):
            lint.check_column("t_x", col)

    def test_varchar_no_default_ok(self, lint):
        from server.utils.table_define import Column
        col = Column("f_name", "VARCHAR")
        lint.check_column("t_x", col)  # should not raise
