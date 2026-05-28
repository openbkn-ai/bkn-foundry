#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import pytest
from server.utils.token import next_token, next_tokens, find_matching_paren


class TestNextToken:
    def test_simple_word(self):
        token, rest = next_token("ENGINE = InnoDB")
        assert token == "ENGINE"
        assert rest == "InnoDB"

    def test_equals_stripped_as_delimiter(self):
        # = 作为分隔符，不返回为 token
        token, rest = next_token("= utf8mb4 COLLATE")
        assert token == "utf8mb4"
        assert rest == "COLLATE"

    def test_backtick_quoted(self):
        token, rest = next_token("`t_user`")
        assert token == "t_user"
        assert rest == ""

    def test_backtick_quoted_with_remainder(self):
        token, rest = next_token("`t_user` (")
        assert token == "t_user"
        assert rest == "("

    def test_single_quote(self):
        token, rest = next_token("'hello world' rest")
        assert token == "hello world"
        assert rest == "rest"

    def test_double_quote(self):
        token, rest = next_token('"idx_name" ON')
        assert token == "idx_name"
        assert rest == "ON"

    def test_stops_at_open_paren(self):
        # next_token 在 ( 处停止，( 留在 rest 中
        token, rest = next_token("t_user (id INT)")
        assert token == "t_user"
        assert rest == "(id INT)"

    def test_empty_string(self):
        token, rest = next_token("")
        assert token == ""
        assert rest == ""

    def test_only_spaces(self):
        token, rest = next_token("   ")
        assert token == ""
        assert rest == ""

    def test_only_equals(self):
        token, rest = next_token("=")
        assert token == ""
        assert rest == ""

    def test_leading_spaces(self):
        token, rest = next_token("   CHARSET = utf8mb4")
        assert token == "CHARSET"
        assert rest == "utf8mb4"

    def test_single_token_no_remainder(self):
        token, rest = next_token("INNODB")
        assert token == "INNODB"
        assert rest == ""

    def test_unclosed_backtick_raises(self):
        with pytest.raises(Exception):
            next_token("`unclosed")


class TestNextTokens:
    def test_consume_two(self):
        tokens, rest = next_tokens("CREATE TABLE t_user", 2)
        assert tokens == ["CREATE", "TABLE"]
        assert rest == "t_user"

    def test_consume_three_if_not_exists(self):
        tokens, rest = next_tokens("IF NOT EXISTS t_user (", 3)
        assert tokens == ["IF", "NOT", "EXISTS"]
        assert rest == "t_user ("

    def test_fewer_tokens_than_requested(self):
        tokens, rest = next_tokens("ONE TWO", 5)
        assert tokens == ["ONE", "TWO"]
        assert rest == ""

    def test_empty_input(self):
        tokens, rest = next_tokens("", 3)
        assert tokens == []
        assert rest == ""

    def test_size_zero(self):
        tokens, rest = next_tokens("CREATE TABLE", 0)
        assert tokens == []
        assert rest == "CREATE TABLE"

    def test_create_table_if_not_exists_full(self):
        sql = "CREATE TABLE IF NOT EXISTS t_foo (id INT)"
        # 跳过 CREATE TABLE（2 tokens），剩余从 IF 开始
        tokens, rest = next_tokens(sql, 2)
        assert tokens == ["CREATE", "TABLE"]
        assert rest.startswith("IF")

        # 再跳过 IF NOT EXISTS（3 tokens），剩余从表名开始
        tokens2, rest2 = next_tokens(rest, 3)
        assert tokens2 == ["IF", "NOT", "EXISTS"]
        assert rest2.startswith("t_foo")


class TestFindMatchingParen:
    def test_simple(self):
        assert find_matching_paren("(abc)") == 4

    def test_nested(self):
        # (a (b) c) → closing ) at index 8
        assert find_matching_paren("(a (b) c)") == 8

    def test_with_trailing_content(self):
        s = "(f_id INT) ENGINE = InnoDB"
        assert find_matching_paren(s) == 9

    def test_quoted_paren_skipped(self):
        # ) inside single quotes must not count
        s = "(COMMENT = 'foo (bar)') ENGINE = InnoDB"
        idx = find_matching_paren(s)
        assert s[idx] == ")"
        assert s[idx + 1:].strip().startswith("ENGINE")

    def test_backtick_paren_skipped(self):
        s = "(`col)name` INT)"
        assert find_matching_paren(s) == 15

    def test_multiline(self):
        s = "(\n  f_id BIGINT,\n  PRIMARY KEY (f_id)\n)"
        idx = find_matching_paren(s)
        assert s[idx] == ")"
        assert idx == len(s) - 1

    def test_no_closing_returns_minus_one(self):
        assert find_matching_paren("(unclosed") == -1

    def test_empty_parens(self):
        assert find_matching_paren("()") == 1
