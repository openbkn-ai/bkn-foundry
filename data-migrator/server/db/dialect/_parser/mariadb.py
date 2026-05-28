#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MariaDB 纯字符串解析器 — 无 DB 依赖"""
from server.db.dialect._parser.base import RDSParser
from server.utils.table_define import Database, Column
from server.utils.token import next_token, next_tokens


class MariaDBParser(RDSParser):

    def get_real_name(self, name: str) -> str:
        real_name = name.strip(" `\n;")
        if {".", '"', "'"} & set(real_name):
            raise Exception(f"名称中包含不合法字符: {name}")
        return real_name

    def get_real_column_name(self, name: str) -> str:
        real_name = name
        idx = real_name.find("(")
        if idx != -1:
            real_name = real_name[:idx]
        real_name = real_name.strip(" `\n")
        if {".", '"', "'"} & set(real_name):
            raise Exception(f"名称中包含不合法字符: {name}")
        return real_name

    def parse_sql_use_db(self, sql: str) -> Database:
        tokens, _ = next_tokens(sql, 2)
        if len(tokens) != 2 or tokens[0].upper() != "USE":
            raise Exception(f"不合法的 USE 语句: {sql}")
        return Database(self.get_real_name(tokens[1]))

    def parse_sql_column_define(self, column_name: str, column_sql: str):
        remaining_sql = column_sql
        column_name = self.get_real_column_name(column_name)
        column_type, remaining_sql = next_token(remaining_sql)
        column = Column(column_name, column_type)
        column.ColumnLen, remaining_sql = self._parse_column_len(remaining_sql)
        column.ColumnUnsigned, remaining_sql = self._parse_column_unsigned(remaining_sql)

        while remaining_sql:
            key, remaining_sql = next_token(remaining_sql)
            key = key.upper()
            if key == "AUTO_INCREMENT":
                column.ColumnIdentity = key
            elif key == "CHARACTER":
                key2, remaining_sql = next_token(remaining_sql)
                if key2.upper() != "SET":
                    raise Exception(f"CHARACTER SET 语法错误: {column_sql}")
                column.ColumnCharset, remaining_sql = next_token(remaining_sql)
            elif key == "COLLATE":
                column.ColumnCollate, remaining_sql = next_token(remaining_sql)
            elif key == "COMMENT":
                column.ColumnComment, remaining_sql = next_token(remaining_sql)
            elif key == "NULL":
                column.ColumnNull = True
            elif key == "NOT":
                key2, remaining_sql = next_token(remaining_sql)
                if key2.upper() != "NULL":
                    raise Exception(f"NOT NULL 语法错误: {column_sql}")
                column.ColumnNull = False
            elif key == "DEFAULT":
                column.ColumnDefault, remaining_sql = self._parse_default_value(remaining_sql, column_sql)
            else:
                raise Exception(f"列定义中包含不合法的关键字 '{key}': {column_sql}")
        return column

    def get_column_type(self, column: dict) -> tuple:
        data_type = column["DATA_TYPE"].upper()
        if data_type in ("INTEGER", "INT", "SMALLINT", "TINYINT", "MEDIUMINT", "BIGINT", "BOOLEAN"):
            return data_type, "IntegerType"
        elif data_type in ("DECIMAL", "NUMERIC"):
            return data_type, "FixedPointType"
        elif data_type in ("FLOAT", "DOUBLE"):
            return data_type, "FloatingPointType"
        elif data_type in ("BIT",):
            return data_type, "BitValueType"
        elif data_type in ("CHAR", "VARCHAR", "BINARY", "VARBINARY", "TINYBLOB", "BLOB",
                           "MEDIUMBLOB", "LONGBLOB", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT"):
            return data_type, "StringType"
        elif data_type in ("DATE", "DATETIME", "TIMESTAMP", "TIME"):
            return data_type, "DateAndTimeType"
        return data_type, "UNKNOWN"
