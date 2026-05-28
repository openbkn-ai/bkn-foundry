#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""方言工厂：db_type -> 方言实例"""
from logging import Logger

from server.config.models import RDSConfig
from server.db.dialect.base import RDSDialect
from server.db.dialect.mariadb import MariaDBDialect
from server.db.dialect.mysql import MySQLDialect
from server.db.dialect.tidb import TiDBDialect
from server.db.dialect.dm8 import DM8Dialect
from server.db.dialect.kdb9 import KDB9Dialect
from server.db.dialect.goldendb import GoldenDBDialect


_DIALECT_MAP = {
    "mariadb": MariaDBDialect,
    "mysql": MySQLDialect,
    "tidb": TiDBDialect,
    "dm8": DM8Dialect,
    "kdb9": KDB9Dialect,
    "goldendb": GoldenDBDialect,
}


def create_dialect(rds_config: RDSConfig, logger: Logger) -> RDSDialect:
    """migrate 用：连接生产库"""
    db_type = rds_config.type.lower()
    dialect_cls = _DIALECT_MAP.get(db_type)
    if dialect_cls is None:
        raise Exception(f"不支持的数据库类型: {db_type}")
    conn_config = {
        "host": rds_config.host,
        "port": rds_config.port,
        "user": rds_config.user,
        "password": rds_config.password,
        "DB_TYPE": rds_config.type.upper(),
    }
    return dialect_cls(conn_config, logger)


def create_check_dialect(db_type: str, conn_config: dict, logger: Logger) -> RDSDialect:
    """check 用：连接测试库"""
    dialect_cls = _DIALECT_MAP.get(db_type.lower())
    if dialect_cls is None:
        raise Exception(f"不支持的数据库类型: {db_type}")
    return dialect_cls(conn_config, logger)
