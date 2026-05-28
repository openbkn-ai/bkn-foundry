#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""纯字符串解析公共基类 — 无 DB 依赖，无 rdsdriver 引用"""
from server.utils.token import next_token


class RDSParser:
    """
    纯解析 Mixin — 提供各方言子类共用的辅助方法。
    不依赖 DB 连接，可在 lint 环境中安全使用。
    """

    # ── 共用辅助 ─────────────────────────────────────────────────────────────

    def _parse_column_len(self, column_sql: str):
        """解析列长度 '(n)' 或 '(n CHAR)'，返回 (len_str, remaining)"""
        if column_sql.startswith("("):
            idx = column_sql.find(")")
            if idx != -1:
                return column_sql[1:idx].strip(), column_sql[idx + 1:].strip()
        return None, column_sql

    def _parse_column_unsigned(self, column_sql: str):
        """解析 UNSIGNED 关键字，返回 (bool, remaining)"""
        token, remaining = next_token(column_sql)
        if token.upper().startswith("UNSIGNED"):
            return True, remaining.strip()
        return False, column_sql

    def _parse_default_value(self, remaining_sql: str, column_sql: str):
        """解析 DEFAULT 值（支持函数调用括号），返回 (value_str, remaining)"""
        default_value, remaining_sql = next_token(remaining_sql)
        if remaining_sql.startswith("("):
            stack, end_idx = [], 0
            for i, char in enumerate(remaining_sql):
                if char == "(":
                    stack.append(char)
                elif char == ")":
                    stack.pop()
                    if not stack:
                        end_idx = i
                        break
            else:
                raise Exception(f"不合法的建表语句, 缺少 ')': {column_sql}")
            default_value += remaining_sql[:end_idx + 1]
            remaining_sql = remaining_sql[end_idx + 1:].strip()
        return default_value, remaining_sql
