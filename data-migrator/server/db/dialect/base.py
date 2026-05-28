#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""数据库方言抽象基类 - 统一 check 和 migrate 的 SQL 执行逻辑"""
import os
from abc import ABC, abstractmethod
from logging import Logger
from server.utils.table_define import Database

try:
    import rdsdriver
except ImportError:
    rdsdriver = None

from server.utils.token import next_token, next_tokens


class RDSDialect(ABC):
    """
    统一方言基类。
    conn_config: {host, port, user, password, DB_TYPE} — 与 rdsdriver.connect 的 kwargs 一致。
    """

    def __init__(self, conn_config: dict, logger: Logger):
        self.conn_config = conn_config
        self.logger = logger
        self.DB_TYPE = conn_config.get("DB_TYPE", "")

    # ── SQL 模板常量（子类覆盖）──────────────────────────────────────────────

    SET_DATABASE_SQL = ""
    QUERY_DATABASES_SQL = ""
    CREATE_DATABASE_SQL = ""
    DROP_DATABASE_SQL = ""

    QUERY_TABLES_SQL = ""
    QUERY_TABLE_SQL = ""
    QUERY_VIEW_SQL = ""
    QUERY_COLUMNS_SQL = ""
    QUERY_COLUMN_SQL = ""
    QUERY_INDEX_SQL = None
    QUERY_CONSTRAINT_SQL = None
    COLUMN_NAME_FIELD = ""

    ADD_COLUMN_SQL = None
    MODIFY_COLUMN_SQL = None
    RENAME_COLUMN_SQL = None
    DROP_COLUMN_SQL = None

    ADD_INDEX_SQL = None
    RENAME_INDEX_SQL = None
    DROP_INDEX_SQL = None

    ADD_CONSTRAINT_SQL = None
    RENAME_CONSTRAINT_SQL = None
    DROP_CONSTRAINT_SQL = None

    RENAME_TABLE_SQL = None
    DROP_TABLE_SQL = None

    # ── 连接 ─────────────────────────────────────────────────────────────────

    def _connect(self):
        """打开新连接（上下文管理器）"""
        os.environ["DB_TYPE"] = self.DB_TYPE
        return rdsdriver.connect(**self.conn_config)

    # ── 子类必须实现 ──────────────────────────────────────────────────────────

    @abstractmethod
    def get_real_name(self, name: str) -> str:
        """去除名称中的引号和空白，各数据库引号规则不同"""
        pass

    @abstractmethod
    def parse_sql_use_db(self, sql: str) -> Database:
        """解析切库语句（USE / SET SCHEMA / SET SEARCH_PATH TO），返回 Database 对象"""
        pass

    @abstractmethod
    def parse_sql_column_define(self, column_name: str, column_sql: str):
        """解析列定义 SQL，返回 Column 对象（check 用于字段校验）"""
        pass

    @abstractmethod
    def get_column_type(self, column: dict) -> tuple:
        """返回 (data_type, type_category) 元组"""
        pass

    # ── 可选初始化（DM8/KDB9 需要）──────────────────────────────────────────

    def init_db_config(self):
        """数据库类型特殊初始化，子类按需 override"""
        pass

    # ── 公共辅助 ─────────────────────────────────────────────────────────────

    def _check_exists(self, cursor, query: str) -> bool:
        self.logger.info(f"[SQL] {query}")
        cursor.execute(query)
        result = cursor.fetchall()
        return len(result) > 0

    def _parse_object_name(self, qualified_name: str) -> str:
        """从 db.object 或 "db"."object" 中提取最后一段对象名"""
        if "." in qualified_name:
            return self.get_real_name(qualified_name.split(".")[-1])
        return self.get_real_name(qualified_name)

    # ── 幂等 SQL 执行（run_sql）──────────────────────────────────────────────

    def run_sql(self, sql_list: list):
        """
        幂等执行 SQL 列表。
        - USE/SET SCHEMA： 切换数据库
        - CREATE TABLE/VIEW/INDEX：先查是否已存在，已存在则跳过
        - DROP TABLE/VIEW/INDEX：先查是否存在，不存在则跳过
        - ALTER / RENAME：调用子类可 override 的 _run_sql_alter / _run_sql_rename
        - 其他：直接执行
        """
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    current_db = ""
                    set_db_prefix = self.SET_DATABASE_SQL.split("{db_name}")[0].upper().strip() if self.SET_DATABASE_SQL else ""

                    for sql in sql_list:
                        token, remaining = next_token(sql)
                        token = token.upper()

                        if set_db_prefix and sql.upper().lstrip().startswith(set_db_prefix + " "):
                            db = self.parse_sql_use_db(sql)
                            exec_sql = self.SET_DATABASE_SQL.format(db_name=db.DBName)
                            self.logger.info(f"[SQL] {exec_sql}")
                            cursor.execute(exec_sql)
                            current_db = db.DBName

                        elif token == "CREATE":
                            self._run_sql_create(cursor, current_db, sql, remaining)

                        elif token == "DROP":
                            self._run_sql_drop(cursor, current_db, sql, remaining)

                        elif token == "ALTER":
                            self._run_sql_alter(cursor, current_db, sql, remaining)

                        elif token == "RENAME":
                            self._run_sql_rename(cursor, current_db, sql, remaining)

                        else:
                            self.logger.info(f"[SQL] {sql}")
                            cursor.execute(sql)
                conn.commit()

        except Exception as e:
            raise Exception(f"run_sql 失败, DB_TYPE: {self.DB_TYPE}, 错误: {e}") from e

    def _run_sql_create(self, cursor, current_db, sql, remaining):
        token2, remaining2 = next_token(remaining)
        token2 = token2.upper()

        if token2 == "TABLE":
            token3, remaining3 = next_token(remaining2)
            if token3.upper() == "IF":
                _, remaining3 = next_tokens(remaining3, 2)
                token3, _ = next_token(remaining3)
            idx = token3.find("(")
            name_raw = token3[:idx] if idx != -1 else token3
            name = self.get_real_name(name_raw)
            check_sql = self.QUERY_TABLE_SQL.format(db_name=current_db, table_name=name)
            if self._check_exists(cursor, check_sql):
                if self.logger:
                    self.logger.info(f"[run_sql] table {name} 已存在, 跳过")
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif token2 == "VIEW":
            token3, remaining3 = next_token(remaining2)
            if token3.upper() == "IF":
                _, remaining3 = next_tokens(remaining3, 2)
                token3, _ = next_token(remaining3)
            idx = token3.find("(")
            name_raw = token3[:idx] if idx != -1 else token3
            name = self.get_real_name(name_raw)
            check_sql = self.QUERY_VIEW_SQL.format(db_name=current_db, view_name=name)
            if self._check_exists(cursor, check_sql):
                if self.logger:
                    self.logger.info(f"[run_sql] view {name} 已存在, 跳过")
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif token2 == "OR":
            # CREATE OR REPLACE VIEW — 天然幂等
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)

        elif token2 == "INDEX":
            self._run_sql_create_index(cursor, current_db, sql, remaining2)

        elif token2 == "UNIQUE":
            _, remaining3 = next_token(remaining2)  # skip INDEX
            self._run_sql_create_index(cursor, current_db, sql, remaining3)

        else:
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)

    def _run_sql_drop(self, cursor, current_db, sql, remaining):
        token2, remaining2 = next_token(remaining)
        token2 = token2.upper()

        if token2 == "TABLE":
            token3, remaining3 = next_token(remaining2)
            if token3.upper() == "IF":
                _, remaining3 = next_token(remaining3)
                token3, _ = next_token(remaining3)
            name = self.get_real_name(token3)
            check_sql = self.QUERY_TABLE_SQL.format(db_name=current_db, table_name=name)
            if not self._check_exists(cursor, check_sql):
                if self.logger:
                    self.logger.info(f"[run_sql] table {name} 不存在, 跳过")
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif token2 == "VIEW":
            token3, remaining3 = next_token(remaining2)
            if token3.upper() == "IF":
                _, remaining3 = next_token(remaining3)
                token3, _ = next_token(remaining3)
            name = self.get_real_name(token3)
            check_sql = self.QUERY_VIEW_SQL.format(db_name=current_db, view_name=name)
            if not self._check_exists(cursor, check_sql):
                if self.logger:
                    self.logger.info(f"[run_sql] view {name} 不存在, 跳过")
            else:
                self.logger.info(f"[SQL] {sql}")
                cursor.execute(sql)

        elif token2 == "INDEX":
            self._run_sql_drop_index(cursor, current_db, sql, remaining2)

        else:
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)

    def _run_sql_create_index(self, cursor, current_db, sql, remaining):
        if self.QUERY_INDEX_SQL is None:
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)
            return

        token, remaining2 = next_token(remaining)
        if token.upper() == "IF":
            _, remaining2 = next_tokens(remaining2, 2)
            token, remaining2 = next_token(remaining2)
        idx_name = self.get_real_name(token)
        _, remaining2 = next_token(remaining2)  # skip ON
        tbl_token, _ = next_token(remaining2)
        idx = tbl_token.find("(")
        tbl_raw = tbl_token[:idx] if idx != -1 else tbl_token
        tbl_name = self._parse_object_name(tbl_raw)
        check_sql = self.QUERY_INDEX_SQL.format(db_name=current_db, table_name=tbl_name, index_name=idx_name)
        if self._check_exists(cursor, check_sql):
            if self.logger:
                self.logger.info(f"[run_sql] index {idx_name} 已存在, 跳过")
        else:
            self.logger.info(f"[SQL] {sql}")
            cursor.execute(sql)

    def _run_sql_drop_index(self, cursor, current_db, sql, remaining):
        """默认直接执行（依赖 SQL 自带 IF EXISTS）；子类可 override"""
        self.logger.info(f"[SQL] {sql}")
        cursor.execute(sql)

    def _run_sql_alter(self, cursor, current_db, sql, remaining):
        """默认直接执行；子类按 DB 语法 override 以实现幂等"""
        self.logger.info(f"[SQL] {sql}")
        cursor.execute(sql)

    def _run_sql_rename(self, cursor, current_db, sql, remaining):
        """默认直接执行；子类按 DB 语法 override"""
        self.logger.info(f"[SQL] {sql}")
        cursor.execute(sql)

    # ── JSON 升级文件操作（check schema 执行 / migrate 执行共用）────────────

    def add_column(self, db_name, table_name, column_name, column_property, column_comment):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exists = self._check_exists(cursor, self.QUERY_COLUMN_SQL.format(
                        db_name=db_name, table_name=table_name, column_name=column_name))
                    if not exists:
                        add_sql = self.ADD_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name,
                            column_property=column_property, column_comment=column_comment)
                        self.logger.info(f"[SQL] {add_sql}")
                        cursor.execute(add_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"add_column: {db_name}.{table_name}.{column_name} 失败: {e}") from e

    def modify_column(self, db_name, table_name, column_name, column_property, column_comment):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    if self._check_exists(cursor, self.QUERY_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name)):
                        modify_sql = self.MODIFY_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name,
                            column_property=column_property, column_comment=column_comment)
                        self.logger.info(f"[SQL] {modify_sql}")
                        cursor.execute(modify_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"modify_column: {db_name}.{table_name}.{column_name} 失败: {e}") from e

    def rename_column(self, db_name, table_name, column_name, new_name, column_property, column_comment):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    if self._check_exists(cursor, self.QUERY_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name)):
                        rename_sql = self.RENAME_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name,
                            new_name=new_name, column_property=column_property, column_comment=column_comment)
                        self.logger.info(f"[SQL] {rename_sql}")
                        cursor.execute(rename_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"rename_column: {db_name}.{table_name}.{column_name} 失败: {e}") from e

    def drop_column(self, db_name, table_name, column_name):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    if self._check_exists(cursor, self.QUERY_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name)):
                        drop_sql = self.DROP_COLUMN_SQL.format(
                            db_name=db_name, table_name=table_name, column_name=column_name)
                        self.logger.info(f"[SQL] {drop_sql}")
                        cursor.execute(drop_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"drop_column: {db_name}.{table_name}.{column_name} 失败: {e}") from e

    def add_index(self, db_name, table_name, index_type, index_name, index_property, index_comment):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exists = False
                    if self.QUERY_INDEX_SQL:
                        exists = self._check_exists(cursor, self.QUERY_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_name=index_name))
                    if not exists:
                        add_sql = self.ADD_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_type=index_type,
                            index_name=index_name, index_property=index_property, index_comment=index_comment)
                        self.logger.info(f"[SQL] {add_sql}")
                        cursor.execute(add_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"add_index: {db_name}.{table_name}.{index_name} 失败: {e}") from e

    def rename_index(self, db_name, table_name, index_name, new_name):
        if self.RENAME_INDEX_SQL is None:
            raise Exception(f"当前数据库类型 {self.DB_TYPE} 不支持 rename index")
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exist = True
                    if self.QUERY_INDEX_SQL:
                        exist = self._check_exists(cursor, self.QUERY_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_name=index_name))
                    if exist:
                        rename_sql = self.RENAME_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_name=index_name, new_name=new_name)
                        self.logger.info(f"[SQL] {rename_sql}")
                        cursor.execute(rename_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"rename_index: {db_name}.{table_name}.{index_name} 失败: {e}") from e

    def drop_index(self, db_name, table_name, index_name):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exist = True
                    if self.QUERY_INDEX_SQL:
                        exist = self._check_exists(cursor, self.QUERY_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_name=index_name))
                    if exist:
                        drop_sql = self.DROP_INDEX_SQL.format(
                            db_name=db_name, table_name=table_name, index_name=index_name)
                        self.logger.info(f"[SQL] {drop_sql}")
                        cursor.execute(drop_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"drop_index: {db_name}.{table_name}.{index_name} 失败: {e}") from e

    def add_constraint(self, db_name, table_name, constraint_name, constraint_property):
        if self.ADD_CONSTRAINT_SQL is None:
            raise Exception(f"当前数据库类型 {self.DB_TYPE} 不支持 add constraint")
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exists = False
                    if self.QUERY_CONSTRAINT_SQL:
                        exists = self._check_exists(cursor, self.QUERY_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name, constraint_name=constraint_name))
                    if not exists:
                        add_sql = self.ADD_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name,
                            constraint_name=constraint_name, constraint_property=constraint_property)
                        self.logger.info(f"[SQL] {add_sql}")
                        cursor.execute(add_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"add_constraint: {db_name}.{table_name}.{constraint_name} 失败: {e}") from e

    def rename_constraint(self, db_name, table_name, constraint_name, new_name):
        if self.RENAME_CONSTRAINT_SQL is None:
            raise Exception(f"当前数据库类型 {self.DB_TYPE} 不支持 rename constraint")
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exist = True
                    if self.QUERY_CONSTRAINT_SQL:
                        exist = self._check_exists(cursor, self.QUERY_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name, constraint_name=constraint_name))
                    if exist:
                        rename_sql = self.RENAME_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name,
                            constraint_name=constraint_name, new_name=new_name)
                        self.logger.info(f"[SQL] {rename_sql}")
                        cursor.execute(rename_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"rename_constraint: {db_name}.{table_name}.{constraint_name} 失败: {e}") from e

    def drop_constraint(self, db_name, table_name, constraint_name):
        if self.DROP_CONSTRAINT_SQL is None:
            raise Exception(f"当前数据库类型 {self.DB_TYPE} 不支持 drop constraint")
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    exist = True
                    if self.QUERY_CONSTRAINT_SQL:
                        exist = self._check_exists(cursor, self.QUERY_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name, constraint_name=constraint_name))
                    if exist:
                        drop_sql = self.DROP_CONSTRAINT_SQL.format(
                            db_name=db_name, table_name=table_name, constraint_name=constraint_name)
                        self.logger.info(f"[SQL] {drop_sql}")
                        cursor.execute(drop_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"drop_constraint: {db_name}.{table_name}.{constraint_name} 失败: {e}") from e

    def rename_table(self, db_name, table_name, new_name):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    if self._check_exists(cursor, self.QUERY_TABLE_SQL.format(
                            db_name=db_name, table_name=table_name)):
                        rename_sql = self.RENAME_TABLE_SQL.format(
                            db_name=db_name, table_name=table_name, new_name=new_name)
                        self.logger.info(f"[SQL] {rename_sql}")
                        cursor.execute(rename_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"rename_table: {db_name}.{table_name} 失败: {e}") from e

    def drop_table(self, db_name, table_name):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    set_db_sql = self.SET_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {set_db_sql}")
                    cursor.execute(set_db_sql)
                    if self._check_exists(cursor, self.QUERY_TABLE_SQL.format(
                            db_name=db_name, table_name=table_name)):
                        drop_sql = self.DROP_TABLE_SQL.format(
                            db_name=db_name, table_name=table_name)
                        self.logger.info(f"[SQL] {drop_sql}")
                        cursor.execute(drop_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"drop_table: {db_name}.{table_name} 失败: {e}") from e

    def drop_db(self, db_name):
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    drop_sql = self.DROP_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {drop_sql}")
                    cursor.execute(drop_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"drop_db: {db_name} 失败: {e}") from e

    def create_db(self, db_name):
        """创建数据库"""
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    create_sql = self.CREATE_DATABASE_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {create_sql}")
                    cursor.execute(create_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"create_db: {db_name} 失败: {e}") from e

    def reset_schema(self, db_names: list):
        """重置数据库 schema（check 用：drop + create）"""
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    self.logger.info(f"[SQL] {self.QUERY_DATABASES_SQL}")
                    cursor.execute(self.QUERY_DATABASES_SQL)
                    rowlist = cursor.fetchall()
                    current_dbs = [row[0] for row in rowlist]
                    for db_name in db_names:
                        if db_name in current_dbs:
                            drop_sql = self.DROP_DATABASE_SQL.format(db_name=db_name)
                            self.logger.info(f"[SQL] {drop_sql}")
                            cursor.execute(drop_sql)
                        create_sql = self.CREATE_DATABASE_SQL.format(db_name=db_name)
                        self.logger.info(f"[SQL] {create_sql}")
                        cursor.execute(create_sql)
                conn.commit()
        except Exception as e:
            raise Exception(f"reset_schema: {db_names} 失败: {e}") from e

    def db_exists(self, db_name: str) -> bool:
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    self.logger.info(f"[SQL] {self.QUERY_DATABASES_SQL}")
                    cursor.execute(self.QUERY_DATABASES_SQL)
                    names = [row[0].upper() for row in cursor.fetchall()]
                    conn.commit()
                    return db_name.upper() in names
        except Exception as e:
            raise Exception(f"db_exists: {db_name} 检查失败: {e}") from e

    def table_exists(self, db_name: str, table_name: str) -> bool:
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    query_sql = self.QUERY_TABLE_SQL.format(
                        db_name=db_name, table_name=table_name)
                    self.logger.info(f"[SQL] {query_sql}")
                    cursor.execute(query_sql)
                    result = cursor.fetchall()
                    conn.commit()
                    return len(result) > 0
        except Exception as e:
            raise Exception(f"table_exists: {db_name}.{table_name} 检查失败: {e}") from e

    def list_tables_by_db(self, db_name: str) -> list:
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    query_sql = self.QUERY_TABLES_SQL.format(db_name=db_name)
                    self.logger.info(f"[SQL] {query_sql}")
                    cursor.execute(query_sql)
                    result = cursor.fetchall()
                    conn.commit()
                    return [row[0] for row in result]
        except Exception as e:
            raise Exception(f"list_tables_by_db: {db_name} 失败: {e}") from e

    def get_table_columns(self, db_name: str, table_name: str) -> dict:
        try:
            with self._connect() as conn:
                with conn.cursor() as cursor:
                    query_sql = self.QUERY_COLUMNS_SQL.format(db_name=db_name, table_name=table_name)
                    self.logger.info(f"[SQL] {query_sql}")
                    cursor.execute(query_sql)
                    columns = [desc[0] for desc in cursor.description]
                    schema = {}
                    for row in cursor.fetchall():
                        row_dict = dict(zip(columns, row))
                        col_name = row_dict[self.COLUMN_NAME_FIELD].upper()
                        schema[col_name] = row_dict
                    conn.commit()
                    return schema
        except Exception as e:
            raise Exception(f"get_table_columns: {db_name}.{table_name} 失败: {e}") from e
