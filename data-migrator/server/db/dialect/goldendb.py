#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
"""GoldenDB 方言"""
from logging import Logger

from server.config.models import RDSConfig
from server.db.dialect.mysql import MySQLDialect


class GoldenDBDialect(MySQLDialect):
    def __init__(self, rds_config: RDSConfig, logger: Logger):
        super().__init__(rds_config, logger)

        self.CREATE_DATABASE_SQL = "CREATE DATABASE IF NOT EXISTS {db_name} CHARSET=utf8mb4 COLLATE=utf8mb4_bin"
