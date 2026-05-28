#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""DM8 (达梦) 方言 - DB 连接 + SQL 模板 + 幂等执行"""
from logging import Logger

from server.db.dialect.base import RDSDialect
from server.db.dialect._parser.dm8 import DM8Parser
from server.utils.token import next_token, next_tokens


class DM8Dialect(DM8Parser, RDSDialect):
    def __init__(self, conn_config: dict, logger: Logger):
        RDSDialect.__init__(self, conn_config, logger)

        self.SET_DATABASE_SQL = "SET SCHEMA {db_name}"
        self.QUERY_DATABASES_SQL = "select OWNER from dba_objects where object_type='SCH'"
        self.CREATE_DATABASE_SQL = "CREATE SCHEMA {db_name}"
        self.DROP_DATABASE_SQL = "DROP SCHEMA {db_name} CASCADE"

        self.QUERY_TABLES_SQL = "SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER='{db_name}'"
        self.QUERY_TABLE_SQL = "SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER='{db_name}' AND TABLE_NAME='{table_name}'"
        self.QUERY_VIEW_SQL = "SELECT VIEW_NAME FROM ALL_VIEWS WHERE OWNER='{db_name}' AND VIEW_NAME='{view_name}'"
        self.QUERY_COLUMNS_SQL = "SELECT * FROM ALL_TAB_COLUMNS WHERE OWNER='{db_name}' AND TABLE_NAME='{table_name}'"
        self.QUERY_COLUMN_SQL = "SELECT COLUMN_NAME FROM ALL_TAB_COLUMNS WHERE OWNER='{db_name}' AND TABLE_NAME='{table_name}' AND COLUMN_NAME='{column_name}'"
        self.QUERY_INDEX_SQL = "SELECT * FROM ALL_INDEXES WHERE OWNER='{db_name}' AND TABLE_NAME='{table_name}' AND index_name='{index_name}'"
        self.QUERY_CONSTRAINT_SQL = "SELECT * FROM ALL_CONSTRAINTS WHERE OWNER='{db_name}' AND TABLE_NAME='{table_name}' AND CONSTRAINT_NAME='{constraint_name}'"
        self.COLUMN_NAME_FIELD = "COLUMN_NAME"

        self.ADD_COLUMN_SQL = "ALTER TABLE {db_name}.\"{table_name}\" ADD COLUMN IF NOT EXISTS {column_name} {column_property}"
        self.MODIFY_COLUMN_SQL = "ALTER TABLE {db_name}.\"{table_name}\" MODIFY {column_name} {column_property}"
        self.RENAME_COLUMN_SQL = "ALTER TABLE {db_name}.\"{table_name}\" RENAME COLUMN {column_name} TO {new_name}"
        self.DROP_COLUMN_SQL = "ALTER TABLE {db_name}.\"{table_name}\" DROP COLUMN IF EXISTS {column_name}"

        self.ADD_INDEX_SQL = "CREATE {index_type} IF NOT EXISTS {index_name} ON {db_name}.\"{table_name}\" ({index_property})"
        self.RENAME_INDEX_SQL = "ALTER INDEX {db_name}.{index_name} RENAME TO {new_name}"
        self.DROP_INDEX_SQL = "DROP INDEX IF EXISTS {db_name}.{index_name}"

        self.ADD_CONSTRAINT_SQL = "ALTER TABLE {db_name}.\"{table_name}\" ADD CONSTRAINT {constraint_name} {constraint_property}"
        self.RENAME_CONSTRAINT_SQL = "ALTER TABLE {db_name}.\"{table_name}\" RENAME CONSTRAINT {constraint_name} TO {new_name}"
        self.DROP_CONSTRAINT_SQL = "ALTER TABLE {db_name}.\"{table_name}\" DROP CONSTRAINT {constraint_name} CASCADE"

        self.RENAME_TABLE_SQL = "ALTER TABLE {db_name}.\"{table_name}\" RENAME TO {new_name}"
        self.DROP_TABLE_SQL = "DROP TABLE IF EXISTS {db_name}.\"{table_name}\" CASCADE"

    def init_db_config(self):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    sqls = [
                        "SP_SET_PARA_VALUE(1,'GROUP_OPT_FLAG',1);",
                        "SP_SET_PARA_VALUE(1,'ENABLE_BLOB_CMP_FLAG',1);",
                        "SP_SET_PARA_VALUE(1,'PK_WITH_CLUSTER',0);",
                        "alter system set 'COMPATIBLE_MODE'=4 spfile;",
                        "alter system set 'MVCC_RETRY_TIMES'=15 spfile;",
                    ]
                    for sql in sqls:
                        self.logger.info(f"[SQL] {sql}")
                        cursor.execute(sql)
        except Exception as e:
            raise Exception(f"init dm8 config failed, error: {e}")

    # ── run_sql overrides ────────────────────────────────────────────────────

    def _run_sql_alter(self, cursor, current_db, sql, remaining):
        token2, remaining2 = next_token(remaining)
        token2_upper = token2.upper()

        if token2_upper == "INDEX":
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)
            return

        if token2_upper != "TABLE":
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)
            return

        tbl_token, remaining3 = next_token(remaining2)
        tbl_name = self._parse_object_name(tbl_token)
        action, remaining4 = next_token(remaining3)
        action = action.upper()

        if action == "ADD":
            obj_type, remaining5 = next_token(remaining4)
            obj_type_upper = obj_type.upper()
            if obj_type_upper == "COLUMN":
                token, _ = next_token(remaining5)
                if token.upper() == "IF":
                    _, remaining5 = next_tokens(remaining5, 3)
                    token, _ = next_token(remaining5)
                col_name = self.get_real_name(token)
                check_sql = self.QUERY_COLUMN_SQL.format(db_name=current_db, table_name=tbl_name, column_name=col_name)
                if self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] column {col_name} 已存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            elif obj_type_upper == "CONSTRAINT":
                constraint_token, _ = next_token(remaining5)
                constraint_name = self.get_real_name(constraint_token)
                check_sql = self.QUERY_CONSTRAINT_SQL.format(db_name=current_db, table_name=tbl_name, constraint_name=constraint_name)
                if self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] constraint {constraint_name} 已存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif action == "DROP":
            obj_type, remaining5 = next_token(remaining4)
            obj_type_upper = obj_type.upper()
            if obj_type_upper == "COLUMN":
                token, remaining6 = next_token(remaining5)
                if token.upper() == "IF":
                    _, remaining6 = next_token(remaining6)
                    token, _ = next_token(remaining6)
                col_name = self.get_real_name(token)
                check_sql = self.QUERY_COLUMN_SQL.format(db_name=current_db, table_name=tbl_name, column_name=col_name)
                if not self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] column {col_name} 不存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            elif obj_type_upper == "CONSTRAINT":
                constraint_token, _ = next_token(remaining5)
                constraint_name = self.get_real_name(constraint_token)
                check_sql = self.QUERY_CONSTRAINT_SQL.format(db_name=current_db, table_name=tbl_name, constraint_name=constraint_name)
                if not self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] constraint {constraint_name} 不存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif action == "MODIFY":
            col_token, _ = next_token(remaining4)
            col_name = self.get_real_name(col_token)
            check_sql = self.QUERY_COLUMN_SQL.format(db_name=current_db, table_name=tbl_name, column_name=col_name)
            if not self._check_exists(cursor, check_sql):
                if self.logger:
                    self.logger.info(f"[run_sql] column {col_name} 不存在, 跳过")
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif action == "RENAME":
            obj_type, remaining5 = next_token(remaining4)
            obj_type_upper = obj_type.upper()
            if obj_type_upper == "COLUMN":
                col_token, _ = next_token(remaining5)
                col_name = self.get_real_name(col_token)
                check_sql = self.QUERY_COLUMN_SQL.format(db_name=current_db, table_name=tbl_name, column_name=col_name)
                if not self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] column {col_name} 不存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            elif obj_type_upper == "CONSTRAINT":
                constraint_token, _ = next_token(remaining5)
                constraint_name = self.get_real_name(constraint_token)
                check_sql = self.QUERY_CONSTRAINT_SQL.format(db_name=current_db, table_name=tbl_name, constraint_name=constraint_name)
                if not self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] constraint {constraint_name} 不存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            elif obj_type_upper == "TO":
                check_sql = self.QUERY_TABLE_SQL.format(db_name=current_db, table_name=tbl_name)
                if not self._check_exists(cursor, check_sql):
                    if self.logger:
                        self.logger.info(f"[run_sql] table {tbl_name} 不存在, 跳过")
                else:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)
        else:
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)
