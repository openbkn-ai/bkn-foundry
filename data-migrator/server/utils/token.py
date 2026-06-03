#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""SQL token 解析工具 — 移植自 tools/utils/util.py"""


def find_matching_paren(sql: str) -> int:
  """
  从 sql[0]='(' 开始，找到对应匹配的 ')' 的位置。
  跳过引号（', ", `）内的括号，正确处理嵌套。
  返回 -1 表示未找到。
  """
  depth = 0
  in_quote = None
  for i, c in enumerate(sql):
    if in_quote:
      if c == in_quote:
        in_quote = None
    elif c in ("'", '"', '`'):
      in_quote = c
    elif c == '(':
      depth += 1
    elif c == ')':
      depth -= 1
      if depth == 0:
        return i
  return -1


def next_tokens(sql: str, size: int):
  tokens = []
  remaining_sql = sql
  i = 0
  while i < size and remaining_sql != "":
    i += 1
    token, remaining_sql = next_token(remaining_sql)
    tokens.append(token)

  return tokens, remaining_sql


def next_token(sql: str):
  token = ""
  remaining_sql = ""

  new_sql = sql.strip(" =\n")
  if new_sql == "":
    return token, remaining_sql

  c = new_sql[0]
  if c == '`' or c == '\'' or c == '\"':
    new_sql = new_sql[1:]
    idx = new_sql.find(c)
    if idx == -1:
      raise Exception(f"sql语句解析token错误: {sql}")
    else:
      token = new_sql[:idx]
      remaining_sql = new_sql[idx + 1:].strip(' =')
  else:
    for idx, char in enumerate(new_sql):
      if char in (' ', '=', '('):
        token = new_sql[:idx]
        remaining_sql = new_sql[idx:].strip(' =')
        break
    else:
      token = new_sql
      remaining_sql = ''

  return token, remaining_sql
