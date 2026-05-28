#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""SQL 解析工具 - 基于 sqlparse 的 split + 注释过滤"""
from logging import Logger
from typing import List

import sqlparse


def parse_sql_file(path: str, logger: Logger) -> List[str]:
    """
    解析 SQL 文件，返回去除注释后的 SQL 语句列表。
    使用 sqlparse 进行 split，过滤空语句和纯分号。
    """
    try:
        with open(path, "r", encoding="utf-8") as fp:
            sql_str = fp.read()
    except IOError as ex:
        raise Exception(f"Cannot open SQL file: {path}, err: {ex}")

    logger.info(f"解析 SQL 文件: {path}")
    sql_str = sqlparse.format(sql_str, strip_comments=True)
    sql_list = sqlparse.split(sql_str)
    sql_list = [sql.strip() for sql in sql_list if sql.strip() and sql.strip() != ";"]
    return sql_list
