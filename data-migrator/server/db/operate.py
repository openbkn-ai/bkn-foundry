#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""数据库操作 - CRUD + DDL 执行"""
from logging import Logger

from server.config.models import RDSConfig
from server.db.connection import DatabaseConnection


class OperateDB:
    def __init__(self, rds_config: RDSConfig, logger: Logger):
        self.rds_config = rds_config
        self.logger = logger
        database_connection = DatabaseConnection(self.rds_config)
        self.conn = database_connection.get_conn()

    def __del__(self):
        try:
            self.conn.close()
        except Exception:
            pass

    def run_ddl(self, sql_list: list):
        """执行 DDL 语句列表"""
        execute_sql = ""
        try:
            cursor = self.conn.cursor()
            for index, execute_sql in enumerate(sql_list):
                self.logger.info(f"Execute the {index}th statement")
                self.logger.info(f"{execute_sql}")
                cursor.execute(execute_sql)
            self.conn.commit()
        except Exception as ex:
            self.conn.rollback()
            self.logger.error(f"execute_sql failed: {execute_sql}")
            raise ex
        finally:
            cursor.close()

    def fetch_one(self, sql: str, *args):
        """执行查询，返回一条记录"""
        try:
            cursor = self.conn.cursor()
            self.logger.debug(f"{sql} ||| {args}")
            cursor.execute(sql, args)
            result = cursor.fetchone()
            self.conn.commit()
            return result
        except Exception as ex:
            self.conn.rollback()
            raise ex
        finally:
            cursor.close()

    def execute(self, sql: str, *args):
        """执行更新/插入语句"""
        try:
            cursor = self.conn.cursor()
            affect_row = cursor.execute(sql, args)
            self.conn.commit()
            return affect_row
        except Exception as ex:
            self.conn.rollback()
            raise ex
        finally:
            cursor.close()

    def insert(self, table: str, columns: dict):
        """插入一条记录（字典方式）"""
        cols = ", ".join([f"`{c}`" for c in columns.keys()])
        placeholders = ", ".join(["%s"] * len(columns))
        sql = f"INSERT INTO {table} ({cols}) VALUES ({placeholders})"
        values = list(columns.values())
        try:
            cursor = self.conn.cursor()
            cursor.execute(sql, values)
            self.conn.commit()
        except Exception as ex:
            self.conn.rollback()
            raise ex
        finally:
            cursor.close()
