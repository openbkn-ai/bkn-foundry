#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""TiDB 方言"""
from logging import Logger

from server.config.models import RDSConfig
from server.db.dialect.mysql import MySQLDialect


class TiDBDialect(MySQLDialect):
    def __init__(self, rds_config: RDSConfig, logger: Logger):
        super().__init__(rds_config, logger)

    def init_db_config(self):
        try:
            conn = self._get_conn()
            with conn.cursor() as cursor:
                sqls = [
                    "SET GLOBAL TRANSACTION ISOLATION LEVEL READ COMMITTED;",
                    "SET GLOBAL group_concat_max_len=1048576;",
                    "SET GLOBAL sql_mode='STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';",
                ]
                for sql in sqls:
                    self.logger.info(f"[SQL] {sql}")
                    cursor.execute(sql)
        except Exception as e:
            raise Exception(f"init tidb config failed, error: {e}")
