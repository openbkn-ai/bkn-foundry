#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""MySQL 方言"""
from logging import Logger

from server.config.models import RDSConfig
from server.db.dialect.mariadb import MariaDBDialect


class MySQLDialect(MariaDBDialect):
    def __init__(self, rds_config: RDSConfig, logger: Logger):
        MariaDBDialect.__init__(self, rds_config, logger)

        self.SET_DATABASE_SQL = "USE {db_name}"
        self.QUERY_DATABASES_SQL = "SELECT SCHEMA_NAME FROM INFORMATION_SCHEMA.SCHEMATA"
        self.CREATE_DATABASE_SQL = "CREATE DATABASE IF NOT EXISTS {db_name} CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
        self.DROP_DATABASE_SQL = "DROP DATABASE IF EXISTS {db_name}"

        self.QUERY_TABLES_SQL = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA='{db_name}'"
        self.QUERY_TABLE_SQL = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA='{db_name}' AND TABLE_NAME='{table_name}'"
        self.RENAME_TABLE_SQL = "RENAME TABLE {db_name}.{table_name} TO {new_name}"
        self.DROP_TABLE_SQL = "DROP TABLE IF EXISTS {db_name}.{table_name}"

        self.QUERY_VIEW_SQL = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.VIEWS WHERE TABLE_SCHEMA='{db_name}' AND TABLE_NAME='{view_name}'"

        self.COLUMN_NAME_FIELD = "COLUMN_NAME"
        self.QUERY_COLUMNS_SQL = "SELECT * FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='{db_name}' AND TABLE_NAME='{table_name}'"
        self.QUERY_COLUMN_SQL = "SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='{db_name}' AND TABLE_NAME='{table_name}' AND COLUMN_NAME='{column_name}'"
        self.ADD_COLUMN_SQL = "ALTER TABLE {db_name}.{table_name} ADD COLUMN {column_name} {column_property} COMMENT '{column_comment}'"
        self.MODIFY_COLUMN_SQL = "ALTER TABLE {db_name}.{table_name} MODIFY COLUMN {column_name} {column_property} COMMENT '{column_comment}'"
        self.RENAME_COLUMN_SQL = "ALTER TABLE {db_name}.{table_name} CHANGE COLUMN {column_name} {new_name} {column_property} COMMENT '{column_comment}'"
        self.DROP_COLUMN_SQL = "ALTER TABLE {db_name}.{table_name} DROP COLUMN {column_name}"

        self.QUERY_INDEX_SQL = "SHOW INDEX FROM {db_name}.{table_name} WHERE Key_name = '{index_name}'"
        self.ADD_INDEX_SQL = "CREATE {index_type} {index_name} ON {db_name}.{table_name} ({index_property}) COMMENT '{index_comment}'"
        self.RENAME_INDEX_SQL = "ALTER TABLE {db_name}.{table_name} RENAME INDEX {index_name} TO {new_name}"
        self.DROP_INDEX_SQL = "DROP INDEX {index_name} ON {db_name}.{table_name}"

        self.QUERY_CONSTRAINT_SQL = """SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA='{db_name}' AND TABLE_NAME='{table_name}' AND CONSTRAINT_NAME='{constraint_name}'"""
        self.ADD_CONSTRAINT_SQL = "ALTER TABLE {db_name}.{table_name} ADD CONSTRAINT {constraint_name} {constraint_property}"
        self.RENAME_CONSTRAINT_SQL = None
        self.DROP_CONSTRAINT_SQL = None
