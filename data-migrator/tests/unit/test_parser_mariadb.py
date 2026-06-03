#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""
MariaDB parser 层单元测试 — 覆盖 RDSParser 基类 + MariaDBParser。
所有测试纯字符串操作，无 DB 依赖。
"""
import logging
import pytest
from server.db.dialect._parser.mariadb import MariaDBParser
from server.db.dialect._parser.base import RDSParser


@pytest.fixture
def p():
    """MariaDBParser 实例（通过最小子类实例化）"""
    class _P(MariaDBParser):
        pass
    return _P()


# ── RDSParser._parse_column_len ───────────────────────────────────────────────

class TestParseColumnLen:
    def test_simple_number(self, p):
        length, rest = p._parse_column_len("(64) NOT NULL")
        assert length == "64"
        assert rest == "NOT NULL"

    def test_char_suffix(self, p):
        length, rest = p._parse_column_len("(64 CHAR) NOT NULL")
        assert length == "64 CHAR"
        assert rest == "NOT NULL"

    def test_no_paren_returns_none(self, p):
        length, rest = p._parse_column_len("NOT NULL")
        assert length is None
        assert rest == "NOT NULL"

    def test_empty_input(self, p):
        length, rest = p._parse_column_len("")
        assert length is None
        assert rest == ""

    def test_decimal_precision(self, p):
        length, rest = p._parse_column_len("(10,2) NOT NULL")
        assert length == "10,2"
        assert rest == "NOT NULL"


# ── RDSParser._parse_column_unsigned ─────────────────────────────────────────

class TestParseColumnUnsigned:
    def test_unsigned_present(self, p):
        flag, rest = p._parse_column_unsigned("UNSIGNED NOT NULL")
        assert flag is True
        assert "NOT" in rest

    def test_unsigned_absent(self, p):
        flag, rest = p._parse_column_unsigned("NOT NULL")
        assert flag is False
        assert rest == "NOT NULL"

    def test_empty(self, p):
        flag, rest = p._parse_column_unsigned("")
        assert flag is False


# ── RDSParser._parse_default_value ────────────────────────────────────────────

class TestParseDefaultValue:
    def test_simple_string(self, p):
        val, rest = p._parse_default_value("'' COMMENT 'x'", "")
        assert val == ""
        assert "COMMENT" in rest

    def test_number(self, p):
        val, rest = p._parse_default_value("0 NOT NULL", "")
        assert val == "0"
        assert rest == "NOT NULL"

    def test_null(self, p):
        val, rest = p._parse_default_value("NULL COMMENT 'x'", "")
        assert val.upper() == "NULL"

    def test_function_call(self, p):
        # DEFAULT CURRENT_TIMESTAMP(3)
        val, rest = p._parse_default_value("CURRENT_TIMESTAMP(3) NOT NULL", "")
        assert val == "CURRENT_TIMESTAMP(3)"
        assert rest == "NOT NULL"

    def test_nested_parens(self, p):
        # DEFAULT NOW() COMMENT
        val, rest = p._parse_default_value("NOW() COMMENT 'ts'", "")
        assert val == "NOW()"
        assert "COMMENT" in rest


# ── MariaDBParser.get_real_name ───────────────────────────────────────────────

class TestGetRealName:
    def test_backtick_stripped(self, p):
        assert p.get_real_name("`t_user`") == "t_user"

    def test_no_quotes(self, p):
        assert p.get_real_name("t_user") == "t_user"

    def test_trailing_semicolon_stripped(self, p):
        assert p.get_real_name("t_user;") == "t_user"

    def test_dot_raises(self, p):
        with pytest.raises(Exception, match="不合法字符"):
            p.get_real_name("schema.t_user")

    def test_double_quote_raises(self, p):
        with pytest.raises(Exception, match="不合法字符"):
            p.get_real_name('"t_user"')

    def test_single_quote_raises(self, p):
        with pytest.raises(Exception, match="不合法字符"):
            p.get_real_name("'t_user'")


# ── MariaDBParser.get_real_column_name ────────────────────────────────────────

class TestGetRealColumnName:
    def test_backtick_stripped(self, p):
        assert p.get_real_column_name("`f_id`") == "f_id"

    def test_length_suffix_stripped(self, p):
        # 索引中 col(191) 截取列名
        assert p.get_real_column_name("f_name(191)") == "f_name"

    def test_plain(self, p):
        assert p.get_real_column_name("f_id") == "f_id"

    def test_dot_raises(self, p):
        with pytest.raises(Exception, match="不合法字符"):
            p.get_real_column_name("t.f_id")


# ── MariaDBParser.parse_sql_use_db ────────────────────────────────────────────

class TestParseSqlUseDb:
    def test_simple(self, p):
        db = p.parse_sql_use_db("USE adp")
        assert db.DBName == "adp"

    def test_backtick(self, p):
        db = p.parse_sql_use_db("USE `adp`")
        assert db.DBName == "adp"

    def test_missing_use_raises(self, p):
        with pytest.raises(Exception, match="USE"):
            p.parse_sql_use_db("SELECT 1")

    def test_only_use_raises(self, p):
        with pytest.raises(Exception):
            p.parse_sql_use_db("USE")


# ── MariaDBParser.parse_sql_column_define ─────────────────────────────────────

class TestParseSqlColumnDefine:
    def test_bigint_not_null(self, p):
        col = p.parse_sql_column_define("f_id", "BIGINT NOT NULL")
        assert col.ColumnName == "f_id"
        assert col.ColumnType == "BIGINT"
        assert col.ColumnNull is False

    def test_varchar_with_default_and_comment(self, p):
        col = p.parse_sql_column_define(
            "f_name", "VARCHAR(64) NOT NULL DEFAULT '' COMMENT '名称'"
        )
        assert col.ColumnType == "VARCHAR"
        assert col.ColumnLen == "64"
        assert col.ColumnNull is False
        assert col.ColumnDefault == ""
        assert col.ColumnComment == "名称"

    def test_auto_increment(self, p):
        col = p.parse_sql_column_define("f_id", "BIGINT NOT NULL AUTO_INCREMENT")
        assert col.ColumnIdentity == "AUTO_INCREMENT"

    def test_nullable_column(self, p):
        col = p.parse_sql_column_define("f_deleted", "TINYINT NULL DEFAULT 0")
        assert col.ColumnNull is True
        assert col.ColumnDefault == "0"

    def test_text_no_default(self, p):
        col = p.parse_sql_column_define("f_body", "TEXT NOT NULL")
        assert col.ColumnType == "TEXT"
        assert col.ColumnDefault is None

    def test_character_set(self, p):
        col = p.parse_sql_column_define(
            "f_name", "VARCHAR(128) CHARACTER SET utf8mb4 NOT NULL DEFAULT ''"
        )
        assert col.ColumnCharset == "utf8mb4"

    def test_collate(self, p):
        col = p.parse_sql_column_define(
            "f_name", "VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT ''"
        )
        assert col.ColumnCollate == "utf8mb4_bin"

    def test_unsigned(self, p):
        col = p.parse_sql_column_define("f_count", "INT UNSIGNED NOT NULL DEFAULT 0")
        assert col.ColumnUnsigned is True

    def test_illegal_keyword_raises(self, p):
        with pytest.raises(Exception, match="不合法的关键字"):
            p.parse_sql_column_define("f_x", "INT UNKNOWN_KW")


# ── MariaDBParser.get_column_type ─────────────────────────────────────────────

class TestGetColumnType:
    @pytest.mark.parametrize("data_type,expected_category", [
        ("INT",       "IntegerType"),
        ("BIGINT",    "IntegerType"),
        ("TINYINT",   "IntegerType"),
        ("BOOLEAN",   "IntegerType"),
        ("DECIMAL",   "FixedPointType"),
        ("NUMERIC",   "FixedPointType"),
        ("FLOAT",     "FloatingPointType"),
        ("DOUBLE",    "FloatingPointType"),
        ("BIT",       "BitValueType"),
        ("VARCHAR",   "StringType"),
        ("TEXT",      "StringType"),
        ("BLOB",      "StringType"),
        ("LONGTEXT",  "StringType"),
        ("DATE",      "DateAndTimeType"),
        ("DATETIME",  "DateAndTimeType"),
        ("TIMESTAMP", "DateAndTimeType"),
    ])
    def test_known_types(self, p, data_type, expected_category):
        result_type, category = p.get_column_type({"DATA_TYPE": data_type})
        assert category == expected_category
        assert result_type == data_type

    def test_unknown_type_returns_unknown(self, p):
        _, category = p.get_column_type({"DATA_TYPE": "GEOMETRY"})
        assert category == "UNKNOWN"

    def test_case_insensitive(self, p):
        _, category = p.get_column_type({"DATA_TYPE": "bigint"})
        assert category == "IntegerType"
